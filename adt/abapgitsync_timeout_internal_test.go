package adt

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// abapGitSlowServer serves the CSRF preflight instantly and delays every other
// response by delay with a valid empty list. Shutdown releases sleeping
// handlers first so httptest.Server.Close does not block.
func abapGitSlowServer(t *testing.T, delay time.Duration) *httpClient {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		case <-stop:
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"repos":[]}`))
	}))
	t.Cleanup(func() {
		close(stop)
		srv.Close()
	})
	c, ok := NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}).(*httpClient)
	if !ok {
		t.Fatal("NewClient did not return *httpClient")
	}
	return c
}

func TestListAbapGitRepos_DefaultDeadline(t *testing.T) {
	c := abapGitSlowServer(t, 500*time.Millisecond)
	restore := defaultLongRunTimeout
	defaultLongRunTimeout = 50 * time.Millisecond
	defer func() { defaultLongRunTimeout = restore }()

	_, err := c.ListAbapGitRepos(context.Background())
	var netErr net.Error
	timedOut := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
	if err == nil || !timedOut {
		t.Fatalf("got %v, want a deadline/timeout error from the default deadline", err)
	}
}

func TestListAbapGitRepos_CallerDeadlineWins(t *testing.T) {
	c := abapGitSlowServer(t, 200*time.Millisecond)
	restore := defaultLongRunTimeout
	defaultLongRunTimeout = 50 * time.Millisecond
	defer func() { defaultLongRunTimeout = restore }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.ListAbapGitRepos(ctx); err != nil {
		t.Fatalf("caller deadline must override the default, got %v", err)
	}
}
