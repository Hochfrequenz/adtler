package adt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Issue #188: StartListener used to make room for its long poll by raising
// Timeout on the debug session's SHARED short *http.Client for the duration of
// the poll, then restoring it. That had two consequences for every other
// request sent on the same DebugSession while the listener was polling
// (Attach, Step, GetStack, GetVariable, StopListener, GetDebuggeeSessions):
//
//  1. the request ran with the listener's raised timeout instead of the short
//     client's normal one, and
//  2. the write to http.Client.Timeout raced with net/http reading that field
//     when the other request started (go test -race flags it).
//
// The fix sends the listener's POST through the long client (no timeout of its
// own) and bounds it with a context deadline instead, so the short client is
// never written to. These tests use the same technique as the issue #114 tests
// in classrun_timeout_internal_test.go: shrink the short client's timeout to
// milliseconds and observe which requests survive it.

const (
	// debuggerListenersPath is the listener endpoint (StartListener/StopListener).
	debuggerListenersPath = "/sap/bc/adt/debugger/listeners"
	// listenerASXResponse is a minimal listener answer carrying a debuggee ID.
	listenerASXResponse = `<?xml version="1.0" encoding="utf-8"?>` +
		`<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
		`<DEBUGGEE_ID>DBG1</DEBUGGEE_ID></DATA></asx:values></asx:abap>`
)

// debuggerServer serves an instant CSRF preflight, answers the listener POST
// after listenerDelay and every other request after otherDelay. listenerStarted
// is closed once the listener POST has reached the server. Shutdown follows the
// slowServer pattern (stop channel closed before srv.Close), so callers must
// not close the server themselves.
func debuggerServer(t *testing.T, listenerDelay, otherDelay time.Duration) (srv *httptest.Server, listenerStarted <-chan struct{}) {
	t.Helper()
	stop := make(chan struct{})
	started := make(chan struct{})
	var startedOnce sync.Once
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		isListenerPoll := r.URL.Path == debuggerListenersPath && r.Method == http.MethodPost
		delay := otherDelay
		if isListenerPoll {
			delay = listenerDelay
			startedOnce.Do(func() { close(started) })
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		case <-stop:
			return
		}
		if isListenerPoll {
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(listenerASXResponse))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})
	return srv, started
}

// TestStartListener_NotCappedByShortClient: the listener poll must not be cut
// off by the short client's timeout. With the short client capped at 30 ms and
// the listener answering after 250 ms, StartListener must still return the
// debuggee.
func TestStartListener_NotCappedByShortClient(t *testing.T) {
	srv, _ := debuggerServer(t, slowResponse, 0)
	dbg := NewDebugSession(shortCappedClient(t, srv.URL), "U")

	res, err := dbg.StartListener(context.Background(), 60)
	if err != nil {
		t.Fatalf("StartListener was capped by the short HTTP client: %v", err)
	}
	if res.Status != "attached" || res.DebuggeeID != "DBG1" {
		t.Errorf("result: got status=%q debuggee=%q, want attached/DBG1", res.Status, res.DebuggeeID)
	}
}

// TestStartListener_HonoursCallerDeadline: moving to the long client must not
// remove the limit. A caller deadline earlier than timeoutSeconds+10 s must
// abort the poll promptly.
func TestStartListener_HonoursCallerDeadline(t *testing.T) {
	srv, _ := debuggerServer(t, stalledResponse, 0)
	dbg := NewDebugSession(shortCappedClient(t, srv.URL), "U")

	ctx, cancel := context.WithTimeout(context.Background(), tinyDeadline)
	defer cancel()

	start := time.Now()
	_, err := dbg.StartListener(ctx, 60)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected the caller's %v deadline to abort StartListener, got nil error", tinyDeadline)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error: got %v, want a context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("StartListener took %v — the caller's deadline was not honoured promptly", elapsed)
	}
}

// TestStartListener_DoesNotLeakTimeoutToConcurrentRequests: while the listener
// polls, another request on the same DebugSession must keep the short client's
// timeout. Here the short client is capped at 30 ms and GetStack's endpoint
// answers after 250 ms, so GetStack must time out. Before the fix it inherited
// the listener's raised timeout (timeoutSeconds+10 s) and succeeded.
func TestStartListener_DoesNotLeakTimeoutToConcurrentRequests(t *testing.T) {
	srv, listenerStarted := debuggerServer(t, stalledResponse, slowResponse)
	dbg := NewDebugSession(shortCappedClient(t, srv.URL), "U")

	ctx, cancel := context.WithCancel(context.Background())
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		_, _ = dbg.StartListener(ctx, 60)
	}()
	defer func() {
		cancel()
		<-listenerDone
	}()

	select {
	case <-listenerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener POST never reached the server")
	}

	_, err := dbg.GetStack(context.Background())
	if err == nil {
		t.Fatal("GetStack succeeded although its server answered after the short client's timeout — " +
			"the listener's raised timeout leaked into a concurrent request")
	}
	if !isTimeoutErr(err) {
		t.Errorf("GetStack error: got %v, want a client timeout", err)
	}
}

// TestStartListener_ConcurrentRequestsRaceFree: run StartListener and other
// requests on the same DebugSession concurrently. Run with -race: before the
// fix StartListener wrote http.Client.Timeout while net/http read it for the
// concurrent request, which the race detector reports.
func TestStartListener_ConcurrentRequestsRaceFree(t *testing.T) {
	srv, _ := debuggerServer(t, 20*time.Millisecond, 0)
	dbg := NewDebugSession(shortCappedClient(t, srv.URL), "U")
	// Give the short client room for the instant endpoints; only the race
	// matters here, not the cap.
	dbg.client.http.Timeout = 5 * time.Second

	// Establish the CSRF token up front so both goroutines go straight to
	// their requests.
	if _, err := dbg.GetDebuggeeSessions(context.Background()); err != nil {
		t.Fatalf("warm-up request: %v", err)
	}

	for i := 0; i < 5; i++ {
		var wg sync.WaitGroup
		begin := make(chan struct{})
		var listenerErr, stackErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-begin
			_, listenerErr = dbg.StartListener(context.Background(), 1)
		}()
		go func() {
			defer wg.Done()
			<-begin
			for j := 0; j < 5; j++ {
				if _, stackErr = dbg.GetStack(context.Background()); stackErr != nil {
					return
				}
			}
		}()
		close(begin)
		wg.Wait()
		if listenerErr != nil {
			t.Fatalf("StartListener: %v", listenerErr)
		}
		if stackErr != nil {
			t.Fatalf("GetStack: %v", stackErr)
		}
	}
}
