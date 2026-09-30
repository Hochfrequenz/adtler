package adt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// attachMethod is the ADT debugger dispatch method DebugSession.Attach uses.
const attachMethod = "attach"

// writeAdiFailed writes a fast (non-timeout) 500 AdiFailed response, the
// known-intermittent SAP kernel-side debugger fault documented on
// aibap.mcp#513.
func writeAdiFailed(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="AdiFailed"/><message>boom</message></exc:exception>`))
}

// TestAttach_AdiFailedThenSuccess_Retries guards the aibap.mcp#513 mitigation:
// a fast-failing 500 AdiFailed response from Attach is transparently retried
// (bounded, fixed delay) rather than surfaced on the first failure, since the
// issue's live investigation found this specific failure shape intermittent
// — retrying the same debuggeeId sometimes succeeds.
func TestAttach_AdiFailedThenSuccess_Retries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == attachMethod {
			n := calls.Add(1)
			if n < 3 {
				writeAdiFailed(w)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := NewDebugSession(NewClient(cfg), "U")

	if err := dbg.Attach(context.Background(), "debuggee-1"); err != nil {
		t.Fatalf("Attach: unexpected error after retries: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("attach requests: got %d, want 3", got)
	}
}

// TestAttach_AdiFailedExhausted_ReturnsError guards the retry ceiling: after
// adiFailedMaxRetries retries all still fail, Attach gives up and returns an
// error that still lets callers recover the underlying *ADTError.
func TestAttach_AdiFailedExhausted_ReturnsError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == attachMethod {
			calls.Add(1)
			writeAdiFailed(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := NewDebugSession(NewClient(cfg), "U")

	err := dbg.Attach(context.Background(), "debuggee-1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := calls.Load(); got != int32(1+adiFailedMaxRetries) {
		t.Errorf("attach requests: got %d, want %d", got, 1+adiFailedMaxRetries)
	}
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected error to wrap *ADTError, got %T: %v", err, err)
	}
	if adtErr.Type != ExceptionTypeAdiFailed {
		t.Errorf("wrapped ADTError.Type: got %q, want %q", adtErr.Type, ExceptionTypeAdiFailed)
	}
	if !strings.Contains(err.Error(), "retries") {
		t.Errorf("error message should mention retries, got: %v", err)
	}
}

// TestStep_AdiFailedThenSuccess_Retries mirrors the Attach retry test for
// Step's non-timeout 500 AdiFailed path (the rarer of the two failure sites
// documented on aibap.mcp#513, e.g. a stepInto failing right after a
// successful attach).
func TestStep_AdiFailedThenSuccess_Retries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == "stepInto" {
			n := calls.Add(1)
			if n < 2 {
				writeAdiFailed(w)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<dbg:step xmlns:dbg="http://www.sap.com/adt/debugger"/>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := NewDebugSession(NewClient(cfg), "U")

	data, err := dbg.Step(context.Background(), "stepInto")
	if err != nil {
		t.Fatalf("Step: unexpected error after retry: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty step response body")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("step requests: got %d, want 2", got)
	}
}

// TestStep_DebuggeeEndedError_NeverRetried guards the decoupling from
// adtler#159 (SAP's native fast AdiFailed debuggee-ended signal): whatever
// that future fix ends up wrapping in *DebuggeeEndedError, retryOnAdiFailed
// must treat it as terminal, not retryable — a debuggee-ended outcome is a
// success to report once, not a transient fault to retry. Simulated here via
// the existing timeout-based DebuggeeEndedError path, since #159's own
// subtype-based path isn't implemented yet.
func TestStep_DebuggeeEndedError_NeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == stepContinueMethod {
			calls.Add(1)
			<-r.Context().Done() // never respond — forces the client timeout
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == getDebuggeeSessionsMethod {
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
	c.http.Timeout = 30 * time.Millisecond

	dbg := NewDebugSession(c, "U")

	_, err := dbg.Step(context.Background(), stepContinueMethod)
	var ended *DebuggeeEndedError
	if !errors.As(err, &ended) {
		t.Fatalf("expected *DebuggeeEndedError, got %T: %v", err, err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("stepContinue requests: got %d, want 1 (must not be retried)", got)
	}
}

// TestAttach_DebuggeeEndedAdiFailed_NeverRetried guards the Attach side of
// adtler#159: attaching to a debuggee that already ran to completion hits the
// same fast AdiFailed/CX_TPDAPI_DEBUGGEE_ENDED signature as Step, but Attach
// has no DebuggeeEndedError-shaped success to return for it — it's still a
// real failure (nothing to attach to), just one retrying can never fix, so it
// must not be retried either.
func TestAttach_DebuggeeEndedAdiFailed_NeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == attachMethod {
			calls.Add(1)
			writeDebuggeeEndedAdiFailed(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := NewDebugSession(NewClient(cfg), "U")

	err := dbg.Attach(context.Background(), "debuggee-1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ended *DebuggeeEndedError
	if errors.As(err, &ended) {
		t.Fatalf("Attach has no DebuggeeEndedError-shaped success — got one anyway: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attach requests: got %d, want 1 (must not be retried)", got)
	}
}

// writeDebuggeeEndedAdiFailed writes SAP's fast-path 500 AdiFailed response
// for a debuggee that already ran to completion — the exact shape captured
// live on aibap.mcp#513/adtler#159 (2026-09-30, ECC): a stepContinue run off
// the program's last statement, with the wrapped CX_TPDAPI_DEBUGGEE_ENDED
// exception surfaced via Properties["previous1ExceptionClassName"].
func writeDebuggeeEndedAdiFailed(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
		`<namespace id="com.sap.adt"/><type id="AdiFailed"/>` +
		`<message>Debuggee-Session wurde angehalten</message>` +
		`<properties>` +
		`<entry key="previous1ExceptionClassName">CX_TPDAPI_DEBUGGEE_ENDED</entry>` +
		`<entry key="previous1Text">Debuggee-Session wurde angehalten</entry>` +
		`</properties>` +
		`</exc:exception>`))
}

// TestStep_FastAdiFailedDebuggeeEnded_ReturnsDebuggeeEndedError_NeverRetried
// guards adtler#159 with the real live signature: a fast (non-timeout) 500
// AdiFailed whose wrapped exception is CX_TPDAPI_DEBUGGEE_ENDED must be
// classified as *DebuggeeEndedError (a success), on the first attempt, never
// retried — retrying a condition that can never succeed only wastes time
// (confirmed live: ~7-17s across 3 attempts before this fix).
func TestStep_FastAdiFailedDebuggeeEnded_ReturnsDebuggeeEndedError_NeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == stepEndpointPath && r.URL.Query().Get("method") == stepContinueMethod {
			calls.Add(1)
			writeDebuggeeEndedAdiFailed(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := NewDebugSession(NewClient(cfg), "U")

	_, err := dbg.Step(context.Background(), stepContinueMethod)
	var ended *DebuggeeEndedError
	if !errors.As(err, &ended) {
		t.Fatalf("expected *DebuggeeEndedError, got %T: %v", err, err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("stepContinue requests: got %d, want 1 (must not be retried)", got)
	}
}
