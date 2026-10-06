package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// concurrent401s is how many requests are rejected together with the same
// expired token in the single-flight refresh tests (issue #193).
const concurrent401s = 4

// expiredTokenServer rejects every request carrying "Bearer t0" with 401, but
// holds each rejection until concurrent401s of them have arrived, so all of
// them are already on their way back as 401 before any refresh can happen.
// Any other token is accepted. The CSRF preflight always succeeds.
func expiredTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	var arrived atomic.Int64
	allArrived := make(chan struct{})
	var closeOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Authorization") == "Bearer t0" {
			if arrived.Add(1) == concurrent401s {
				closeOnce.Do(func() { close(allArrived) })
			}
			select {
			case <-allArrived:
			case <-time.After(5 * time.Second):
				t.Error("not every request reached the server with the expired token")
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// countingRefreshClient returns an OAuth client starting at token "t0" whose
// refresh callback hands out t1, t2, ... and counts its calls.
func countingRefreshClient(t *testing.T, host string) (*httpClient, *atomic.Int64) {
	t.Helper()
	var refreshes atomic.Int64
	c := NewClientWithToken(raceTestConfig(host), "t0", func(string) (string, error) {
		return fmt.Sprintf("t%d", refreshes.Add(1)), nil
	}).(*httpClient)
	if err := c.ensureCSRF(context.Background()); err != nil {
		t.Fatalf("CSRF preflight: %v", err)
	}
	return c, &refreshes
}

// assertSingleRefresh runs concurrent401s copies of do at once, each of which
// is rejected with the expired token, and asserts that all of them succeed on
// retry after exactly one call to onTokenRefresh.
func assertSingleRefresh(t *testing.T, c *httpClient, refreshes *atomic.Int64, do func() (*http.Response, error)) {
	t.Helper()
	fns := make([]func(), concurrent401s)
	for i := range fns {
		fns[i] = func() {
			resp, err := do()
			if err != nil {
				t.Errorf("request: %v", err)
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("retried request: status %d, want 200", resp.StatusCode)
			}
		}
	}
	runConcurrently(fns...)

	if got := refreshes.Load(); got != 1 {
		t.Errorf("onTokenRefresh called %d times for %d concurrent 401s, want 1", got, concurrent401s)
	}
	if c.token() != "t1" {
		t.Errorf("token: got %q, want %q", c.token(), "t1")
	}
}

func TestTokenRefresh_ConcurrentReadsRefreshOnce(t *testing.T) {
	c, refreshes := countingRefreshClient(t, expiredTokenServer(t).URL)
	assertSingleRefresh(t, c, refreshes, func() (*http.Response, error) {
		return c.doRead(context.Background(), raceOKPath, nil)
	})
}

func TestTokenRefresh_ConcurrentMutatesRefreshOnce(t *testing.T) {
	c, refreshes := countingRefreshClient(t, expiredTokenServer(t).URL)
	assertSingleRefresh(t, c, refreshes, func() (*http.Response, error) {
		return c.doMutate(context.Background(), http.MethodPost, raceOKPath, nil, nil)
	})
}
