package adt_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// unauthorizedBody is a stand-in for the HTML page SAP sends with a 401. It
// is well-formed XML on purpose: Go's XML decoder then parses it without an
// error, which is what let a poll loop read it as "still running" (#222).
const unauthorizedBody = `<html><body><p>Logon failed</p></body></html>`

const (
	unauthorizedTask    = "AAAK900002"
	unauthorizedRequest = "AAAK900001"
	unauthorizedPollURI = "/sap/bc/adt/cts/transportrequests/" + unauthorizedRequest + "/bgrun"
)

// unauthorizedServer counts every request, CSRF preflights included. Until
// reject is set it answers like a working SAP system; from then on it answers
// everything with 401, as SAP does once a password is wrong or the user is
// locked. The count is the whole point: under Basic auth SAP counts every one
// of these requests as a failed logon, and a few in a row lock the user.
type unauthorizedServer struct {
	*httptest.Server
	requests atomic.Int32
	reject   atomic.Bool
}

func newUnauthorizedServer(t *testing.T, rejectFromStart bool) *unauthorizedServer {
	t.Helper()
	s := &unauthorizedServer{}
	s.reject.Store(rejectFromStart)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if s.reject.Load() {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(unauthorizedBody))
			return
		}
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

// batchCheckObjects is how many objects the BatchSyntaxCheck case checks:
// enough for three chunks of ten, so a loop that carried on after the first
// chunk's 401 would send two more requests.
const batchCheckObjects = 25

// resultText is the error of a path whose result carries the 401 only as
// text: BatchSyntaxCheck reports per-object error strings, not error values.
type resultText string

func (e resultText) Error() string { return string(e) }

// unauthorizedOperations are the request paths that must stop after the
// first 401 under Basic auth. Each names the code path it covers.
var unauthorizedOperations = []struct {
	name string
	call func(ctx context.Context, c adt.Client) error
}{
	{"read", func(ctx context.Context, c adt.Client) error {
		_, err := c.GetSource(ctx, probeObjectURI)
		return err
	}},
	{"mutate", func(ctx context.Context, c adt.Client) error {
		return c.UnlockObject(ctx, probeObjectURI, "handle")
	}},
	{"read on the long-timeout client", func(ctx context.Context, c adt.Client) error {
		_, err := c.ExportPackage(ctx, "ZPACKAGE")
		return err
	}},
	{"mutate on the long-timeout client", func(ctx context.Context, c adt.Client) error {
		_, err := c.RunQuery(ctx, "SELECT * FROM T000", 1)
		return err
	}},
	{"fresh session (RunClass)", func(ctx context.Context, c adt.Client) error {
		_, err := c.RunClass(ctx, "ZCL_TEST")
		return err
	}},
	{"fresh session (DebugSession)", func(ctx context.Context, c adt.Client) error {
		_, err := adt.NewDebugSession(c, "USERA").SetBreakpoint(ctx, probeObjectURI+"/source/main", 2, progType, "ZTEST")
		return err
	}},
	{"delete releasing a lock first", func(ctx context.Context, c adt.Client) error {
		return c.DeleteObject(ctx, probeObjectURI, "handle", "")
	}},
	{"ETag resolution with fallback", func(ctx context.Context, c adt.Client) error {
		_, err := adt.NewLockMap().ResolveETag(ctx, c, "key", probeObjectURI)
		return err
	}},
	{"remove from transport with capability check", func(ctx context.Context, c adt.Client) error {
		return c.RemoveFromTransport(ctx, unauthorizedTask, unauthorizedRequest, "R3TR", "PROG", "ZTEST", "", "")
	}},
	{"release transport with fallback endpoint", func(ctx context.Context, c adt.Client) error {
		_, err := c.ReleaseTransport(ctx, unauthorizedRequest)
		return err
	}},
	{"syntax check", func(ctx context.Context, c adt.Client) error {
		_, err := c.SyntaxCheck(ctx, probeObjectURI)
		return err
	}},
	{"batch syntax check over several chunks", func(ctx context.Context, c adt.Client) error {
		uris := make([]string, batchCheckObjects)
		for i := range uris {
			uris[i] = fmt.Sprintf("%s%d", probeObjectURI, i)
		}
		results := c.BatchSyntaxCheck(ctx, uris)
		for i, r := range results {
			if r.Error == "" {
				return fmt.Errorf("result %d carries no error", i)
			}
		}
		// The last object is in the last chunk, which must not have been sent.
		return resultText(results[len(results)-1].Error)
	}},
	{"DDIC dependency chain", func(ctx context.Context, c adt.Client) error {
		_, err := c.GetObjectDependencies(ctx, "TABL", "ZTABLE", 0, 2)
		return err
	}},
}

// assertUnauthorized fails unless err carries the 401 as an *adt.ADTError,
// the same shape a 401 on any other request produces.
func assertUnauthorized(t *testing.T, err error) {
	t.Helper()
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("error = %v (%T), want one wrapping *adt.ADTError", err, err)
	}
	if adtErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", adtErr.StatusCode)
	}
}

// TestBasicAuth_401BeforeAnySession_SendsOneRequest: a client whose
// credential SAP rejects from the start. The CSRF preflight is the first
// request, and its 401 must end the call (#222). Before the fix the preflight
// status was ignored, and one call sent the credential four times.
func TestBasicAuth_401BeforeAnySession_SendsOneRequest(t *testing.T) {
	for _, op := range unauthorizedOperations {
		t.Run(op.name, func(t *testing.T) {
			srv := newUnauthorizedServer(t, true)
			client := adt.NewClient(newTestConfig(srv.URL))

			err := op.call(context.Background(), client)

			assertOperationUnauthorized(t, err)
			if got := srv.requests.Load(); got != 1 {
				t.Errorf("server received %d request(s), want 1", got)
			}
		})
	}
}

// assertOperationUnauthorized is assertUnauthorized, except for a path whose
// result carries the 401 only as text (see resultText).
func assertOperationUnauthorized(t *testing.T, err error) {
	t.Helper()
	var text resultText
	if !errors.As(err, &text) {
		assertUnauthorized(t, err)
		return
	}
	if !strings.Contains(string(text), "401") {
		t.Fatalf("error = %v, want one naming the 401", err)
	}
}

// TestBasicAuth_401InEstablishedSession_SendsOneRequest: the client already
// holds a CSRF token when SAP starts rejecting the credential, for instance
// because the password was changed or the user got locked. The request's 401
// must be returned as it is, without a second preflight and a retry (#222).
func TestBasicAuth_401InEstablishedSession_SendsOneRequest(t *testing.T) {
	for _, op := range unauthorizedOperations {
		t.Run(op.name, func(t *testing.T) {
			srv := newUnauthorizedServer(t, false)
			client := adt.NewClient(newTestConfig(srv.URL))
			if _, err := client.SystemFlavor(context.Background()); err != nil {
				t.Fatalf("establishing the session: %v", err)
			}
			srv.reject.Store(true)
			srv.requests.Store(0)

			err := op.call(context.Background(), client)

			assertOperationUnauthorized(t, err)
			if got := srv.requests.Load(); got != 1 {
				t.Errorf("server received %d request(s), want 1", got)
			}
		})
	}
}

// TestBasicAuth_Logout401_SendsOneRequest: Logout sends one request and never
// repeats it, whether or not a session exists. It reports the 401, so that a
// caller such as CreateObject can stop instead of sending the rejected
// credential again.
func TestBasicAuth_Logout401_SendsOneRequest(t *testing.T) {
	for _, established := range []bool{false, true} {
		name := "before any session"
		if established {
			name = "in an established session"
		}
		t.Run(name, func(t *testing.T) {
			srv := newUnauthorizedServer(t, !established)
			client := adt.NewClient(newTestConfig(srv.URL))
			if established {
				if _, err := client.SystemFlavor(context.Background()); err != nil {
					t.Fatalf("establishing the session: %v", err)
				}
				srv.reject.Store(true)
				srv.requests.Store(0)
			}

			err := client.Logout(context.Background())

			assertUnauthorized(t, err)
			if got := srv.requests.Load(); got != 1 {
				t.Errorf("server received %d request(s), want 1", got)
			}
		})
	}
}

// TestLogout_OtherErrorStatusIsIgnored: only a 401 is reported. Any other
// status of the logoff request is ignored as before #222: the local session
// state is dropped either way.
func TestLogout_OtherErrorStatusIsIgnored(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	client := adt.NewClient(newTestConfig(srv.URL))

	if err := client.Logout(context.Background()); err != nil {
		t.Errorf("Logout after a 500: %v, want nil", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("server received %d request(s), want 1", got)
	}
}

// TestBasicAuth_CreateObjectLogout401_ReportsCreatedObject: CreateObject logs
// out after a successful create (adtler#4). A 401 on that logout must end the
// call with an error, so the caller sends nothing more, and the error must say
// that the object exists, so the caller does not treat it as never created.
func TestBasicAuth_CreateObjectLogout401_ReportsCreatedObject(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case programsEndpoint:
			w.WriteHeader(http.StatusCreated)
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(unauthorizedBody))
		}
	}))
	t.Cleanup(srv.Close)
	client := adt.NewClient(newTestConfig(srv.URL))

	err := client.CreateObject(context.Background(), "PROG", "ZTEST", "$TMP", "test", "")

	assertUnauthorized(t, err)
	if !errors.Is(err, adt.ErrObjectCreated) {
		t.Errorf("error = %v, want one matching adt.ErrObjectCreated", err)
	}
	if got := requests.Load(); got != 3 {
		t.Errorf("server received %d request(s), want 3 (preflight, create, logout)", got)
	}
}

// TestBasicAuth_401OnReleaseResult_StopsBeforeVerification: a background
// release that finished is read once more through its result link. A 401 there
// must end the call. Before the fix the HTML page passed as an empty release
// report, and the verification read that follows sent the rejected credential
// again.
func TestBasicAuth_401OnReleaseResult_StopsBeforeVerification(t *testing.T) {
	const resultPath = unauthorizedPollURI + "/result"
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case unauthorizedPollURI:
			_, _ = w.Write([]byte(`<?xml version="1.0"?><run><status>finished</status><link rel="result" href="` + resultPath + `"/></run>`))
		case resultPath:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(unauthorizedBody))
		default:
			_, _ = w.Write([]byte(`<?xml version="1.0"?><run><status>running</status><link rel="self" href="` + unauthorizedPollURI + `"/></run>`))
		}
	}))
	t.Cleanup(srv.Close)
	client := adt.NewClientWithPollInterval(newTestConfig(srv.URL), time.Millisecond)

	_, err := client.ReleaseTransport(context.Background(), unauthorizedRequest)

	assertUnauthorized(t, err)
	if got := requests.Load(); got != 4 {
		t.Errorf("server received %d request(s), want 4 (preflight, release request, poll, result)", got)
	}
}

// TestBasicAuth_401WhilePollingRelease_StopsPolling: a release that SAP runs
// in the background is polled until it finishes. A poll answered with 401
// must end the loop. Before the fix the HTML body parsed as a status-less
// document, read as "still running", and the loop polled on, each poll one
// more failed logon.
func TestBasicAuth_401WhilePollingRelease_StopsPolling(t *testing.T) {
	var requests, polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case unauthorizedPollURI:
			polls.Add(1)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(unauthorizedBody))
		default:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><run><status>running</status><link rel="self" href="` + unauthorizedPollURI + `"/></run>`))
		}
	}))
	t.Cleanup(srv.Close)
	client := adt.NewClientWithPollInterval(newTestConfig(srv.URL), time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.ReleaseTransport(ctx, unauthorizedRequest)

	assertUnauthorized(t, err)
	if got := polls.Load(); got != 1 {
		t.Errorf("server received %d poll(s), want 1", got)
	}
	if got := requests.Load(); got != 3 {
		t.Errorf("server received %d request(s), want 3 (preflight, release request, one poll)", got)
	}
}

// TestReleasePoll_5xxKeepsPolling: only a 4xx ends the poll loop. A 5xx may be
// transient while the release itself still finishes, so the loop polls on, as
// it did before #222.
func TestReleasePoll_5xxKeepsPolling(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case unauthorizedPollURI:
			if polls.Add(1) == 1 {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`<html><body><p>Internal Server Error</p></body></html>`))
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><run><status>finished</status></run>`))
		default:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><run><status>running</status><link rel="self" href="` + unauthorizedPollURI + `"/></run>`))
		}
	}))
	t.Cleanup(srv.Close)
	client := adt.NewClientWithPollInterval(newTestConfig(srv.URL), time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.ReleaseTransport(ctx, unauthorizedRequest); err != nil {
		t.Fatalf("ReleaseTransport after a transient 500 while polling: %v", err)
	}
	if got := polls.Load(); got != 2 {
		t.Errorf("server received %d poll(s), want 2", got)
	}
}

// TestOAuth2_WithoutRefreshCallback_401SendsOneRequest: a token client
// without a refresh callback cannot change its token, so a 401 is final there
// too: one request, no retry.
func TestOAuth2_WithoutRefreshCallback_401SendsOneRequest(t *testing.T) {
	for _, established := range []bool{false, true} {
		name := "before any session"
		if established {
			name = "in an established session"
		}
		t.Run(name, func(t *testing.T) {
			srv := newUnauthorizedServer(t, !established)
			client := adt.NewClientWithToken(sapmcpconfig.SAPSystem{Host: srv.URL, Client: "100"}, "token", nil)
			if established {
				if _, err := client.SystemFlavor(context.Background()); err != nil {
					t.Fatalf("establishing the session: %v", err)
				}
				srv.reject.Store(true)
				srv.requests.Store(0)
			}

			_, err := client.GetSource(context.Background(), probeObjectURI)

			assertUnauthorized(t, err)
			if got := srv.requests.Load(); got != 1 {
				t.Errorf("server received %d request(s), want 1", got)
			}
		})
	}
}

// TestBasicAuth_403CSRFRetryKeepsWorking: the 401 change must leave the
// CSRF retry alone. A 403 on a mutating request still fetches a new token
// and repeats the request once: preflight, request, preflight, retry.
func TestBasicAuth_403CSRFRetryKeepsWorking(t *testing.T) {
	var requests, mutations atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if mutations.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	client := adt.NewClient(newTestConfig(srv.URL))

	if err := client.UnlockObject(context.Background(), probeObjectURI, "handle"); err != nil {
		t.Fatalf("UnlockObject after a 403: %v", err)
	}
	if got := requests.Load(); got != 4 {
		t.Errorf("server received %d request(s), want 4 (preflight, request, preflight, retry)", got)
	}
}

// oauthServer rejects the token "expired" with 401 on every path, CSRF
// preflight included, and accepts any other bearer token. It counts every
// request.
func oauthServer(t *testing.T, rejectAll bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if rejectAll || r.Header.Get("Authorization") == "Bearer expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// TestOAuth2_401OnPreflight_RefreshesOnceAndRetries: now that the preflight
// reports a 401, an OAuth client must still refresh an expired token there
// and carry on, as it does for a 401 on the request itself.
func TestOAuth2_401OnPreflight_RefreshesOnceAndRetries(t *testing.T) {
	srv, requests := oauthServer(t, false)
	var refreshes atomic.Int32
	client := adt.NewClientWithToken(sapmcpconfig.SAPSystem{Host: srv.URL, Client: "100"}, "expired",
		func(string) (string, error) {
			refreshes.Add(1)
			return "fresh", nil
		})

	for _, op := range []struct {
		name string
		call func() error
	}{
		{"read", func() error { _, err := client.GetSource(context.Background(), probeObjectURI); return err }},
		{"mutate", func() error { return client.UnlockObject(context.Background(), probeObjectURI, "handle") }},
	} {
		if err := op.call(); err != nil {
			t.Fatalf("%s after a refresh: %v", op.name, err)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("token refreshed %d times, want 1", got)
	}
	// Preflight with the expired token, preflight with the fresh one, then
	// the read and the mutating request.
	if got := requests.Load(); got != 4 {
		t.Errorf("server received %d request(s), want 4", got)
	}
}

// TestOAuth2_401AfterRefresh_RefreshesOnlyOnce: a refreshed token that SAP
// rejects as well ends the call with the 401. There is one refresh per call,
// not a loop.
func TestOAuth2_401AfterRefresh_RefreshesOnlyOnce(t *testing.T) {
	srv, requests := oauthServer(t, true)
	var refreshes atomic.Int32
	client := adt.NewClientWithToken(sapmcpconfig.SAPSystem{Host: srv.URL, Client: "100"}, "expired",
		func(string) (string, error) {
			refreshes.Add(1)
			return "fresh", nil
		})

	_, err := client.GetSource(context.Background(), probeObjectURI)

	assertUnauthorized(t, err)
	if got := refreshes.Load(); got != 1 {
		t.Errorf("token refreshed %d times, want 1", got)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("server received %d request(s), want 2 (preflight before and after the refresh)", got)
	}
}
