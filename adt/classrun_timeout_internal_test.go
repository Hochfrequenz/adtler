package adt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Issue #114: RunClass used to POST through the short (30-second) HTTP client,
// so any classrun running longer than 30 s failed with "Client.Timeout exceeded
// while awaiting headers" even though the ABAP work process was still running.
// Consumers could not work around it: http.Client.Timeout and the context
// deadline combine as min(Timeout, ctx), and the client fields are unexported.
//
// The tests below are internal (package adt) on purpose: the regression is about
// WHICH *http.Client carries the request, and the only way to observe that
// without sleeping past 30 s of real time is to shrink the short client's
// timeout to milliseconds and check whether the request survives. A request that
// survives a 30-ms short-client cap cannot be running on the short client.
const (
	// discoveryPath is the CSRF preflight endpoint (fast in these tests).
	discoveryPath = "/sap/bc/adt/discovery"
	// dataPreviewPath is the RunQuery endpoint.
	dataPreviewPath = "/sap/bc/adt/datapreview/freestyle"
	// tinyShortTimeout stands in for the production 30 s short-client cap.
	tinyShortTimeout = 30 * time.Millisecond
	// slowResponse is comfortably longer than tinyShortTimeout but short enough
	// to keep the unit suite fast. It stands in for a 45 s classrun.
	slowResponse = 250 * time.Millisecond
	// emptyTableData is a minimal, parseable data preview response for RunQuery.
	emptyTableData = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<tableData><totalRows>0</totalRows><queryExecutionTime>1.0</queryExecutionTime></tableData>`
)

// slowServer serves an instant CSRF preflight and delays every other response by
// delay. The classrun endpoint answers text/plain, the data preview endpoint
// answers a minimal tableData document, so the same server drives both RunClass
// and RunQuery.
func slowServer(delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		if strings.HasPrefix(r.URL.Path, dataPreviewPath) {
			w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
			_, _ = w.Write([]byte(emptyTableData))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("elapsed 45.00 s\n"))
	}))
}

// shortCappedClient returns a client whose SHORT http client is capped at
// tinyShortTimeout while the long client keeps its production configuration
// (no timeout of its own, deadline supplied by the context). Any request that
// completes on such a client took the long path.
func shortCappedClient(t *testing.T, host string) *httpClient {
	t.Helper()
	c, ok := NewClient(sapmcpconfig.SAPSystem{Host: host, User: "U", Password: "P", Client: "100"}).(*httpClient)
	if !ok {
		t.Fatalf("NewClient did not return *httpClient")
	}
	c.http.Timeout = tinyShortTimeout
	return c
}

// TestRunClass_NotCappedByShortClient is the issue #114 regression guard: the
// classrun POST must not run on the short HTTP client. With the short client
// capped at 30 ms and the server answering after 250 ms, the pre-fix code
// (doMutate) fails with "Client.Timeout exceeded"; the fixed code (doMutateLong)
// returns the console output.
func TestRunClass_NotCappedByShortClient(t *testing.T) {
	srv := slowServer(slowResponse)
	defer srv.Close()
	c := shortCappedClient(t, srv.URL)

	result, err := c.RunClass(context.Background(), "ZCL_SLOW")
	if err != nil {
		t.Fatalf("RunClass was capped by the short HTTP client: %v", err)
	}
	if result.ConsoleOutput != "elapsed 45.00 s\n" {
		t.Errorf("ConsoleOutput: got %q, want %q", result.ConsoleOutput, "elapsed 45.00 s\n")
	}
}

// TestRunClass_HonoursCallerDeadline guards the other direction: switching to
// the unlimited client must not remove the limit altogether. A caller-supplied
// deadline shorter than the server's response time must abort the run, and the
// caller's deadline must win over defaultLongRunTimeout.
func TestRunClass_HonoursCallerDeadline(t *testing.T) {
	srv := slowServer(3 * time.Second)
	defer srv.Close()
	c := shortCappedClient(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.RunClass(ctx, "ZCL_SLOW")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the caller's 100 ms deadline to abort RunClass, got nil error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error: got %v, want a context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("RunClass took %v — the caller's deadline was not honoured promptly", elapsed)
	}
}

// TestRunClass_DefaultDeadlineApplies verifies that a context WITHOUT a deadline
// still gets one. The long HTTP client has no timeout of its own, so without the
// default a runaway classrun would hang the caller forever.
func TestRunClass_DefaultDeadlineApplies(t *testing.T) {
	srv := slowServer(10 * time.Second)
	defer srv.Close()
	c := shortCappedClient(t, srv.URL)

	restore := defaultLongRunTimeout
	defaultLongRunTimeout = 150 * time.Millisecond
	defer func() { defaultLongRunTimeout = restore }()

	start := time.Now()
	_, err := c.RunClass(context.Background(), "ZCL_SLOW") // no deadline
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the default deadline to abort RunClass, got nil error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error: got %v, want a context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("RunClass ran for %v — the default deadline was not applied", elapsed)
	}
}

// TestWithDefaultDeadline covers the helper directly: a caller deadline is left
// untouched, an unbounded context receives defaultLongRunTimeout.
func TestWithDefaultDeadline(t *testing.T) {
	restore := defaultLongRunTimeout
	defaultLongRunTimeout = time.Hour
	defer func() { defaultLongRunTimeout = restore }()

	callerDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	got, cancel2 := withDefaultDeadline(ctx)
	defer cancel2()
	deadline, ok := got.Deadline()
	if !ok || !deadline.Equal(callerDeadline) {
		t.Errorf("caller deadline: got (%v, %v), want %v", deadline, ok, callerDeadline)
	}

	got, cancel3 := withDefaultDeadline(context.Background())
	defer cancel3()
	deadline, ok = got.Deadline()
	if !ok {
		t.Fatal("unbounded context got no deadline")
	}
	if remaining := time.Until(deadline); remaining < 55*time.Minute {
		t.Errorf("default deadline: %v remaining, want ~1h (defaultLongRunTimeout)", remaining)
	}
}

// TestFreshSession_PreservesLongClient pins the assumption RunClass relies on:
// the single-use fresh session must carry the same long-client configuration as
// its parent, or RunClass would silently fall back to a capped client via the
// fresh-session path. The fresh session's cookie jar must still be its own —
// that isolation is what the issue #106 fix depends on.
func TestFreshSession_PreservesLongClient(t *testing.T) {
	parent, ok := NewClient(sapmcpconfig.SAPSystem{Host: "https://example.invalid", Client: "100"}).(*httpClient)
	if !ok {
		t.Fatalf("NewClient did not return *httpClient")
	}
	fresh := parent.freshSession()

	if fresh.httpLong == nil {
		t.Fatal("fresh session has no long HTTP client")
	}
	if fresh.httpLong.Timeout != parent.httpLong.Timeout {
		t.Errorf("fresh httpLong.Timeout: got %v, want %v (parent's)",
			fresh.httpLong.Timeout, parent.httpLong.Timeout)
	}
	if fresh.httpLong.Timeout != 0 {
		t.Errorf("fresh httpLong.Timeout: got %v, want 0 (deadline comes from the context)",
			fresh.httpLong.Timeout)
	}
	if fresh.httpLong.Transport != parent.httpLong.Transport {
		t.Error("fresh httpLong does not reuse the parent's transport")
	}
	if fresh.httpLong.Jar == nil {
		t.Fatal("fresh httpLong has no cookie jar")
	}
	if fresh.httpLong.Jar == parent.httpLong.Jar {
		t.Error("fresh httpLong reuses the parent's cookie jar — session isolation lost (#106)")
	}
	if fresh.httpLong.Jar != fresh.http.Jar {
		t.Error("fresh session's short and long clients use different cookie jars")
	}
}

// TestRunQueryAndRunClass_ShareLongPath is the drift guard required by issue
// #114: both endpoints execute open-ended ABAP, so both must route through the
// long HTTP client AND share one default deadline. The two assertions below fail
// if either endpoint is changed in isolation.
func TestRunQueryAndRunClass_ShareLongPath(t *testing.T) {
	run := func(t *testing.T, c *httpClient) (classErr, queryErr error) {
		t.Helper()
		_, classErr = c.RunClass(context.Background(), "ZCL_SLOW")
		_, queryErr = c.RunQuery(context.Background(), "SELECT * FROM T000", 1)
		return classErr, queryErr
	}

	t.Run("both bypass the short client cap", func(t *testing.T) {
		srv := slowServer(slowResponse)
		defer srv.Close()
		classErr, queryErr := run(t, shortCappedClient(t, srv.URL))
		if classErr != nil {
			t.Errorf("RunClass ran on the short client: %v", classErr)
		}
		if queryErr != nil {
			t.Errorf("RunQuery ran on the short client: %v", queryErr)
		}
	})

	t.Run("both apply the same default deadline", func(t *testing.T) {
		srv := slowServer(10 * time.Second)
		defer srv.Close()
		restore := defaultLongRunTimeout
		defaultLongRunTimeout = 150 * time.Millisecond
		defer func() { defaultLongRunTimeout = restore }()

		classErr, queryErr := run(t, shortCappedClient(t, srv.URL))
		if !errors.Is(classErr, context.DeadlineExceeded) {
			t.Errorf("RunClass: got %v, want context.DeadlineExceeded from the shared default", classErr)
		}
		if !errors.Is(queryErr, context.DeadlineExceeded) {
			t.Errorf("RunQuery: got %v, want context.DeadlineExceeded from the shared default", queryErr)
		}
	})
}
