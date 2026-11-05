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

// TestADTErrorMessageDoesNotNameTheHost: a host the server names in its own
// error text is removed from ADTError.Message, while Properties and T100Vars
// keep what SAP sent (#167).
func TestADTErrorMessageDoesNotNameTheHost(t *testing.T) {
	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
		`<namespace id="com.sap.adt"/><type id="ExceptionResourceFailure"/>` +
		`<message>RFC to sapsrv01_ABC_00 (sapsrv01.corp.example) failed</message>` +
		`<properties><entry key="T100KEY-V1">sapsrv01_ABC_00</entry></properties>` +
		`</exc:exception>`
	req, _ := http.NewRequest(http.MethodGet, "https://sapsrv01.corp.example:44300/sap/bc/adt/x", nil)
	resp := &http.Response{StatusCode: http.StatusInternalServerError, Request: req}

	err := parseResponseError(resp, strings.NewReader(body))
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("want *ADTError, got %T", err)
	}
	if want := "RFC to <host>_ABC_00 (<host>) failed"; adtErr.Message != want {
		t.Errorf("Message = %q, want %q", adtErr.Message, want)
	}
	if adtErr.T100Vars[0] != "sapsrv01_ABC_00" || adtErr.Properties["T100KEY-V1"] != "sapsrv01_ABC_00" {
		t.Errorf("structured values changed: %v / %v", adtErr.T100Vars, adtErr.Properties)
	}
}

// TestADTErrorMessageThroughClient: the same holds for a plain-text error
// body on a real round trip, where the address is the test server's.
func TestADTErrorMessageThroughClient(t *testing.T) {
	var self string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/discovery") {
			w.Header().Set("X-CSRF-Token", "tok")
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream " + self + " unreachable"))
	}))
	defer srv.Close()
	self = strings.TrimPrefix(srv.URL, "http://")

	_, err := redirectClients(srv.URL)["NewClient"].GetSource(context.Background(), redirectTestSource)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) || adtErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("want an ADTError 502, got %v", err)
	}
	assertNoHost(t, err.Error(), self, "127.0.0.1")
}
