package adt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// stepEndpointPath mirrors the debugger dispatch endpoint used by Step,
// GetStack, GetVariable, Attach etc. Defined locally (internal test, package
// adt) since the external test package's own constant isn't visible here.
const stepEndpointPath = "/sap/bc/adt/debugger"

// TestStep_TimeoutWithNoRemainingSessions_ReturnsDebuggeeEndedError guards
// the fix for aibap.mcp#513: SAP's ADT debugger kernel call never returns an
// HTTP response when a step action (most commonly stepContinue) runs the
// debuggee past its last statement — the ABAP side genuinely finishes
// (confirmed live against a real SAP system, see aibap.mcp#513), but the
// HTTP request hangs until the client's own timeout fires. Treating that
// bare timeout as an opaque error forces every caller to separately guess
// "did it actually work?" This test verifies Step recognizes the pattern
// (timeout + confirmed-empty debuggee session list) and returns a typed
// *DebuggeeEndedError instead of a bare timeout error.
func TestStep_TimeoutWithNoRemainingSessions_ReturnsDebuggeeEndedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == "stepContinue" {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == "getDebuggeeSessions" {
			w.WriteHeader(http.StatusOK) // empty body — no sessions
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	c, ok := NewClient(cfg).(*httpClient)
	if !ok {
		t.Fatalf("NewClient did not return *httpClient")
	}
	c.http.Timeout = 30 * time.Millisecond // shrink so the 200ms server delay times out fast

	dbg := NewDebugSession(c, "U")

	_, err := dbg.Step(context.Background(), "stepContinue")
	if err == nil {
		t.Fatal("expected an error (timeout), got nil")
	}
	var endedErr *DebuggeeEndedError
	if !errors.As(err, &endedErr) {
		t.Fatalf("expected *DebuggeeEndedError, got %T: %v", err, err)
	}
	if endedErr.Action != "stepContinue" {
		t.Errorf("Action: got %q, want stepContinue", endedErr.Action)
	}
}

// TestStep_TimeoutWithRemainingSessions_ReturnsPlainError guards the other
// direction: if the debuggee session is still alive after a Step timeout
// (e.g. a genuinely stuck work process, not a finished debuggee), Step must
// NOT misreport it as ended — that would hide a real hang as a false
// success.
func TestStep_TimeoutWithRemainingSessions_ReturnsPlainError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == "stepContinue" {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == "getDebuggeeSessions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<sessions><session id="still-alive"/></sessions>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	c, ok := NewClient(cfg).(*httpClient)
	if !ok {
		t.Fatalf("NewClient did not return *httpClient")
	}
	c.http.Timeout = 30 * time.Millisecond

	dbg := NewDebugSession(c, "U")

	_, err := dbg.Step(context.Background(), "stepContinue")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var endedErr *DebuggeeEndedError
	if errors.As(err, &endedErr) {
		t.Fatalf("wrongly reported DebuggeeEndedError while a session was still alive: %v", endedErr)
	}
}

// TestStep_NonTimeoutError_ReturnsPlainError guards against over-eager
// detection: a genuine non-timeout failure (e.g. a 500 from SAP) must not be
// reinterpreted as a debuggee-ended condition just because it's an error.
func TestStep_NonTimeoutError_ReturnsPlainError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == "stepContinue" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="AdiFailed"/><message>boom</message></exc:exception>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := NewDebugSession(NewClient(cfg), "U")

	_, err := dbg.Step(context.Background(), "stepContinue")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var endedErr *DebuggeeEndedError
	if errors.As(err, &endedErr) {
		t.Fatalf("wrongly reported DebuggeeEndedError for a real 500 error: %v", endedErr)
	}
}
