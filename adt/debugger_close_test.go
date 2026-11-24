package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

const (
	closeTestCookieName = "SAP_SESSIONID_TST_100"
	closeTestProgramURI = "/sap/bc/adt/programs/programs/ztest/source/main"
)

// closeTestServer counts hits on the CSRF preflight and on the logoff
// endpoint. Every other path answers 200. The logoff answers logoffStatus. The
// preflight sets a session cookie; cookieOnPreflight turns true when a
// preflight arrives that still carries it (a jar that was not emptied).
func closeTestServer(t *testing.T, logoffStatus int, csrfHits, logoffHits *atomic.Int32, cookieOnPreflight *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			csrfHits.Add(1)
			if _, err := r.Cookie(closeTestCookieName); err == nil {
				cookieOnPreflight.Store(true)
			}
			http.SetCookie(w, &http.Cookie{Name: closeTestCookieName, Value: "v", Path: "/"})
			w.Header().Set("X-CSRF-Token", "token")
		case logoffPath:
			logoffHits.Add(1)
			w.WriteHeader(logoffStatus)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func closeTestConfig(srv *httptest.Server) sapmcpconfig.SAPSystem {
	return sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
}

func TestDebugSessionClose_ResetsSession(t *testing.T) {
	var csrfHits, logoffHits atomic.Int32
	var cookieOnPreflight atomic.Bool
	srv := closeTestServer(t, http.StatusOK, &csrfHits, &logoffHits, &cookieOnPreflight)
	dbg := adt.NewDebugSession(adt.NewClient(closeTestConfig(srv)), "U")
	ctx := context.Background()

	if err := dbg.StopListener(ctx); err != nil {
		t.Fatalf("StopListener: %v", err)
	}
	if got := csrfHits.Load(); got != 1 {
		t.Fatalf("CSRF hits before Close: got %d, want 1", got)
	}
	if err := dbg.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := logoffHits.Load(); got != 1 {
		t.Errorf("logoff hits: got %d, want 1", got)
	}
	if err := dbg.StopListener(ctx); err != nil {
		t.Fatalf("StopListener after Close: %v", err)
	}
	if got := csrfHits.Load(); got != 2 {
		t.Errorf("CSRF hits after Close and a second call: got %d, want 2 (new session)", got)
	}
}

func TestDebugSessionClose_ReportsUnauthorized(t *testing.T) {
	var csrfHits, logoffHits atomic.Int32
	var cookieOnPreflight atomic.Bool
	srv := closeTestServer(t, http.StatusUnauthorized, &csrfHits, &logoffHits, &cookieOnPreflight)
	dbg := adt.NewDebugSession(adt.NewClient(closeTestConfig(srv)), "U")

	err := dbg.Close(context.Background())
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("Close: got %v, want an *adt.ADTError", err)
	}
}

// A logoff that fails on the transport (here: the context is already
// cancelled) makes httpClient.Logout return before it drops its local state.
// Close must drop it anyway.
func TestDebugSessionClose_ResetsSessionWhenLogoffFails(t *testing.T) {
	var csrfHits, logoffHits atomic.Int32
	var cookieOnPreflight atomic.Bool
	srv := closeTestServer(t, http.StatusOK, &csrfHits, &logoffHits, &cookieOnPreflight)
	dbg := adt.NewDebugSession(adt.NewClient(closeTestConfig(srv)), "U")

	if err := dbg.StopListener(context.Background()); err != nil {
		t.Fatalf("StopListener: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := dbg.Close(cancelled); err == nil {
		t.Fatal("Close with a cancelled context: got nil, want an error")
	}
	if err := dbg.StopListener(context.Background()); err != nil {
		t.Fatalf("StopListener after Close: %v", err)
	}
	if got := csrfHits.Load(); got != 2 {
		t.Errorf("CSRF hits: got %d, want 2 (Close must drop the cached token)", got)
	}
	if cookieOnPreflight.Load() {
		t.Error("the new preflight carried the old session cookie (Close must empty the jar)")
	}
}

func TestDebugSessionClose_LeavesParentSessionAlone(t *testing.T) {
	// The cookie is set once, on the very first request, so a request that
	// carries it later can only have it from the jar of the client that sent it.
	var requests atomic.Int32
	var parentCookieSeen, afterClose atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.SetCookie(w, &http.Cookie{Name: closeTestCookieName, Value: "v", Path: "/"})
		} else if r.URL.Path == closeTestProgramURI && afterClose.Load() {
			if _, err := r.Cookie(closeTestCookieName); err == nil {
				parentCookieSeen.Store(true)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	parent := adt.NewClient(closeTestConfig(srv))
	dbg := adt.NewDebugSession(parent, "U")
	ctx := context.Background()

	if _, err := parent.GetSource(ctx, closeTestProgramURI); err != nil {
		t.Fatalf("parent GetSource: %v", err)
	}
	if err := dbg.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	afterClose.Store(true)
	if _, err := parent.GetSource(ctx, closeTestProgramURI); err != nil {
		t.Fatalf("parent GetSource after Close: %v", err)
	}
	if !parentCookieSeen.Load() {
		t.Error("parent request after Close carried no session cookie: Close reached the parent's jar")
	}
}
