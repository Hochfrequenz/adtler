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

const redirectTestSource = "/sap/bc/adt/programs/programs/ZTEST"

// redirectClients builds one client per constructor this package offers,
// plus a freshSession copy, all pointed at host.
func redirectClients(host string) map[string]*httpClient {
	cfg := sapmcpconfig.SAPSystem{Host: host, Client: "100", User: "TESTUSER", Password: "testpass"}
	basic := NewClient(cfg).(*httpClient)
	return map[string]*httpClient{
		"NewClient":              basic,
		"NewClientWithToken":     NewClientWithToken(cfg, "tok", nil).(*httpClient),
		"NewClientWithTransport": NewClientWithTransport(cfg, http.DefaultTransport).(*httpClient),
		"freshSession":           basic.freshSession(),
	}
}

// TestCrossOriginRedirectIsNotFollowed: a redirect to another host, port
// or scheme never reaches its target, for every client this package builds.
// "Another host" is the second server under the name localhost instead of
// 127.0.0.1; "another scheme" is the same address via https.
func TestCrossOriginRedirectIsNotFollowed(t *testing.T) {
	var elsewhereHits atomic.Int32
	count := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHits.Add(1)
		_, _ = w.Write([]byte(`REPORT ZOTHER.`))
	})
	otherPort := httptest.NewServer(count)
	defer otherPort.Close()

	var target atomic.Value // string; set per subtest, read by the handler
	sap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.Load().(string)+r.URL.Path, http.StatusFound)
	}))
	defer sap.Close()

	targets := map[string]string{
		"other host":   strings.Replace(otherPort.URL, "127.0.0.1", "localhost", 1),
		"other port":   otherPort.URL,
		"other scheme": strings.Replace(sap.URL, "http://", "https://", 1),
	}
	for tname, tgt := range targets {
		for name, c := range redirectClients(sap.URL) {
			t.Run(tname+"/"+name, func(t *testing.T) {
				target.Store(tgt)
				before := elsewhereHits.Load()
				_, err := c.GetSource(context.Background(), redirectTestSource)
				if got := elsewhereHits.Load() - before; got != 0 {
					t.Fatalf("redirect target was contacted %d time(s)", got)
				}
				if !errors.Is(err, ErrCrossOriginRedirect) {
					t.Fatalf("want ErrCrossOriginRedirect, got %v", err)
				}
			})
		}
	}
}

// TestRedirectLoopStops: a custom CheckRedirect replaces net/http's
// 10-hop limit, so the policy must re-implement it; without it a
// same-origin loop runs until the client's timeout.
func TestRedirectLoopStops(t *testing.T) {
	var hits atomic.Int32
	sap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	defer sap.Close()

	for name, c := range redirectClients(sap.URL) {
		t.Run(name, func(t *testing.T) {
			before := hits.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := c.GetSource(ctx, redirectTestSource)
			if err == nil || errors.Is(err, ErrCrossOriginRedirect) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("want the hop-limit error, got %v", err)
			}
			// 10 is net/http's default, spelled out rather than taken
			// from maxRedirects so that changing the constant fails here.
			if got := hits.Load() - before; got != 10 {
				t.Fatalf("want 10 requests, got %d", got)
			}
		})
	}
}

// TestSameOriginRedirectIsFollowed: a redirect that stays on the host is
// still followed, for every client this package builds.
func TestSameOriginRedirectIsFollowed(t *testing.T) {
	sap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == redirectTestSource {
			http.Redirect(w, r, redirectTestSource+"/moved", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`REPORT ZTEST.`))
	}))
	defer sap.Close()

	for name, c := range redirectClients(sap.URL) {
		t.Run(name, func(t *testing.T) {
			res, err := c.GetSource(context.Background(), redirectTestSource)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Source != `REPORT ZTEST.` {
				t.Fatalf("want the redirected body, got %q", res.Source)
			}
		})
	}
}

// TestRedirectPolicyOnBothClients: http and httpLong both carry the policy;
// a hostname-only change (same port) is refused; host case is ignored.
func TestRedirectPolicyOnBothClients(t *testing.T) {
	sap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, strings.Replace("http://"+r.Host, "localhost", "127.0.0.1", 1)+"/ok", http.StatusFound)
		case "/case":
			http.Redirect(w, r, "http://"+strings.ToUpper(r.Host)+"/ok", http.StatusFound)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer sap.Close()
	host := strings.Replace(sap.URL, "127.0.0.1", "localhost", 1)
	for name, c := range redirectClients(host) {
		for hname, hc := range map[string]*http.Client{"http": c.http, "httpLong": c.httpLong} {
			t.Run(name+"/"+hname, func(t *testing.T) {
				resp, err := hc.Get(host + "/cross")
				if err == nil {
					_ = resp.Body.Close()
				}
				if !errors.Is(err, ErrCrossOriginRedirect) {
					t.Fatalf("same port, other hostname: want ErrCrossOriginRedirect, got %v", err)
				}
				resp, err = hc.Get(host + "/case")
				if err != nil {
					t.Fatalf("upper-cased host: want followed, got %v", err)
				}
				_ = resp.Body.Close()
			})
		}
	}
}

// TestCrossOriginRedirectHintsAtHTTPS: an http->https redirect on the same
// host names the fix in the error.
func TestCrossOriginRedirectHintsAtHTTPS(t *testing.T) {
	origin, _ := http.NewRequest(http.MethodGet, "http://sap.example:8000/x", nil)
	next, _ := http.NewRequest(http.MethodGet, "https://sap.example:8443/x", nil)
	err := sameOriginRedirect(next, []*http.Request{origin})
	if !errors.Is(err, ErrCrossOriginRedirect) || !strings.Contains(err.Error(), "configure the host as https://sap.example:8443") {
		t.Fatalf("want the HTTPS hint, got %v", err)
	}
}
