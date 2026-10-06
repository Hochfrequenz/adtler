package adt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Issue #186: RunUnitTests used to POST through the short (30-second) HTTP
// client. http.Client.Timeout and the context deadline combine as
// min(Timeout, ctx), so a caller passing timeoutSeconds > 25 was still cut off
// after 30 s — fatal for long-running test runs and for debugging a unit test,
// where the /abapunit/testruns request stays open while the debuggee is
// suspended at an external breakpoint (aibap.mcp#558).
//
// Same technique as the classrun tests (issue #114): shrink the short client's
// timeout to milliseconds and check whether the request survives a slower
// server. Must not call t.Parallel() (nothing in package adt does today).

const (
	unitTestRunsPath = "/sap/bc/adt/abapunit/testruns"
	// minimalRunResult is a parseable AUnit run result with one passing test.
	minimalRunResult = `<?xml version="1.0"?>` +
		`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">` +
		`<program adtcore:uri="/sap/bc/adt/programs/programs/ZSLOW" adtcore:name="ZSLOW"><testClasses>` +
		`<testClass adtcore:name="LCL_TEST"><testMethods>` +
		`<testMethod adtcore:name="TEST_SLOW" executionTime="45.0"><alerts/></testMethod>` +
		`</testMethods></testClass></testClasses></program></aunit:runResult>`
)

// slowUnitTestServer serves an instant CSRF preflight and answers the AUnit
// endpoint after delay. Teardown follows slowServer's stop-channel pattern so a
// handler sleeping past the test does not block srv.Close().
func slowUnitTestServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != unitTestRunsPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		case <-stop:
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(minimalRunResult))
	}))
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})
	return srv
}

// TestRunUnitTests_NotCappedByShortClient is the issue #186 regression guard:
// with the short client capped at 30 ms and the server answering after 250 ms,
// the pre-fix code (doMutate) fails with "Client.Timeout exceeded"; the fixed
// code (doMutateLong) returns the run result.
func TestRunUnitTests_NotCappedByShortClient(t *testing.T) {
	srv := slowUnitTestServer(t, slowResponse)
	c := shortCappedClient(t, srv.URL)

	result, err := c.RunUnitTests(context.Background(), "/sap/bc/adt/programs/programs/ZSLOW", 60)
	if err != nil {
		t.Fatalf("RunUnitTests was capped by the short HTTP client: %v", err)
	}
	if result.Passed != 1 {
		t.Errorf("Passed: got %d, want 1", result.Passed)
	}
}

// TestRunUnitTests_HonoursCallerDeadline guards the other direction: moving to
// the long client (no timeout of its own) must not remove the limit. A caller
// deadline shorter than the server's response time must abort the run promptly.
func TestRunUnitTests_HonoursCallerDeadline(t *testing.T) {
	srv := slowUnitTestServer(t, stalledResponse)
	c := shortCappedClient(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), tinyDeadline)
	defer cancel()

	start := time.Now()
	_, err := c.RunUnitTests(ctx, "/sap/bc/adt/programs/programs/ZSLOW", 60)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected the caller's %v deadline to abort RunUnitTests, got nil error", tinyDeadline)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error: got %v, want a context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("RunUnitTests took %v — the caller's deadline was not honoured promptly", elapsed)
	}
}
