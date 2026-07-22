package adt

import (
	"errors"
	"fmt"
	"regexp"
)

// Common constants used across ADT operations.
const (
	contentTypeXML = "application/xml"
	nsADTCore      = "http://www.sap.com/adt/core"
)

// SourceResult holds ABAP source code and its ETag for optimistic locking.
type SourceResult struct {
	Source string
	ETag   string
}

// ObjectInfo describes an ABAP repository object.
type ObjectInfo struct {
	URI         string
	Type        string
	Name        string
	Description string
	PackageName string
}

// ActivationMessage is a per-object message from an activation response.
type ActivationMessage struct {
	ObjectURI string
	Type      string // "E" error, "W" warning, "I" info
	Text      string
}

// ActivationResult is returned by ActivateObject.
type ActivationResult struct {
	Success  bool
	Messages []ActivationMessage
}

// SyntaxMessage is a single message from a syntax check.
type SyntaxMessage struct {
	Type   string // "E", "W", "I"
	Text   string
	Line   int
	Column int
}

// TestCase represents a single ABAP unit test method result.
type TestCase struct {
	Name          string
	ExecutionTime float64
	Passed        bool
	Messages      []string
}

// TestResult is returned by RunUnitTests.
type TestResult struct {
	Passed    int
	Failed    int
	Errors    int
	TestCases []TestCase
}

// TransportRequest describes a CTS transport request.
type TransportRequest struct {
	Number      string
	Owner       string
	Description string
	Status      string // "D" = modifiable, "L" = released
}

// Transport request status codes (TransportRequest.Status).
const (
	TransportStatusModifiable = "D" // request is open / editable
	TransportStatusReleased   = "L" // request has been released
)

// TransportCheckResult is returned by CheckTransport.
type TransportCheckResult struct {
	PgmID      string             // R3TR, LIMU, etc.
	Object     string             // PROG, CLAS, INTF, etc.
	ObjectName string             // object name
	DevClass   string             // package
	Result     string             // S=success, E=error
	Recording  bool               // true if object can be recorded
	Requests   []TransportRequest // available transport requests
	Messages   []string           // informational/error messages
}

// CompletionItem represents a single code completion proposal.
//
// The asXML payload from /sap/bc/adt/abapsource/codecompletion/proposal
// also carries metadata (kind, role, location, grade) and an identifier
// only — there is no description. Element details (signatures, doc text)
// come from a separate /elementinfo call.
type CompletionItem struct {
	Text string
}

// ATCCustomizingResult holds ATC configuration from the SAP system.
type ATCCustomizingResult struct {
	SystemCheckVariant string
	Properties         map[string]string
}

// ATCFinding represents a single ATC check finding.
type ATCFinding struct {
	ObjectURI    string
	Priority     string // 1=error, 2=warning, 3=info
	CheckID      string
	CheckTitle   string
	MessageTitle string
	Location     string // e.g. line number reference
}

// ATCResult is returned by RunATCCheck.
type ATCResult struct {
	WorklistID string
	Findings   []ATCFinding
}

// QueryResult holds the result of a SQL query via ADT data preview.
type QueryResult struct {
	Columns     []QueryColumn
	Rows        [][]string // row-major: Rows[rowIdx][colIdx]
	TotalRows   int
	ExecutionMs float64
}

// QueryColumn describes a single column in a query result.
type QueryColumn struct {
	Name        string
	Type        string // ABAP type: C, N, D, T, P, I, etc.
	Description string
	IsKey       bool
}

// Exception type IDs that adtler reacts to internally.
//
// These are the values SAP places in <exc:exception><type id="…"/> for the
// exceptions adtler asserts against (retry logic, error classification).
// They are stable across SAP releases and locales — far safer to compare
// against than the localised <message> text. Consumer code is welcome to
// compare against bare strings for IDs not listed here; new constants are
// added on demand.
const (
	ExceptionTypeResourceInvalidLockHandle = "ExceptionResourceInvalidLockHandle"
	ExceptionTypeResourceNoAccess          = "ExceptionResourceNoAccess" // 403 "currently editing"
	ExceptionTypeResourceLocked            = "ExceptionResourceLocked"
	ExceptionTypePreconditionFailed        = "ExceptionPreconditionFailed"
	ExceptionTypeResourceWrongData         = "ExceptionResourceWrongData"
	ExceptionTypeResourceAlreadyExists     = "ExceptionResourceAlreadyExists" // 400 on S/4, 405 on R/3
	ExceptionTypeResourceLockConflict      = "ExceptionResourceLockConflict"  // 409, S/4 only
	ExceptionTypeResourceInvalidEtag       = "ExceptionResourceInvalidEtag"   // 412
	ExceptionTypeResourceNotAcceptable     = "ExceptionResourceNotAcceptable" // 406
	ExceptionTypeUnsupportedMediaType      = "ExceptionUnsupportedMediaType"  // 415
	ExceptionTypeUnprocessableEntity       = "ExceptionUnprocessableEntity"   // 422
	ExceptionTypeNotAllowed                = "ExceptionNotAllowed"            // 405, S/4 only
	// ExceptionTypeResourceCreationFailure is raised (HTTP 500) when an object
	// create cannot complete. The PROGRAM-create endpoint reports a name
	// collision this way rather than as ExceptionResourceAlreadyExists —
	// verified live on both S/4 and R/3 (mcp-server-abap #406 / #407).
	ExceptionTypeResourceCreationFailure = "ExceptionResourceCreationFailure"
)

// ADTError is returned when SAP ADT responds with an error status.
//
// Namespace and Type carry the SAP-stable identifier from <exc:exception>
// envelopes (the ADT equivalent of an ABAP MSGID/MSGNO). They are populated
// when the error body matches the modern <exc:exception> schema and remain
// "" for legacy <ExceptionText> bodies, HTML error pages, and plain-text
// fallbacks. Callers that branch on a specific exception (e.g. resource
// locked) should compare ADTError.Type against an ExceptionType* constant
// rather than substring-matching the localised Message.
type ADTError struct {
	StatusCode int
	Namespace  string // e.g. "com.sap.adt" — empty if unknown
	Type       string // e.g. "ExceptionResourceLocked" — empty if unknown
	Message    string
}

func (e *ADTError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("SAP ADT error %d (%s): %s", e.StatusCode, e.Type, e.Message)
	}
	return fmt.Sprintf("SAP ADT error %d: %s", e.StatusCode, e.Message)
}

// ctsRequestRe matches a CTS transport request ID (e.g. "S4UK901974"):
// a 3-character system ID, the request-category letter 'K', then six digits.
// This is a language-independent format, so it survives message localisation —
// SAP's "locked in request <TR>" text is translated but the ID is not.
var ctsRequestRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{2}K[0-9]{6}\b`)

// LockingTransport returns the CTS transport request that a "locked in request
// <TR>" conflict names, if the message contains one. It is meaningful for a
// lock-conflict error (HTTP 409 / ExceptionResourceLockConflict) where the
// object is registered in another open request — a lock domain distinct from
// the runtime ENQUEUE; retargeting the write at the returned request typically
// clears the conflict. Returns ("", false) when no request ID is present.
// See mcp-server-abap#442.
//
// Yes, scraping a transport ID out of a localised, human-readable error string
// with a regex is ugly and brittle — but SAP gives us no choice: the 409
// carries the blocking request only inside the free-text <message>, with no
// structured field (no <exc:properties> entry, no header) exposing it. So we
// make the parse as robust as it can be: we key on the request-ID *format*
// rather than the surrounding words (survives translation), and we take the
// LAST match. The message orders the object name before the request
// ("Object <PGMID> <TYPE> <NAME> ... locked in request <TR> ..."), so if the
// locked object's own name happens to match the <SID>K<6-digit> shape, the
// first match would be the object name; the request ID is always the last CTS
// ID in the string (English "... request <TR> of user X" and German
// "... in Auftrag <TR> von Benutzer X gesperrt" both put it last).
func (e *ADTError) LockingTransport() (string, bool) {
	if m := ctsRequestRe.FindAllString(e.Message, -1); len(m) > 0 {
		return m[len(m)-1], true
	}
	return "", false
}

// isInvalidLockHandle returns true if the error is a 423
// ExceptionResourceInvalidLockHandle from SAP. Used by SetSource to
// decide whether to retry with a different lock handle delivery
// mechanism (header vs query param). See adtler#4.
//
// When the error carries a populated Type, this is a structural check
// against ExceptionTypeResourceInvalidLockHandle. When Type is empty
// (legacy <ExceptionText> responses), the function falls back to the
// status-code check that predates the structured envelope support.
func isInvalidLockHandle(err error) bool {
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		return false
	}
	if adtErr.Type != "" {
		return adtErr.Type == ExceptionTypeResourceInvalidLockHandle
	}
	return adtErr.StatusCode == 423
}

// isCurrentlyEditing reports whether err is SAP's "currently editing"
// (ExceptionResourceNoAccess). A source write returns this when the lock handle
// was delivered where the object's ADT handler does not read it: OO classes /
// interfaces (and other modern handlers) expect the ?lockHandle= query
// parameter, so the header-first attempt is treated as unlocked even though we
// hold a valid lock. Used by trySetSource to retry with query-param delivery.
// See aibap.mcp#443 (and #383 for the DDLS sibling).
//
// This matches on the TYPED exception only — never a bare 403. Unlike 423
// (effectively monosemous → invalid lock handle), 403 is heavily overloaded
// (generic authorization denial, transport-auth failure, HTML error pages). The
// OO handler that triggers #443 always emits Type=ExceptionResourceNoAccess via
// the modern exception envelope, so Type-matching covers the real case; a bare
// 403 must NOT trigger a query retry, which on R/3 would "poison" the write into
// a misleading 423 and hide the true 403.
func isCurrentlyEditing(err error) bool {
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		return false
	}
	return adtErr.Type == ExceptionTypeResourceNoAccess
}
