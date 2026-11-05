package adt

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// assertNoHost fails when msg names any of the given host or address forms.
func assertNoHost(t *testing.T, msg string, forms ...string) {
	t.Helper()
	for _, f := range forms {
		if f != "" && strings.Contains(strings.ToLower(msg), strings.ToLower(f)) {
			t.Errorf("error text names %q: %s", f, msg)
		}
	}
}

// TestTransportErrorsDoNotNameTheHost: a request that fails below HTTP
// returns an error whose text names neither the configured host nor its
// port, on reads, on mutations and for every client constructor (#167).
// Integration tests print these errors, and their output is posted on
// public pull requests.
func TestTransportErrorsDoNotNameTheHost(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens there any more: connection refused
	_, port, _ := net.SplitHostPort(addr)

	hosts := map[string]string{
		"refused":    "http://" + addr,
		"unresolved": "http://sap-internal.invalid:" + port,
	}
	for hname, host := range hosts {
		for cname, c := range redirectClients(host) {
			t.Run(hname+"/"+cname, func(t *testing.T) {
				ctx := context.Background()
				_, readErr := c.GetSource(ctx, redirectTestSource)
				_, mutErr := c.LockObject(ctx, redirectTestSource)
				for _, err := range []error{readErr, mutErr} {
					if err == nil {
						t.Fatal("want a transport error")
					}
					assertNoHost(t, err.Error(), "127.0.0.1", "sap-internal.invalid", ":"+port, "http://")
					var ue *url.Error
					if !errors.As(err, &ue) {
						t.Errorf("want a *url.Error in the chain, got %T: %v", err, err)
					}
				}
			})
		}
	}
}

// TestTransportTimeoutStillClassifies: the redacted error keeps reporting a
// timeout, which isTimeoutErr (debugger) relies on.
func TestTransportTimeoutStillClassifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, Client: "100", User: "TESTUSER", Password: "testpass"}
	c := NewClient(cfg).(*httpClient)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.GetSource(ctx, redirectTestSource)
	if err == nil {
		t.Fatal("want a timeout")
	}
	assertNoHost(t, err.Error(), srv.URL, strings.TrimPrefix(srv.URL, "http://"))
	if !isTimeoutErr(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want a timeout error, got %v", err)
	}
}

// TestCrossOriginRedirectErrorDoesNotNameHosts: the redirect refusal names
// both the origin and the target in its own text; neither reaches the
// caller, while errors.Is still finds the sentinel.
func TestCrossOriginRedirectErrorDoesNotNameHosts(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()
	target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	sap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusFound)
	}))
	defer sap.Close()

	_, err := redirectClients(sap.URL)["NewClient"].GetSource(context.Background(), redirectTestSource)
	if !errors.Is(err, ErrCrossOriginRedirect) {
		t.Fatalf("want ErrCrossOriginRedirect, got %v", err)
	}
	_, sapPort, _ := net.SplitHostPort(strings.TrimPrefix(sap.URL, "http://"))
	_, otherPort, _ := net.SplitHostPort(strings.TrimPrefix(other.URL, "http://"))
	assertNoHost(t, err.Error(), "127.0.0.1", "localhost", ":"+sapPort, ":"+otherPort)
}
