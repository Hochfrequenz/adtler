package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// languageHeaders records the logon-language headers of every request a
// test server receives, keyed by request path.
type languageHeaders struct {
	mu   sync.Mutex
	seen map[string][2]string // path -> {sap-language, Accept-Language}
}

func (l *languageHeaders) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen[r.URL.Path] = [2]string{r.Header.Get("sap-language"), r.Header.Get("Accept-Language")}
}

func (l *languageHeaders) get(path string) ([2]string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.seen[path]
	return h, ok
}

func newLanguageServer(t *testing.T) (*httptest.Server, *languageHeaders) {
	t.Helper()
	got := &languageHeaders{seen: map[string][2]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.record(r)
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// TestConfiguredLanguageSentOnEveryRequest guards #247: the configured
// Language must reach SAP on the CSRF preflight (which opens the session and
// so fixes its logon language), on reads, on mutations and on logoff.
func TestConfiguredLanguageSentOnEveryRequest(t *testing.T) {
	srv, got := newLanguageServer(t)
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100", Language: "de"}
	client := adt.NewClient(cfg)
	ctx := context.Background()

	if _, err := client.GetSource(ctx, programsEndpoint+"/ZTEST"); err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if _, err := client.LockObject(ctx, programsEndpoint+"/ZTEST"); err != nil {
		t.Logf("LockObject: %v (only the request headers matter here)", err)
	}
	if err := client.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	want := [2]string{"DE", "de"}
	for _, path := range []string{csrfEndpoint, programsEndpoint + "/ZTEST/source/main", programsEndpoint + "/ZTEST", logoffPath} {
		h, ok := got.get(path)
		if !ok {
			t.Errorf("%s: no request received", path)
			continue
		}
		if h != want {
			t.Errorf("%s: sap-language/Accept-Language = %q/%q, want %q/%q", path, h[0], h[1], want[0], want[1])
		}
	}
}

// TestEmptyLanguageSendsNoLanguageHeaders: a hand-built SAPSystem without a
// Language leaves the logon language to SAP (SU01 default, else system
// default) instead of forcing one.
func TestEmptyLanguageSendsNoLanguageHeaders(t *testing.T) {
	srv, got := newLanguageServer(t)
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetSource(context.Background(), programsEndpoint+"/ZTEST"); err != nil {
		t.Fatalf("GetSource: %v", err)
	}

	for _, path := range []string{csrfEndpoint, programsEndpoint + "/ZTEST/source/main"} {
		h, ok := got.get(path)
		if !ok {
			t.Errorf("%s: no request received", path)
			continue
		}
		if h != [2]string{} {
			t.Errorf("%s: expected no language headers, got sap-language=%q Accept-Language=%q", path, h[0], h[1])
		}
	}
}

// TestOneLetterLanguageSendsSAPLanguageOnly: a one-letter SAP code is not a
// valid Accept-Language tag, so only sap-language carries it.
func TestOneLetterLanguageSendsSAPLanguageOnly(t *testing.T) {
	srv, got := newLanguageServer(t)
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100", Language: "d"}
	client := adt.NewClient(cfg)

	if _, err := client.GetSource(context.Background(), programsEndpoint+"/ZTEST"); err != nil {
		t.Fatalf("GetSource: %v", err)
	}

	h, ok := got.get(csrfEndpoint)
	if !ok {
		t.Fatalf("%s: no request received", csrfEndpoint)
	}
	if h != [2]string{"D", ""} {
		t.Errorf("sap-language/Accept-Language = %q/%q, want %q/%q", h[0], h[1], "D", "")
	}
}
