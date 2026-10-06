package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Tests for issue #191: shared state on httpClient and DebugSession that
// overlapping calls touch. The *RaceFree tests only prove something under
// -race, which the unit-test workflow enables.

const (
	raceBreakpointsPath = "/sap/bc/adt/debugger/breakpoints"
	raceOKPath          = "/sap/bc/adt/ok"
	raceExpiredPath     = "/sap/bc/adt/expired"
	raceSessionCookie   = "SAP_SESSIONID_TST_100"
	raceRounds          = 5
)

// sharedStateServer answers the CSRF preflight with a session cookie, the
// breakpoint POST with a fresh breakpoint ID, raceExpiredPath with 401 and
// everything else with 200.
func sharedStateServer(t *testing.T) *httptest.Server {
	t.Helper()
	var sessions, breakpoints atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case r.URL.Path == discoveryPath:
			http.SetCookie(w, &http.Cookie{Name: raceSessionCookie, Value: fmt.Sprintf("s%d", sessions.Add(1)), Path: "/"})
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == raceBreakpointsPath && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><dbg:breakpoints xmlns:dbg="http://www.sap.com/adt/debugger"><breakpoint kind="line" clientId="0" id="BP%d"/></dbg:breakpoints>`, breakpoints.Add(1))
		case r.URL.Path == raceExpiredPath:
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func raceTestConfig(host string) sapmcpconfig.SAPSystem {
	return sapmcpconfig.SAPSystem{Host: host, User: "U", Password: "P", Client: "100"}
}

// runConcurrently starts every fn at once and waits for all of them.
func runConcurrently(fns ...func()) {
	var wg sync.WaitGroup
	begin := make(chan struct{})
	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			fn()
		}()
	}
	close(begin)
	wg.Wait()
}

// readOK issues one GET on c and fails the test unless it answers 200.
func readOK(t *testing.T, c *httpClient, path string) {
	t.Helper()
	resp, err := c.doRead(context.Background(), path, nil)
	if err != nil {
		t.Errorf("GET %s: %v", path, err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
}

// TestLogout_ConcurrentRequestsRaceFree: Logout used to assign a new cookie jar
// to c.http.Jar and c.httpLong.Jar while net/http read those fields, unlocked,
// for a request in flight on the same client.
func TestLogout_ConcurrentRequestsRaceFree(t *testing.T) {
	srv := sharedStateServer(t)
	c := NewClient(raceTestConfig(srv.URL)).(*httpClient)

	for i := 0; i < raceRounds; i++ {
		runConcurrently(
			func() {
				if err := c.Logout(context.Background()); err != nil {
					t.Errorf("Logout: %v", err)
				}
			},
			func() {
				for j := 0; j < raceRounds; j++ {
					readOK(t, c, raceOKPath)
				}
			},
		)
	}
}

// TestLogout_EmptiesCookieJar: Logout must still discard the session cookies
// of the terminated SAP session, now by emptying the jar both clients share
// instead of replacing it.
func TestLogout_EmptiesCookieJar(t *testing.T) {
	srv := sharedStateServer(t)
	c := NewClient(raceTestConfig(srv.URL)).(*httpClient)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	readOK(t, c, raceOKPath)
	if len(c.http.Jar.Cookies(u)) == 0 {
		t.Fatal("no session cookie stored after the CSRF preflight; the test server did not set one")
	}
	jar := c.http.Jar

	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := c.http.Jar.Cookies(u); len(got) != 0 {
		t.Errorf("short client still sends %d cookie(s) after Logout", len(got))
	}
	if got := c.httpLong.Jar.Cookies(u); len(got) != 0 {
		t.Errorf("long client still sends %d cookie(s) after Logout", len(got))
	}
	if c.http.Jar != jar || c.httpLong.Jar != jar {
		t.Error("Logout replaced a client's Jar field; it must reset the shared jar in place (#191)")
	}
}

// TestTokenRefresh_ConcurrentRequestsRaceFree: the 401 retry path writes the
// refreshed OAuth token while setAuth (every request) and freshSession read it.
func TestTokenRefresh_ConcurrentRequestsRaceFree(t *testing.T) {
	srv := sharedStateServer(t)
	var refreshes atomic.Int64
	c := NewClientWithToken(raceTestConfig(srv.URL), "t0", func(string) (string, error) {
		return fmt.Sprintf("t%d", refreshes.Add(1)), nil
	}).(*httpClient)

	for i := 0; i < raceRounds; i++ {
		runConcurrently(
			func() {
				// Always 401, so every call refreshes the token.
				resp, err := c.doRead(context.Background(), raceExpiredPath, nil)
				if err != nil {
					t.Errorf("GET %s: %v", raceExpiredPath, err)
					return
				}
				_ = resp.Body.Close()
			},
			func() {
				for j := 0; j < raceRounds; j++ {
					readOK(t, c, raceOKPath)
				}
			},
			func() {
				for j := 0; j < raceRounds; j++ {
					_ = c.freshSession()
				}
			},
		)
	}

	if refreshes.Load() != raceRounds {
		t.Errorf("token refreshed %d times, want %d", refreshes.Load(), raceRounds)
	}
	if want := fmt.Sprintf("t%d", raceRounds); c.token() != want {
		t.Errorf("token after refreshes: got %q, want %q", c.token(), want)
	}
	if got := c.freshSession().token(); got != c.token() {
		t.Errorf("freshSession token: got %q, want the parent's %q", got, c.token())
	}
}

// TestDebugSession_ConcurrentCallsRaceFree: SetBreakpoint used to insert into a
// map that a concurrent SetBreakpoint or StopListener also wrote, and Attach
// wrote a field concurrent Attach calls shared. Two concurrent map writes can
// crash the process even without -race.
func TestDebugSession_ConcurrentCallsRaceFree(t *testing.T) {
	srv := sharedStateServer(t)
	dbg := NewDebugSession(NewClient(raceTestConfig(srv.URL)), "U")
	ctx := context.Background()

	setBreakpoint := func() {
		for j := 0; j < raceRounds; j++ {
			bp, err := dbg.SetBreakpoint(ctx, "/sap/bc/adt/programs/programs/ztest/source/main", 2, "PROG/P", "ZTEST")
			if err != nil {
				t.Errorf("SetBreakpoint: %v", err)
				return
			}
			if bp.ID == "" {
				t.Errorf("SetBreakpoint: empty ID (error message %q)", bp.ErrorMessage)
			}
		}
	}
	attach := func() {
		if err := dbg.Attach(ctx, "DEBUGGEE"); err != nil {
			t.Errorf("Attach: %v", err)
		}
	}

	for i := 0; i < raceRounds; i++ {
		runConcurrently(
			setBreakpoint,
			setBreakpoint,
			attach,
			attach,
			func() {
				if err := dbg.StopListener(ctx); err != nil {
					t.Errorf("StopListener: %v", err)
				}
			},
		)
	}
}
