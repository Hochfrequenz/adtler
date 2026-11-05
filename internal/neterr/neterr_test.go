package neterr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// closedAddr returns a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func assertClean(t *testing.T, msg string, leaks ...string) {
	t.Helper()
	for _, leak := range leaks {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(leak)) {
			t.Errorf("message still contains %q: %s", leak, msg)
		}
	}
}

// TestRedact_ConnectionRefused: the URL loses scheme, host and port, and the
// dialled address in the wrapped error is replaced too.
func TestRedact_ConnectionRefused(t *testing.T) {
	addr := closedAddr(t)
	_, port, _ := net.SplitHostPort(addr)
	raw := "http://" + addr + "/sap/bc/adt/discovery?x=1"
	req, _ := http.NewRequest(http.MethodGet, raw, nil)
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Fatal("want a connection error")
	}

	got := Redact(err, "http://"+addr)
	assertClean(t, got.Error(), "127.0.0.1", ":"+port, "http://")
	if !strings.Contains(got.Error(), `"/sap/bc/adt/discovery?x=1"`) {
		t.Errorf("want path and query kept, got %s", got)
	}
	var ue *url.Error
	if !errors.As(got, &ue) {
		t.Fatalf("want *url.Error kept, got %T", got)
	}
	var opErr *net.OpError
	if !errors.As(got, &opErr) {
		t.Errorf("want the *net.OpError still reachable, got %v", got)
	}
}

// TestRedact_DNS: a host that does not resolve is named by the DNS error;
// it is removed even when the caller does not pass it.
func TestRedact_DNS(t *testing.T) {
	const host = "sap-internal.invalid"
	req, _ := http.NewRequest(http.MethodGet, "http://"+host+":8000/sap/bc/adt/discovery", nil)
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Fatal("want a lookup error")
	}
	got := Redact(err)
	assertClean(t, got.Error(), host, "8000")
	var dnsErr *net.DNSError
	if !errors.As(got, &dnsErr) {
		t.Errorf("want the *net.DNSError still reachable, got %v", got)
	}
}

// TestRedact_TimeoutStaysATimeout: url.Error.Timeout asserts on its direct
// Err, so the redacting wrapper must forward Timeout.
func TestRedact_TimeoutStaysATimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := &http.Client{Timeout: 50 * time.Millisecond}
	_, err := c.Get(srv.URL + "/sap/bc/adt/discovery")
	if err == nil {
		t.Fatal("want a timeout")
	}
	got := Redact(err, srv.URL)
	assertClean(t, got.Error(), "127.0.0.1", srv.URL)

	var ue *url.Error
	if !errors.As(got, &ue) || !ue.Timeout() {
		t.Errorf("want a *url.Error with Timeout() true, got %#v", got)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("want errors.Is(context.DeadlineExceeded), got %v", got)
	}
}

// TestRedact_CertificateNames: a hostname mismatch lists every name the
// certificate is valid for; none of them, nor the name asked for, survives.
func TestRedact_CertificateNames(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    pool,
		ServerName: "sap.internal.example",
	}}}
	_, err := c.Get(srv.URL + "/x")
	if err == nil {
		t.Fatal("want a certificate error")
	}
	var hostErr x509.HostnameError
	if !errors.As(err, &hostErr) {
		t.Fatalf("want an x509.HostnameError, got %v", err)
	}

	got := Redact(err, srv.URL)
	leaks := append([]string{"sap.internal.example", "127.0.0.1"}, srv.Certificate().DNSNames...)
	assertClean(t, got.Error(), leaks...)
}

// TestRedact_ExtraHostsInWrappedText: a host only the caller knows (here in a
// redirect message) is replaced in the wrapped text.
func TestRedact_ExtraHostsInWrappedText(t *testing.T) {
	inner := errors.New("redirect from http://sap.example:8000 to https://sap.example:8443")
	err := &url.Error{Op: "Get", URL: "https://sap.example:8443/x", Err: inner}
	got := Redact(err, "http://sap.example:8000")
	assertClean(t, got.Error(), "sap.example", "8000", "8443")
	if !errors.Is(got, inner) {
		t.Error("want the inner error still reachable")
	}
}

// TestRedact_NoHostUnchanged: an error that names no host is returned as is.
func TestRedact_NoHostUnchanged(t *testing.T) {
	if got := Redact(context.Canceled, "https://sap.example"); got != context.Canceled {
		t.Errorf("want context.Canceled itself, got %#v", got)
	}
	if Redact(nil) != nil {
		t.Error("want nil for nil")
	}
}

// TestReplaceToken_Boundaries: a short host name does not cut into a longer
// word, and the match is case-insensitive.
func TestReplaceToken_Boundaries(t *testing.T) {
	tests := []struct{ in, tok, want string }{
		{"lookup SAP: no such host", "sap", "lookup <host>: no such host"},
		{"/sap/bc and sapphire", "sap", "/<host>/bc and sapphire"},
		{"dial a.b.example:1: refused", "a.b.example:1", "dial <host>: refused"},
		{"sub.host.example", "host.example", "sub.<host>"},
		{"Größe host", "host", "Größe <host>"},
	}
	for _, tt := range tests {
		if got := replaceToken(tt.in, tt.tok, hostPlaceholder); got != tt.want {
			t.Errorf("replaceToken(%q, %q) = %q, want %q", tt.in, tt.tok, got, tt.want)
		}
	}
}

// TestScrub_IPv4Fallback: an address no typed error named is still replaced.
func TestScrub_IPv4Fallback(t *testing.T) {
	got := scrub("proxyconnect tcp: dial tcp 10.1.2.3:3128: i/o timeout", nil)
	if got != "proxyconnect tcp: dial tcp <addr>:3128: i/o timeout" {
		t.Errorf("got %q", got)
	}
}

// TestPathOnly: user info is dropped along with the host.
func TestPathOnly(t *testing.T) {
	if got := pathOnly("https://u:p@sap.example:44300/sap/bc/adt/x?a=b"); got != "/sap/bc/adt/x?a=b" {
		t.Errorf("got %q", got)
	}
	if got := pathOnly("::bad"); got != urlPlaceholder {
		t.Errorf("got %q", got)
	}
}
