package adt_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"

	"github.com/Hochfrequenz/adtler/adt"
)

// rejectionRecorder passes every request through and remembers whether SAP
// ever answered 401, so newLoggedOffClient can skip the logoff for a rejected
// credential (a 401 is final under Basic auth, #222).
type rejectionRecorder struct {
	base     http.RoundTripper
	rejected atomic.Bool
}

func (r *rejectionRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		r.rejected.Store(true)
	}
	return resp, err
}

// newLoggedOffClient builds a client the way adt.NewClient does and logs its
// SAP session off when the test ends (#300). Logging off releases every edit
// lock the session still holds, so a test that stops between LockObject and
// UnlockObject leaves no lock on a shared fixture for the tests after it. The
// cleanup is registered first, so it runs after every cleanup the test
// registers itself. After a 401 it sends nothing: the session holds no locks,
// and the logoff would only count one more failed logon.
func newLoggedOffClient(t *testing.T, cfg sapmcpconfig.SAPSystem) adt.Client {
	t.Helper()
	rec := &rejectionRecorder{base: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify}, //nolint:gosec // as adt.NewClient
	}}
	c := adt.NewClientWithTransport(cfg, rec)
	t.Cleanup(func() {
		if !rec.rejected.Load() {
			_ = c.Logout(context.Background())
		}
	})
	return c
}

// TestNewLoggedOffClient: the client logs off when its test ends, but sends
// nothing more once SAP has answered 401.
func TestNewLoggedOffClient(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		wantLogoff bool
	}{
		{"working session is logged off", http.StatusOK, true},
		{"rejected credential gets no logoff", http.StatusUnauthorized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests, logoffs atomic.Int32
			srv := newCountingServer(t, tc.status, &requests, &logoffs)
			t.Run("test using the client", func(t *testing.T) {
				_, _ = newLoggedOffClient(t, newTestConfig(srv.URL)).GetSource(context.Background(), probeObjectURI)
			})
			if got := logoffs.Load() == 1; got != tc.wantLogoff {
				t.Errorf("logoff sent: %v, want %v (%d requests in total)", got, tc.wantLogoff, requests.Load())
			}
			if !tc.wantLogoff && requests.Load() != 1 {
				t.Errorf("server received %d requests after a 401, want 1", requests.Load())
			}
		})
	}
}

// newCountingServer answers every request with status, counting all requests
// and the logoffs among them.
func newCountingServer(t *testing.T, status int, requests, logoffs *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/sap/public/bc/icf/logoff" {
			logoffs.Add(1)
		}
		if status == http.StatusOK && r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}
