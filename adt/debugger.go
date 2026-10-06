package adt

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

// DebugSession manages a stateful ABAP debug session via ADT REST endpoints.
// It runs on its own isolated ADT session (see NewDebugSession); its short and
// long HTTP clients share that session's cookie jar and CSRF token.
//
// Every field is set once by NewDebugSession and never written again, so its
// methods may run concurrently (issue #191) — keep it that way. State that
// changes per call, such as the IDs of set breakpoints, belongs to the caller.
type DebugSession struct {
	client     *httpClient
	user       string
	terminalID string
	ideID      string
}

// resolveHTTPClient extracts the concrete *httpClient from a Client.
// Supports both direct *httpClient and *ClientRegistry (uses active client).
func resolveHTTPClient(c Client) *httpClient {
	switch v := c.(type) {
	case *httpClient:
		return v
	case *ClientRegistry:
		hc, ok := v.activeClient().(*httpClient)
		if !ok {
			panic("ClientRegistry active client is not *httpClient")
		}
		return hc
	default:
		panic("NewDebugSession requires *httpClient or *ClientRegistry")
	}
}

// NewDebugSession creates a debug session on an ISOLATED session derived from
// an existing Client's HTTP transport and credentials (see freshSession).
//
// Isolation matters because Attach/Step/GetVariable/GetStack all carry
// X-sap-adt-sessiontype: stateful, which pins the underlying HTTP session to
// one backend server instance for the rest of its cookie jar's lifetime. If a
// stateful debug call gets stuck server-side — e.g. a stepContinue that never
// returns — every other request sharing that same cookie jar risks being
// routed to (or blocked behind) the same stuck session, wedging unrelated
// tool calls (GetSource, SearchObjects, ...) until the client is discarded.
// freshSession's isolated jar contains the blast radius to this debug session
// alone. Mirrors the isolation RunClass already uses for a different reason
// (issue #106).
//
// An optional ideID can be passed to identify the debug client to SAP (default: "go-sap-adt").
func NewDebugSession(c Client, user string, ideID ...string) *DebugSession {
	hc := resolveHTTPClient(c).freshSession()
	id := "go-sap-adt"
	if len(ideID) > 0 && ideID[0] != "" {
		id = ideID[0]
	}
	return &DebugSession{
		client:     hc,
		user:       strings.ToUpper(user),
		terminalID: "MCP01",
		ideID:      id,
	}
}

// BreakpointScope selects which breakpoint set a request addresses.
type BreakpointScope string

const (
	// BreakpointScopeExternal addresses the external breakpoints of the
	// logged-on user, requestUser and ideId: set before a run, matched when a
	// listener is waiting. Other DebugSessions with the same user and IDE ID
	// see the same set.
	BreakpointScopeExternal BreakpointScope = "external"
	// BreakpointScopeDebugger addresses the breakpoints of the debugger this
	// DebugSession is attached to. Requests in this scope carry
	// X-sap-adt-sessiontype: stateful, like Attach and Step, and fail with
	// ErrNoSessionAttached when no debugger is attached.
	BreakpointScopeDebugger BreakpointScope = "debugger"
)

// maxBreakpointsPerRequest is the limit the breakpoint resource enforces.
const maxBreakpointsPerRequest = 30

// LineBreakpoint is one line breakpoint to set with SetBreakpoints.
type LineBreakpoint struct {
	// ObjectURI is the source URI, e.g. /sap/bc/adt/programs/programs/ZREPORT/source/main.
	ObjectURI string
	Line      int
	// ObjectType and ObjectName (adtcore:type, adtcore:name, e.g. "PROG/P",
	// "ZREPORT") are optional and omitted from the request when empty: the
	// resource derives program, include and line from ObjectURI alone.
	ObjectType string
	ObjectName string
}

// BreakpointResult holds the response for one requested breakpoint.
//
// A breakpoint is set only when ID is non-empty and ErrorKind and
// ErrorMessage are both empty — see IsSet. SAP can return an ID together
// with an error.
type BreakpointResult struct {
	ID string
	// ErrorKind is SAP's errorKind attribute, e.g. "existing",
	// "tooManyBreakpoints", "invalidPosition", "conditionError",
	// "nonAbapFlavour", "error". Empty when SAP sent none.
	ErrorKind    string
	ErrorMessage string
}

// IsSet reports whether SAP set the breakpoint.
func (r BreakpointResult) IsSet() bool {
	return r.ID != "" && r.ErrorKind == "" && r.ErrorMessage == ""
}

// errNoBreakpointResult is the ErrorMessage of a requested breakpoint that
// the response carried no entry for.
const errNoBreakpointResult = "not set: SAP returned no result for this breakpoint"

// ErrNoSessionAttached is returned, wrapping the *ADTError, when a
// BreakpointScopeDebugger request reaches SAP while this DebugSession has no
// attached debugger.
var ErrNoSessionAttached = errors.New("no debugger attached")

// CL_TPDA_ADT_RES_BREAKPOINTS raises ExceptionInvalidData (400) with subtype
// noSessionAttached (CX_TPDA_ADT_FAILURE=>C_SUBTYPE-NO_SESSION_ATTACHED) when
// scope=debugger finds no attached debugger. The ADT framework transmits an
// exception's subtype as the property adtExceptionSubtypeKey
// (CX_ADT_REST=>CO_EXC_SUB_TYPE_).
const (
	subtypeNoSessionAttached = "noSessionAttached"
	adtExceptionSubtypeKey   = "com.sap.adt.communicationFramework.subType"
)

// SetBreakpoints sets all given line breakpoints in ONE request and returns
// one result per requested breakpoint, in the order of bps.
//
// For BreakpointScopeExternal, SAP's handling of breakpoints that were
// already set differs by release (adtler#200): SAP_BASIS 816 replaces them
// with the requested list, SAP_BASIS 750 adds to them. Callers that need a
// known set should remove the old breakpoints with RemoveBreakpoint first.
// One request carrying the complete list behaves the same on both.
// Breakpoints are keyed by the logged-on user, requestUser and ideId: other
// DebugSessions with the same user and IDE ID see the same set.
//
// BreakpointScopeDebugger adds breakpoints to the attached debugger and is
// the scope to use while the debuggee is halted: an external-scope request
// sent then detaches the debugger on SAP_BASIS 750 (adtler#200).
//
// Results are matched to the request by clientId, not by position: the
// resource re-sorts the list and reports breakpoints rejected during
// validation first. A breakpoint the response carries no entry for is
// reported as not set.
func (d *DebugSession) SetBreakpoints(ctx context.Context, scope BreakpointScope, bps []LineBreakpoint) ([]BreakpointResult, error) {
	if len(bps) == 0 {
		return nil, errors.New("SetBreakpoints: no breakpoints given")
	}
	if len(bps) > maxBreakpointsPerRequest {
		return nil, fmt.Errorf("SetBreakpoints: %d breakpoints given, SAP accepts at most %d", len(bps), maxBreakpointsPerRequest)
	}
	if err := checkBreakpointScope(scope); err != nil {
		return nil, fmt.Errorf("SetBreakpoints: %w", err)
	}
	for i, bp := range bps {
		if bp.Line <= 0 {
			return nil, fmt.Errorf("SetBreakpoints: breakpoint %d: line %d is not a source line", i, bp.Line)
		}
	}
	// No sync mode is sent. The request transformation reads it only from a
	// <syncScope mode="…"> element; the syncMode attribute adtler sent until
	// adtler#200 was ignored on both SAP_BASIS 750 and 816, so leaving it out
	// changes nothing on the wire that SAP reads.
	reqBody := adtxml.BreakpointsRequest{
		NSDebug:       "http://www.sap.com/adt/debugger",
		NSCore:        nsADTCore,
		Scope:         string(scope),
		DebuggingMode: "user",
		RequestUser:   d.user,
		TerminalID:    d.terminalID,
		IdeID:         d.ideID,
	}
	for i, bp := range bps {
		reqBody.Breakpoints = append(reqBody.Breakpoints, adtxml.BreakpointRequest{
			Kind:     "line",
			ClientID: strconv.Itoa(i),
			URI:      fmt.Sprintf("%s#start=%d,0", bp.ObjectURI, bp.Line),
			Type:     bp.ObjectType,
			Name:     bp.ObjectName,
		})
	}
	bodyXML, err := xml.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("SetBreakpoints marshal: %w", err)
	}

	resp, err := d.client.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/debugger/breakpoints",
		strings.NewReader(xml.Header+string(bodyXML)),
		breakpointHeaders(scope, map[string]string{
			"Content-Type": contentTypeXML,
			"Accept":       "application/xml",
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("SetBreakpoints: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, wrapNoSessionAttached(err)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("SetBreakpoints read: %w", err)
	}
	var bpResp adtxml.BreakpointsResponse
	if err := xml.Unmarshal(data, &bpResp); err != nil {
		return nil, fmt.Errorf("SetBreakpoints unmarshal: %w", err)
	}
	return matchBreakpointResults(len(bps), bpResp.Breakpoints), nil
}

// matchBreakpointResults maps response entries back to the n requested
// breakpoints by clientId (the request index). Entries with an unknown or
// duplicate clientId are ignored; a requested breakpoint without an entry is
// reported as not set. The match is strict on purpose: the response
// transformation echoes clientId whenever the request carried one (verified
// on SAP_BASIS 750 and 816), and guessing an entry's owner by position is
// exactly what the resource's re-sorting makes wrong.
func matchBreakpointResults(n int, entries []adtxml.BreakpointResponse) []BreakpointResult {
	results := make([]BreakpointResult, n)
	matched := make([]bool, n)
	for _, e := range entries {
		i, err := strconv.Atoi(e.ClientID)
		if err != nil || i < 0 || i >= n || matched[i] {
			continue
		}
		matched[i] = true
		results[i] = BreakpointResult{ID: e.ID, ErrorKind: e.ErrorKind, ErrorMessage: e.ErrorMessage}
	}
	for i := range results {
		if !matched[i] {
			results[i].ErrorMessage = errNoBreakpointResult
		}
	}
	return results
}

// RemoveBreakpoint deletes one breakpoint by the ID SAP returned when it was
// set. scope must be the scope the breakpoint was set in.
//
// StopListener does not remove breakpoints, and external breakpoints outlive
// the DebugSession that set them, so a caller that wants a clean slate
// removes every ID it set.
func (d *DebugSession) RemoveBreakpoint(ctx context.Context, scope BreakpointScope, id string) error {
	if id == "" {
		return errors.New("RemoveBreakpoint: empty breakpoint ID")
	}
	if err := checkBreakpointScope(scope); err != nil {
		return fmt.Errorf("RemoveBreakpoint: %w", err)
	}
	q := url.Values{}
	q.Set("debuggingMode", "user")
	q.Set("requestUser", d.user)
	q.Set("terminalId", d.terminalID)
	q.Set("ideId", d.ideID)
	q.Set("scope", string(scope))
	// The resource does not unescape the ID a second time, so it is escaped
	// exactly once here. Verified live only for IDs without "/": IDs of
	// programs in a registered namespace contain "/", sent as %2F, and that
	// ICF passes %2F through unchanged is so far proven by unit tests only.
	path := "/sap/bc/adt/debugger/breakpoints/" + url.PathEscape(id) + "?" + q.Encode()

	resp, err := d.client.doMutate(ctx, http.MethodDelete, path, nil, breakpointHeaders(scope, nil))
	if err != nil {
		return fmt.Errorf("RemoveBreakpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return wrapNoSessionAttached(checkResponse(resp))
}

// SetBreakpoint sets one external line breakpoint on the given object. It is
// SetBreakpoints with a single breakpoint.
//
// Calling it once per breakpoint keeps only the last breakpoint on
// SAP_BASIS 816 and keeps all of them on SAP_BASIS 750 (adtler#200). To set
// several breakpoints, pass the complete list to SetBreakpoints instead.
//
// Check the result with IsSet, not ID != "": since adtler#200 a result can
// carry SAP's ID together with an error (e.g. ErrorKind "existing"), and a
// response without an entry for the breakpoint comes back as a nil error
// with ErrorMessage set instead of as an error.
func (d *DebugSession) SetBreakpoint(ctx context.Context, objectURI string, line int, objectType, objectName string) (*BreakpointResult, error) {
	results, err := d.SetBreakpoints(ctx, BreakpointScopeExternal, []LineBreakpoint{{
		ObjectURI:  objectURI,
		Line:       line,
		ObjectType: objectType,
		ObjectName: objectName,
	}})
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// breakpointHeaders adds X-sap-adt-sessiontype: stateful for
// BreakpointScopeDebugger, so the request reaches the attached debugger's
// session the way Attach and Step do.
func breakpointHeaders(scope BreakpointScope, headers map[string]string) map[string]string {
	if scope != BreakpointScopeDebugger {
		return headers
	}
	h := map[string]string{"X-sap-adt-sessiontype": "stateful"}
	for k, v := range headers {
		h[k] = v
	}
	return h
}

// checkBreakpointScope rejects a scope SAP would refuse anyway, before any
// request is sent.
func checkBreakpointScope(scope BreakpointScope) error {
	switch scope {
	case BreakpointScopeExternal, BreakpointScopeDebugger:
		return nil
	default:
		return fmt.Errorf("unknown breakpoint scope %q", scope)
	}
}

// wrapNoSessionAttached wraps err with ErrNoSessionAttached when it is SAP's
// no-session-attached response, and returns it unchanged otherwise.
func wrapNoSessionAttached(err error) error {
	var adtErr *ADTError
	if errors.As(err, &adtErr) && adtErr.Properties[adtExceptionSubtypeKey] == subtypeNoSessionAttached {
		return fmt.Errorf("%w: %w", ErrNoSessionAttached, err)
	}
	return err
}

// ListenerResult holds the result of a debug listener call.
type ListenerResult struct {
	Status      string // "attached", "timeout"
	DebuggeeID  string
	RawResponse string // full XML for debugging
}

// StartListener starts a debug listener that blocks until a breakpoint
// is hit or the timeout expires. Uses Accept: application/vnd.sap.as+xml
// to receive the debuggee session info in ASX XML format.
func (d *DebugSession) StartListener(ctx context.Context, timeoutSeconds int) (*ListenerResult, error) {
	path := fmt.Sprintf("/sap/bc/adt/debugger/listeners?debuggingMode=user&requestUser=%s&terminalId=%s&ideId=%s&timeout=%d",
		d.user, d.terminalID, d.ideID, timeoutSeconds)

	// The listener long-polls for up to timeoutSeconds, longer than the short
	// client's timeout allows. Send it through the long client (no timeout of
	// its own) and bound it with a context deadline of timeoutSeconds+10 s; an
	// earlier caller deadline still wins. The long client shares this session's
	// cookie jar and CSRF token, so the poll runs in the same ADT session as
	// Attach/Step. Never mutate the shared short client's Timeout here: other
	// requests on this DebugSession may be in flight concurrently (issue #188).
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+10)*time.Second)
	defer cancel()

	resp, err := d.client.doMutateLong(ctx, http.MethodPost, path, nil,
		map[string]string{"Accept": "application/vnd.sap.as+xml"})
	if err != nil {
		return nil, fmt.Errorf("StartListener: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, _ := io.ReadAll(resp.Body)
	if len(data) == 0 {
		return &ListenerResult{Status: "timeout"}, nil
	}

	// Parse debuggee ID from ASX XML response
	debuggeeID := extractXMLTag(string(data), "DEBUGGEE_ID")
	return &ListenerResult{Status: "attached", DebuggeeID: debuggeeID, RawResponse: string(data)}, nil
}

// extractXMLTag extracts the text content of a simple XML tag.
func extractXMLTag(s, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return s[i+len(open) : i+j]
}

// StopListener stops the debug listener.
func (d *DebugSession) StopListener(ctx context.Context) error {
	path := fmt.Sprintf("/sap/bc/adt/debugger/listeners?debuggingMode=user&requestUser=%s&terminalId=%s&ideId=%s",
		d.user, d.terminalID, d.ideID)

	resp, err := d.client.doMutate(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return fmt.Errorf("StopListener: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

// GetDebuggeeSessions returns active debuggee sessions.
func (d *DebugSession) GetDebuggeeSessions(ctx context.Context) ([]byte, error) {
	resp, err := d.client.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/debugger?method=getDebuggeeSessions",
		nil,
		map[string]string{"Accept": "application/vnd.sap.as+xml"})
	if err != nil {
		return nil, fmt.Errorf("GetDebuggeeSessions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	data, _ := io.ReadAll(resp.Body)
	return data, nil
}

// Attach attaches to an active debuggee session.
// Uses X-sap-adt-sessiontype: stateful to keep the work process for subsequent calls.
//
// Retries on a fast-failing 500 AdiFailed response (see retryOnAdiFailed) —
// a known-intermittent SAP kernel-side debugger fault documented on
// aibap.mcp#513.
func (d *DebugSession) Attach(ctx context.Context, debuggeeID string) error {
	_, err := retryOnAdiFailed(ctx, func() (struct{}, error) {
		return struct{}{}, d.attachOnce(ctx, debuggeeID)
	})
	return err
}

func (d *DebugSession) attachOnce(ctx context.Context, debuggeeID string) error {
	path := fmt.Sprintf("/sap/bc/adt/debugger?method=attach&debuggeeId=%s", debuggeeID)
	resp, err := d.client.doMutate(ctx, http.MethodPost, path, nil,
		map[string]string{"Accept": "application/xml", "X-sap-adt-sessiontype": "stateful"})
	if err != nil {
		return fmt.Errorf("Attach: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

// DebuggeeEndedError is returned by Step when a step action's HTTP request
// times out AND a follow-up check confirms no debuggee session remains.
//
// SAP's ADT debugger kernel call (invoked inside CL_TPDA_ADT_RES_DEBUGGER's
// local LCL_HANDLER_CONTROL=>INVOKE) never returns an HTTP response when a
// step action runs the debuggee past its last statement — the ABAP side
// genuinely finishes executing, but the request itself hangs until the
// client's own timeout fires, with no SAP-side fix available from this
// client. See aibap.mcp#513 for the live investigation and root-cause
// derivation (kernel-level, not an adtler/aibap.mcp bug).
//
// Distinguishing this from a genuine hang matters: an opaque timeout error
// forces every caller to separately call GetDebuggeeSessions to find out
// whether stepping actually worked. This type lets callers treat "debuggee
// ended" as the expected, successful conclusion of a step action rather than
// a failure.
type DebuggeeEndedError struct {
	Action     string // the step action that timed out, e.g. "stepContinue"
	Underlying error  // the original timeout error
}

func (e *DebuggeeEndedError) Error() string {
	return fmt.Sprintf("Step(%s): request timed out and no debuggee session remains — "+
		"the debuggee likely ran to completion (see aibap.mcp#513): %v", e.Action, e.Underlying)
}

func (e *DebuggeeEndedError) Unwrap() error { return e.Underlying }

// isAdiFailed reports whether err is a fast-failing 500 AdiFailed response —
// the ADT debugger REST framework's generic wrapped-exception error, known
// to be intermittent for Attach and (rarer) Step (aibap.mcp#513). Deliberately
// distinct from a bare timeout (no HTTP response at all), which is handled
// separately by DebuggeeEndedError.
//
// A *DebuggeeEndedError is never treated as AdiFailed here, even for a future
// change that recognizes SAP's native fast-path debuggee-ended signal (an
// AdiFailed response with the ADT framework's own "ended" subtype — see
// adtler#159) inside stepOnce and returns *DebuggeeEndedError for it directly:
// that is a successful, terminal outcome to report as-is, never something to
// retry. The explicit type check below guards that case regardless of what
// isAdiFailed's own Type-matching would otherwise say about its Underlying.
func isAdiFailed(err error) bool {
	var ended *DebuggeeEndedError
	if errors.As(err, &ended) {
		return false
	}
	// A debuggee-ended AdiFailed can also reach here directly from Attach
	// (attaching to a debuggee that already ran to completion) — attachOnce
	// has no DebuggeeEndedError-shaped success to return, unlike stepOnce, so
	// it never gets the chance to pre-classify and short-circuit. Exclude it
	// here too: retrying a condition that can never succeed only wastes time
	// (confirmed live: ~7-17s across 3 attempts — see isDebuggeeEndedAdiFailed).
	if isDebuggeeEndedAdiFailed(err) {
		return false
	}
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		return false
	}
	return adtErr.Type == ExceptionTypeAdiFailed
}

const (
	adiFailedMaxRetries = 2
	adiFailedRetryDelay = 500 * time.Millisecond
)

// retryOnAdiFailed calls fn up to 1+adiFailedMaxRetries times, retrying only
// when fn's error is a fast-failing AdiFailed 500 (aibap.mcp#513: a known
// intermittent SAP kernel-side debugger fault with no further diagnosable
// detail — live investigation found retrying the same call sometimes
// succeeds). Any other error — including a bare timeout, already handled
// elsewhere via DebuggeeEndedError — returns immediately on the first
// attempt, unretried. A context cancellation during the retry delay aborts
// immediately with the last error seen.
func retryOnAdiFailed[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= adiFailedMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(adiFailedRetryDelay):
			case <-ctx.Done():
				return zero, lastErr
			}
		}
		v, err := fn()
		if err == nil {
			return v, nil
		}
		if !isAdiFailed(err) {
			return zero, err
		}
		lastErr = err
	}
	return zero, fmt.Errorf("after %d retries on AdiFailed (aibap.mcp#513): %w", adiFailedMaxRetries, lastErr)
}

// isTimeoutErr reports whether err represents an HTTP client timeout —
// either a context deadline or a *url.Error whose Timeout() is true (the
// shape net/http wraps http.Client.Timeout expirations in).
func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) && urlErr.Timeout()
}

// debuggeeSessionsEmpty reports whether GetDebuggeeSessions currently shows
// no active sessions. Mirrors the emptiness check aibap.mcp's own
// buildDebugSessionsResult uses (an empty/whitespace-only ASX body means no
// sessions).
//
// Deliberately uses a fresh, detached context with its own short deadline
// rather than the (possibly already-expired or cancelled) ctx the caller's
// Step call was made with — reusing a dead context here would make this
// follow-up check fail immediately regardless of the debuggee's real state.
func (d *DebugSession) debuggeeSessionsEmpty(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	data, err := d.GetDebuggeeSessions(checkCtx)
	if err != nil {
		return false
	}
	return len(bytes.TrimSpace(data)) == 0
}

// Step executes a debug action: stepInto, stepOver, stepReturn,
// stepContinue, stepJumpToLine, stepRunToLine.
//
// If the request times out and a follow-up GetDebuggeeSessions call confirms
// no session remains, Step returns *DebuggeeEndedError instead of a bare
// timeout — see its doc comment for why. Any other error (including a
// timeout where a session is still alive, or where the follow-up check
// itself fails) is returned unchanged.
//
// SAP can also report the same debuggee-ended condition as a *fast* 500
// AdiFailed instead of hanging — see isDebuggeeEndedAdiFailed — which Step
// also recognizes and reports as *DebuggeeEndedError.
//
// Any other fast-failing (non-timeout) 500 AdiFailed response is retried —
// see retryOnAdiFailed — since it's a known-intermittent SAP kernel-side
// fault (aibap.mcp#513), distinct from both debuggee-ended shapes above.
func (d *DebugSession) Step(ctx context.Context, action string) ([]byte, error) {
	return retryOnAdiFailed(ctx, func() ([]byte, error) {
		return d.stepOnce(ctx, action)
	})
}

func (d *DebugSession) stepOnce(ctx context.Context, action string) ([]byte, error) {
	path := fmt.Sprintf("/sap/bc/adt/debugger?method=%s", action)
	resp, err := d.client.doMutate(ctx, http.MethodPost, path, nil,
		map[string]string{"Accept": "application/xml", "X-sap-adt-sessiontype": "stateful"})
	if err != nil {
		if isTimeoutErr(err) && d.debuggeeSessionsEmpty(ctx) {
			return nil, &DebuggeeEndedError{Action: action, Underlying: err}
		}
		return nil, fmt.Errorf("Step: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		if isDebuggeeEndedAdiFailed(err) {
			return nil, &DebuggeeEndedError{Action: action, Underlying: err}
		}
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

// cxTpdapiDebuggeeEnded is the ABAP exception class SAP's ADT debugger
// framework wraps (as the "previous" exception) when a step action fails
// because the debuggee already ran to completion — SAP's own fast-path
// signal for the same condition #529/DebuggeeEndedError already recognizes
// via the slower bare-timeout shape. Confirmed live on aibap.mcp#513/adtler#159
// (2026-09-30, ECC): a stepContinue run off the program's last statement
// returned a 500 AdiFailed carrying
// Properties["previous1ExceptionClassName"] = "CX_TPDAPI_DEBUGGEE_ENDED" —
// a stable, language-independent identifier, unlike the German
// Properties["previous1Text"]/Message alongside it ("Debuggee-Session wurde
// angehalten").
const cxTpdapiDebuggeeEnded = "CX_TPDAPI_DEBUGGEE_ENDED"

// isDebuggeeEndedAdiFailed reports whether err is a fast (non-timeout) 500
// AdiFailed response whose wrapped "previous" exception is
// CX_TPDAPI_DEBUGGEE_ENDED — SAP's own signal that the debuggee already ran
// to completion, not a transient fault. stepOnce checks this before falling
// through to a plain error, so callers get *DebuggeeEndedError (a success)
// for this case exactly as they already do for the slower timeout shape.
// Deliberately never retried: retryOnAdiFailed's isAdiFailed already excludes
// any error that is/wraps *DebuggeeEndedError, so returning it here from
// stepOnce (before retryOnAdiFailed ever sees the raw AdiFailed) skips the
// pointless retry outright — confirmed live to otherwise cost ~7-17s across
// 3 attempts for a condition that can never succeed on retry.
func isDebuggeeEndedAdiFailed(err error) bool {
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		return false
	}
	if adtErr.Type != ExceptionTypeAdiFailed {
		return false
	}
	return adtErr.Properties["previous1ExceptionClassName"] == cxTpdapiDebuggeeEnded
}

// GetVariable reads a variable value from the debug session.
// Uses the debugger main endpoint (POST /debugger?method=getVariables) to stay
// in the stateful HTTP session. The separate GET /debugger/variables/ endpoint
// uses a different ICF handler that doesn't share the stateful work process.
func (d *DebugSession) GetVariable(ctx context.Context, name string) ([]byte, error) {
	path := fmt.Sprintf("/sap/bc/adt/debugger?method=getVariableValue&variableName=%s", name)
	resp, err := d.client.doMutate(ctx, http.MethodPost, path, nil,
		map[string]string{
			"Accept":                "text/plain",
			"X-sap-adt-sessiontype": "stateful",
		})
	if err != nil {
		return nil, fmt.Errorf("GetVariable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

// GetStack returns the current call stack.
// Uses the debugger main endpoint (POST /debugger?method=getStack) to stay
// in the stateful HTTP session.
func (d *DebugSession) GetStack(ctx context.Context) ([]byte, error) {
	resp, err := d.client.doMutate(ctx, http.MethodPost, "/sap/bc/adt/debugger?method=getStack", nil,
		map[string]string{"Accept": "application/xml", "X-sap-adt-sessiontype": "stateful"})
	if err != nil {
		return nil, fmt.Errorf("GetStack: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

// SetWatchpoint sets a watchpoint on a variable to break when its value changes.
func (d *DebugSession) SetWatchpoint(ctx context.Context, variableName, condition string) ([]byte, error) {
	path := fmt.Sprintf("/sap/bc/adt/debugger/watchpoints?variableName=%s", variableName)
	if condition != "" {
		path += "&condition=" + condition
	}
	resp, err := d.client.doMutate(ctx, http.MethodPost, path, nil,
		map[string]string{"Accept": "application/xml"})
	if err != nil {
		return nil, fmt.Errorf("SetWatchpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}
