package adt

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// SourceClient reads and writes ABAP source code.
type SourceClient interface {
	GetSource(ctx context.Context, objectURI string) (*SourceResult, error)
	GetClassDefinition(ctx context.Context, objectURI string) (*SourceResult, error)
	SetSource(ctx context.Context, objectURI, source, lockHandle, transport, etag string) (string, error)
	GetIncludeSource(ctx context.Context, objectURI, include string) (*SourceResult, error)
	SetIncludeSource(ctx context.Context, objectURI, include, source, lockHandle, transport, etag string) (string, error)
	CreateTestInclude(ctx context.Context, objectURI, lockHandle, transport string) error
	PrettyPrint(ctx context.Context, source string) (string, error)
	GetCompletions(ctx context.Context, objectURI, source string, line, column int) ([]CompletionItem, error)
}

// ObjectClient manages ABAP object lifecycle.
type ObjectClient interface {
	CreateObject(ctx context.Context, objectType, name, packageName, description, transport string) error
	CreateFunctionModule(ctx context.Context, groupName, moduleName, description, packageName, transport string) error
	CreatePackage(ctx context.Context, name, description, responsible, softwareComponent, transportLayer, transport string) error
	DeleteObject(ctx context.Context, objectURI, lockHandle, transport string) error
	ActivateObjects(ctx context.Context, objectURIs []string) (*ActivationResult, error)
	GetInactiveObjects(ctx context.Context) ([]ObjectInfo, error)
}

// LockClient handles object locking.
type LockClient interface {
	LockObject(ctx context.Context, objectURI string) (string, error)
	UnlockObject(ctx context.Context, objectURI, lockHandle string) error
}

// DocuClient provides ABAP documentation.
type DocuClient interface {
	GetABAPDoc(ctx context.Context, keyword string) (string, error)
	GetTextElements(ctx context.Context, objectURI string) (*TextElements, error)
	GetMessageClass(ctx context.Context, messageClassName string) (*MessageClassInfo, error)
	SearchMessages(ctx context.Context, query string, maxResults int) ([]MessageSearchResult, error)
	SetMessages(ctx context.Context, messageClassName, etag string, messages []Message) error
	SetTextElements(ctx context.Context, objectURI string, symbols []TextSymbol, selections []SelectionText, lockHandle, transport string) error
}

// NavigationClient resolves source references.
type NavigationClient interface {
	// NavigateToDefinition resolves the symbol at the cursor position
	// embedded in sourceURI (as `...#start=line,column`) to its definition.
	// The source code that the cursor refers to must be passed in source —
	// the SAP handler reads the body as plain text and uses it together
	// with the position fragment to compute the navigation target. Passing
	// the source the caller already has avoids a round-trip GetSource call.
	NavigateToDefinition(ctx context.Context, sourceURI, source string) (string, error)
}

// SearchClient provides object discovery.
type SearchClient interface {
	SearchObjects(ctx context.Context, query, objectType string, maxResults int) ([]ObjectInfo, error)
	SearchPackages(ctx context.Context, query string, maxResults int) ([]ObjectInfo, error)
	WhereUsed(ctx context.Context, objectURI string) ([]ObjectInfo, error)
	BrowsePackage(ctx context.Context, packageName string) ([]ObjectInfo, error)
	GetObjectInfo(ctx context.Context, objectURI string) (*ObjectInfo, error)
}

// RefactoringClient provides code refactoring operations.
type RefactoringClient interface {
	Rename(ctx context.Context, sourceURI, newName, transport string) (*RenameResult, error)
}

// DDICClient provides DDIC metadata.
type DDICClient interface {
	GetTableFields(ctx context.Context, tableName string) ([]FieldInfo, error)
}

// DumpClient reads ABAP short dumps (ST22).
type DumpClient interface {
	ListShortDumps(ctx context.Context, from, to, user string) ([]ShortDumpHeader, error)
	GetShortDumps(ctx context.Context, from, to, user string) ([]ShortDump, error)
}

// QualityClient runs checks and tests.
type QualityClient interface {
	SyntaxCheck(ctx context.Context, objectURI string) ([]SyntaxMessage, error)
	BatchSyntaxCheck(ctx context.Context, objectURIs []string) []ObjectSyntaxResult
	RunUnitTests(ctx context.Context, objectURI string, timeoutSeconds int) (*TestResult, error)
	RunATCCheck(ctx context.Context, objectURIs []string, checkVariant string) (*ATCResult, error)
	GetATCCustomizing(ctx context.Context) (*ATCCustomizingResult, error)
}

// VersionClient provides version history and comparison.
type VersionClient interface {
	GetVersionHistory(ctx context.Context, objectURI string) ([]VersionInfo, error)
	GetVersionSource(ctx context.Context, contentURI string) (string, error)
	DiffActiveInactive(ctx context.Context, objectURI string) (*DiffResult, error)
}

// TransportClient manages CTS transports.
type TransportClient interface {
	CheckTransport(ctx context.Context, pgmID, object, objectName string) (*TransportCheckResult, error)
	CreateTransport(ctx context.Context, category, target, description, devClass string) (string, error)
	CreateTransportTask(ctx context.Context, parentTransport, owner, description string) (string, error)
	DeleteTransport(ctx context.Context, transportNumber string) error
	ReleaseTransport(ctx context.Context, transportNumber string) error
	ReleaseTransportWithTasks(ctx context.Context, transportNumber string) error
	GetTransportRequests(ctx context.Context, user, status string) ([]TransportRequest, error)
	AddToTransport(ctx context.Context, objectURI, transport string) error
	RemoveFromTransport(ctx context.Context, taskNumber, parentTransport, pgmID, objectType, objectName, wbType, position string) error
	GetTransportInfo(ctx context.Context, transportNumber string) (*TransportRequest, error)
	GetTransportObjects(ctx context.Context, transportNumber string) ([]TransportObject, error)
	GetTransportTasks(ctx context.Context, transportNumber string) ([]string, error)
}

// ExportClient handles package exports.
type ExportClient interface {
	ExportPackage(ctx context.Context, packageName string) ([]byte, error)
}

// QueryClient runs data queries.
type QueryClient interface {
	RunQuery(ctx context.Context, sql string, maxRows int) (*QueryResult, error)
}

// EnhancementClient reads and writes BAdI enhancement spots and implementations.
type EnhancementClient interface {
	GetEnhancementSpot(ctx context.Context, spotName string) (*EnhancementSpotInfo, error)
	GetEnhancementImplementation(ctx context.Context, implName string) (*BAdIImplementationInfo, error)
	SetEnhancementImplementation(ctx context.Context, implName, xmlBody, lockHandle, transport, etag string) error
}

// SystemClient provides system metadata.
type SystemClient interface {
	SystemInfo() (host, client string)
	Logout(ctx context.Context) error
}

// Client is the full ADT client combining all capabilities.
type Client interface {
	SourceClient
	ObjectClient
	LockClient
	DocuClient
	NavigationClient
	SearchClient
	DDICClient
	QualityClient
	RefactoringClient
	VersionClient
	TransportClient
	ExportClient
	QueryClient
	EnhancementClient
	DumpClient
	SystemClient
}

type httpClient struct {
	cfg              sapmcpconfig.SAPSystem
	http             *http.Client
	httpLong         *http.Client // long-timeout client for large queries; shares transport + cookie jar
	mu               sync.Mutex
	csrfToken        string
	hasSecureCookies bool                         // true if SAP sets Secure cookies on an HTTP connection
	discovery        map[string][]string          // endpoint → accepted content types from discovery
	accessToken      string                       // OAuth2 access token (empty = Basic Auth)
	onTokenRefresh   func(string) (string, error) // callback to refresh token, returns new access token
	pollInterval     time.Duration                // polling interval for background runs (default: 10s)
}

// NewClient creates a new ADT HTTP client configured from cfg.
func NewClient(cfg sapmcpconfig.SAPSystem) Client {
	return NewClientWithPollInterval(cfg, backgroundRunPollInterval)
}

// NewClientWithPollInterval creates a new ADT HTTP client with a custom polling interval
// for background release jobs. Use NewClient for the default 10-second interval.
func NewClientWithPollInterval(cfg sapmcpconfig.SAPSystem, pollInterval time.Duration) Client {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.TLSSkipVerify, //nolint:gosec
		},
	}
	return &httpClient{
		cfg: cfg,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			Jar:       jar,
		},
		httpLong: &http.Client{
			Timeout:   0, // no timeout; caller controls via context deadline
			Transport: transport,
			Jar:       jar,
		},
		pollInterval: pollInterval,
	}
}

// NewClientWithToken creates a Client using Bearer token auth.
// onRefresh is called with the current access token when a 401 occurs; it should return a new access token.
func NewClientWithToken(cfg sapmcpconfig.SAPSystem, accessToken string, onRefresh func(string) (string, error)) Client {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.TLSSkipVerify, //nolint:gosec
		},
	}
	return &httpClient{
		cfg: cfg,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			Jar:       jar,
		},
		httpLong: &http.Client{
			Timeout:   0, // no timeout; caller controls via context deadline
			Transport: transport,
			Jar:       jar,
		},
		accessToken:    accessToken,
		onTokenRefresh: onRefresh,
	}
}

// NewClientWithTransport creates a Client using a caller-supplied http.RoundTripper.
// Use this when the underlying HTTP transport requires special routing — for example,
// when running inside SAP BTP Cloud Foundry and reaching an on-premise system through
// the BTP Connectivity service's SOCKS5 proxy. Authentication (Basic Auth from cfg.User
// and cfg.Password) still flows through cfg; the transport is responsible only for the
// network path.
//
// Unlike NewClient, TLSSkipVerify from cfg is NOT applied — the caller's RoundTripper
// owns its own TLS configuration. If you need Bearer token auth with a custom transport,
// use NewClientWithTokenAndTransport (not yet available; see GitHub issue tracker).
func NewClientWithTransport(cfg sapmcpconfig.SAPSystem, transport http.RoundTripper) Client {
	return NewClientWithTransportAndPollInterval(cfg, transport, backgroundRunPollInterval)
}

// NewClientWithTransportAndPollInterval is like NewClientWithTransport but with a custom
// polling interval for background release jobs. Use NewClientWithTransport for the default
// 10-second interval.
//
// Like NewClientWithTransport, TLSSkipVerify from cfg is NOT applied — the caller's
// RoundTripper owns its own TLS configuration.
func NewClientWithTransportAndPollInterval(cfg sapmcpconfig.SAPSystem, transport http.RoundTripper, pollInterval time.Duration) Client {
	jar, _ := cookiejar.New(nil)
	return &httpClient{
		cfg: cfg,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			Jar:       jar,
		},
		httpLong: &http.Client{
			Timeout:   0, // no timeout; caller controls via context deadline
			Transport: transport,
			Jar:       jar,
		},
		pollInterval: pollInterval,
	}
}

// SystemInfo returns the SAP system host URL and client number.
func (c *httpClient) SystemInfo() (host, client string) {
	return c.cfg.Host, c.cfg.Client
}

// Logout invalidates the SAP session. After calling this, the CSRF token
// and session cookies are no longer valid.
func (c *httpClient) Logout(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Host+"/sap/public/bc/icf/logoff", nil)
	if err != nil {
		return fmt.Errorf("Logout: %w", err)
	}
	c.setAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Logout: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	c.mu.Lock()
	c.csrfToken = ""
	// Replace the cookie jar with a fresh one so no stale session cookies
	// leak into the next request. The old jar may still hold SAP_SESSIONID_*
	// and sap-usercontext cookies that reference the now-terminated server
	// session — sending them alongside new cookies on some SAP versions
	// confuses the session manager.
	jar, _ := cookiejar.New(nil)
	c.http.Jar = jar
	c.httpLong.Jar = jar
	c.mu.Unlock()
	return nil
}

// fetchCSRFToken performs the CSRF preflight GET and caches the token and session cookies.
// Caller must hold c.mu.
func (c *httpClient) fetchCSRFToken(ctx context.Context) error {
	url := c.cfg.Host + "/sap/bc/adt/discovery"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	req.Header.Set("X-CSRF-Token", "Fetch")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("CSRF fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Parse discovery XML to cache accepted content types per endpoint.
	body, _ := io.ReadAll(resp.Body)
	if len(body) > 0 {
		c.discovery = parseDiscovery(body)
	}

	c.csrfToken = resp.Header.Get("X-CSRF-Token")
	c.hasSecureCookies = hasSecureCookieOnHTTP(c.cfg.Host, resp.Header)
	return nil
}

// ensureCSRF populates the CSRF token (and, as a side effect, the
// discovery cache) exactly once. It acquires c.mu for the duration
// of the check, so callers MUST NOT already hold c.mu. Subsequent
// calls after the token is populated are a cheap mutex-only no-op.
//
// Call this from any method that needs discovery data available
// BEFORE the request-level preflight inside doReadWith/doMutateWith
// has a chance to run — e.g. callers that compute content-type
// headers via sourceContentType or acceptHeaderForURI before
// invoking doRead/doMutate.
func (c *httpClient) ensureCSRF(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.csrfToken != "" {
		return nil
	}
	return c.fetchCSRFToken(ctx)
}

// hasSecureCookieOnHTTP returns true if the response sets a cookie with the
// Secure flag while the connection uses plain HTTP. This combination silently
// breaks CSRF validation on S4 systems because the client never sends the
// cookie back.
func hasSecureCookieOnHTTP(host string, header http.Header) bool {
	if strings.HasPrefix(host, "https://") {
		return false
	}
	for _, setCookie := range header.Values("Set-Cookie") {
		if strings.Contains(strings.ToLower(setCookie), "; secure") {
			return true
		}
	}
	return false
}

func (c *httpClient) setAuth(req *http.Request) {
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	} else {
		req.SetBasicAuth(c.cfg.User, c.cfg.Password)
	}
	if c.cfg.Client != "" {
		req.Header.Set("sap-client", c.cfg.Client)
	}
}

// doRead performs a GET request with the default HTTP client (30-second timeout).
func (c *httpClient) doRead(ctx context.Context, path string, headers map[string]string) (*http.Response, error) {
	return c.doReadWith(ctx, c.http, path, headers)
}

// doReadLong performs a GET request with the long-timeout HTTP client.
// Use for operations that may take minutes (e.g., exporting large packages).
func (c *httpClient) doReadLong(ctx context.Context, path string, headers map[string]string) (*http.Response, error) {
	return c.doReadWith(ctx, c.httpLong, path, headers)
}

func (c *httpClient) doReadWith(ctx context.Context, hc *http.Client, path string, headers map[string]string) (*http.Response, error) {
	path = encodeNamespacePath(path)

	if err := c.ensureCSRF(ctx); err != nil {
		return nil, err
	}

	makeReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Host+path, nil)
		if err != nil {
			return nil, err
		}
		c.setAuth(req)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}

	req, err := makeReq()
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		c.mu.Lock()
		if c.onTokenRefresh != nil {
			newToken, err := c.onTokenRefresh(c.accessToken)
			if err != nil {
				c.mu.Unlock()
				return nil, fmt.Errorf("token refresh failed: %w", err)
			}
			c.accessToken = newToken
		}
		if err := c.fetchCSRFToken(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.mu.Unlock()
		req2, err := makeReq()
		if err != nil {
			return nil, err
		}
		return hc.Do(req2)
	}
	return resp, nil
}

// doMutate performs a POST/PUT/DELETE with CSRF token and retry on 403/401.
// Uses the default HTTP client (30-second timeout).
func (c *httpClient) doMutate(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	return c.doMutateWith(ctx, c.http, method, path, body, headers)
}

// doMutateLong is like doMutate but uses the long-timeout HTTP client (httpLong).
// Intended for long-running queries where the caller controls the deadline via context.
func (c *httpClient) doMutateLong(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	return c.doMutateWith(ctx, c.httpLong, method, path, body, headers)
}

// doMutateWith performs a POST/PUT/DELETE with CSRF token and retry on 403/401,
// using the given HTTP client. Body is buffered so it can be replayed on retry.
func (c *httpClient) doMutateWith(ctx context.Context, hc *http.Client, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	path = encodeNamespacePath(path)
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("buffering request body: %w", err)
		}
	}
	newBody := func() io.Reader {
		if bodyBytes == nil {
			return nil
		}
		return bytes.NewReader(bodyBytes)
	}

	if err := c.ensureCSRF(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	token := c.csrfToken
	c.mu.Unlock()

	resp, err := c.execMutateWith(ctx, hc, method, path, newBody(), headers, token)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		c.mu.Lock()
		if resp.StatusCode == http.StatusUnauthorized && c.onTokenRefresh != nil {
			newToken, err := c.onTokenRefresh(c.accessToken)
			if err != nil {
				c.mu.Unlock()
				return nil, fmt.Errorf("token refresh failed: %w", err)
			}
			c.accessToken = newToken
		}
		if err := c.fetchCSRFToken(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		token = c.csrfToken
		secureCookies := c.hasSecureCookies
		c.mu.Unlock()

		retryResp, err := c.execMutateWith(ctx, hc, method, path, newBody(), headers, token)
		if err != nil {
			return nil, err
		}
		if retryResp.StatusCode == http.StatusForbidden && secureCookies {
			_ = retryResp.Body.Close()
			return nil, fmt.Errorf("CSRF token validation failed after retry — the SAP system sets Secure cookies " +
				"but the connection uses plain HTTP, so session cookies are silently dropped. " +
				"Change the host URL from http:// to https:// to fix this")
		}
		return retryResp, nil
	}

	return resp, nil
}

// execMutateWith builds and executes a mutating request using the given *http.Client.
// This allows callers to choose between the default (30s timeout) and long-timeout client.
func (c *httpClient) execMutateWith(ctx context.Context, hc *http.Client, method, path string, body io.Reader, headers map[string]string, csrfToken string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.Host+path, body)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	req.Header.Set("X-CSRF-Token", csrfToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return hc.Do(req)
}

// htmlErrorTextHeaderRe matches the SAP "Application Server Error" page's
// header line, e.g. <p class="errorTextHeader">500 Internal Server Error</p>.
var htmlErrorTextHeaderRe = regexp.MustCompile(`<p class="errorTextHeader">([^<]*)</p>`)

// htmlDetailTextRe matches the SAP "Application Server Error" page's detail
// lines, e.g. <p class="detailText">Internal error code 8.</p>.
var htmlDetailTextRe = regexp.MustCompile(`<p class="detailText">([^<]*)</p>`)

// parseADTError reads an error response body and returns an *ADTError.
//
// The body is parsed in four layers:
//  1. As an <exc:exception> envelope. Two SAP XML namespaces share this root
//     local name: the modern schema
//     (http://www.sap.com/abapxml/types/communicationframework) and an older
//     schema (http://www.sap.com/adt/exceptions, used by some refactoring
//     endpoints). A single unmarshal claims both. Namespace and Type are only
//     populated when the XML namespace URI matches the modern schema — the
//     older schema does not carry those children, and a caller reading
//     adtErr.Type from an old-namespace body would be confused by an empty
//     value next to a non-empty Message. The old namespace yields Message-only.
//  2. As the legacy ADT framework <ExceptionText><message>…</message>
//     envelope — populates Message only.
//  3. As a SAP "Application Server Error" HTML page — see adtler#13 /
//     mcp-server-abap#292 for the regression this layer prevents (dumping
//     several KB of HTML, CSS, and base64-encoded PNG into the message).
//  4. As-is (trimmed) for anything else, preserving prior behaviour for
//     non-XML, non-HTML bodies.
//
// Layer 1 falls through to subsequent layers when <message> is empty or
// missing — even if <namespace> and <type> children were present. This is
// rare in practice (SAP always sends <message> for non-trivial errors) but
// means a hypothetical <exc:exception> body with structured identifiers but
// no message text would degrade to whatever later layers can extract.
func parseADTError(statusCode int, body io.Reader) error {
	data, _ := io.ReadAll(body)

	// Layer 1: <exc:exception> envelope. Both the modern schema
	// (http://www.sap.com/abapxml/types/communicationframework) and an
	// older SAP namespace (http://www.sap.com/adt/exceptions, used by
	// some refactoring endpoints) share the same root local name, so a
	// single unmarshal claims both. Namespace and Type are only
	// populated when the XML namespace URI matches the modern schema —
	// the older schema does not carry those children, and a caller
	// reading adtErr.Type from an old-namespace body would be confused
	// by an empty value next to a non-empty Message.
	const modernExcNS = "http://www.sap.com/abapxml/types/communicationframework"
	var excEnv struct {
		XMLName   xml.Name `xml:"exception"`
		Namespace struct {
			ID string `xml:"id,attr"`
		} `xml:"namespace"`
		Type struct {
			ID string `xml:"id,attr"`
		} `xml:"type"`
		Message string `xml:"message"`
	}
	if err := xml.Unmarshal(data, &excEnv); err == nil && excEnv.Message != "" {
		if excEnv.XMLName.Space == modernExcNS {
			return &ADTError{
				StatusCode: statusCode,
				Namespace:  excEnv.Namespace.ID,
				Type:       excEnv.Type.ID,
				Message:    excEnv.Message,
			}
		}
		// Old-namespace envelope: extract message only, leave Namespace/Type empty.
		return &ADTError{StatusCode: statusCode, Message: excEnv.Message}
	}

	// Layer 2: legacy <ExceptionText> envelope. Namespace/Type stay empty.
	var legacy struct {
		XMLName xml.Name `xml:"ExceptionText"`
		Message string   `xml:"message"`
	}
	if err := xml.Unmarshal(data, &legacy); err == nil && legacy.Message != "" {
		return &ADTError{StatusCode: statusCode, Message: legacy.Message}
	}

	// Layer 3: SAP HTML "Application Server Error" page.
	if msg := parseHTMLErrorBody(data); msg != "" {
		return &ADTError{StatusCode: statusCode, Message: msg}
	}

	// Layer 4: any other body, trimmed.
	return &ADTError{StatusCode: statusCode, Message: strings.TrimSpace(string(data))}
}

// parseHTMLErrorBody returns a short, human-readable summary of an SAP HTML
// error page, or "" if the body doesn't look like one. The expected page
// shape is the standard <p class="errorTextHeader"> + several
// <p class="detailText"> lines that the SAP HTTP server emits for generic
// 4xx/5xx failures.
func parseHTMLErrorBody(data []byte) string {
	s := strings.TrimSpace(string(data))
	if s == "" {
		return ""
	}
	head := s
	if len(head) > 64 {
		head = head[:64]
	}
	headLower := strings.ToLower(head)
	if !strings.HasPrefix(headLower, "<!doctype html") && !strings.HasPrefix(headLower, "<html") {
		return ""
	}

	var parts []string
	if m := htmlErrorTextHeaderRe.FindStringSubmatch(s); m != nil {
		if t := strings.TrimSpace(m[1]); t != "" {
			parts = append(parts, t)
		}
	}
	for _, m := range htmlDetailTextRe.FindAllStringSubmatch(s, -1) {
		if t := strings.TrimSpace(m[1]); t != "" {
			parts = append(parts, t)
		}
	}

	if len(parts) == 0 {
		// It's HTML, but not the expected SAP error layout. Don't dump
		// the whole page; tell the caller it was an HTML response so they
		// can investigate further.
		return "SAP returned an HTML error page (no parseable detail)"
	}
	return strings.Join(parts, " — ")
}

// encodeNamespacePath detects SAP namespace objects in ADT paths and
// percent-encodes the namespace slashes. When a user passes an object URI
// like /sap/bc/adt/programs/programs//HFQ/REPORT, the double slash indicates
// a namespace object. This function converts it to the ADT-required format:
// /sap/bc/adt/programs/programs/%2fhfq%2freport
func encodeNamespacePath(path string) string {
	idx := strings.Index(path, "//")
	if idx < 0 {
		return path
	}
	// Separate query string before processing
	query := ""
	if qIdx := strings.IndexByte(path, '?'); qIdx >= 0 {
		query = path[qIdx:]
		path = path[:qIdx]
	}
	prefix := path[:idx+1]
	rest := path[idx+1:]
	endNS := strings.Index(rest[1:], "/")
	if endNS < 0 {
		return path + query
	}
	nsName := rest[1 : endNS+1]
	after := rest[endNS+2:]
	objName := after
	suffix := ""
	if slashIdx := strings.Index(after, "/"); slashIdx >= 0 {
		objName = after[:slashIdx]
		suffix = after[slashIdx:]
	}
	return prefix + "%2f" + strings.ToLower(nsName) + "%2f" + strings.ToLower(objName) + suffix + query
}

// checkResponse returns an *ADTError if the response status indicates failure.
func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 400 {
		return parseADTError(resp.StatusCode, resp.Body)
	}
	return nil
}
