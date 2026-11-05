// Package neterr removes host names and network addresses from the errors
// net/http returns.
//
// A failed request comes back from (*http.Client).Do as a *url.Error whose
// text carries the full request URL, and the error it wraps often names the
// host or address again: a DNS lookup, a refused connection, a certificate
// issued for another name. Those texts end up in logs, test output and pull
// request comments, and for an internal system the host name is exactly what
// must not leave the building (adtler#167). Redact rewrites such an error
// once, where it enters the library, so that no caller has to.
package neterr

import (
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Placeholders that replace what Redact removes.
const (
	hostPlaceholder = "<host>"
	addrPlaceholder = "<addr>"
	urlPlaceholder  = "<url>"
)

// ipv4Re catches an IPv4 address the typed errors did not name, for example
// one that only appears inside a proxy's error text.
var ipv4Re = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// Redact returns err with every host name, port and network address it
// carries replaced by a placeholder. hosts names further hosts to remove,
// typically the configured system URL ("https://host:port" or a bare host);
// the hosts the error itself names are found without help.
//
// A *url.Error at the top stays a *url.Error, so that errors.As and its
// Timeout method keep working, but its URL is cut down to path and query:
//
//	Get "/sap/bc/adt/discovery": dial tcp <addr>: connect: connection refused
//
// The wrapped error stays reachable through errors.Is and errors.As; only
// its text is replaced. A caller that unwraps it and prints the inner error
// directly therefore still sees the original text.
//
// An error that names no host is returned unchanged, so identity checks such
// as err == context.Canceled still hold for it.
func Redact(err error, hosts ...string) error {
	if err == nil {
		return nil
	}
	tokens := collectTokens(err, hosts)
	var ue *url.Error
	if errors.As(err, &ue) && ue == err {
		return &url.Error{Op: ue.Op, URL: pathOnly(ue.URL), Err: wrap(ue.Err, tokens)}
	}
	return wrap(err, tokens)
}

// wrap returns err behind a scrubbed text, or err itself when scrubbing
// changes nothing.
func wrap(err error, tokens []string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	clean := scrub(msg, tokens)
	if clean == msg {
		return err
	}
	return &redacted{msg: clean, err: err}
}

// redacted carries a scrubbed text for an error it still unwraps to.
type redacted struct {
	msg string
	err error
}

func (e *redacted) Error() string { return e.msg }
func (e *redacted) Unwrap() error { return e.err }

// Timeout and Temporary are forwarded because url.Error asserts them on its
// direct Err rather than through errors.As.
func (e *redacted) Timeout() bool {
	var t interface{ Timeout() bool }
	return errors.As(e.err, &t) && t.Timeout()
}

func (e *redacted) Temporary() bool {
	var t interface{ Temporary() bool }
	return errors.As(e.err, &t) && t.Temporary()
}

// pathOnly keeps path and query of a request URL and drops scheme, user
// info, host and port.
func pathOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return urlPlaceholder
	}
	return (&url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}).String()
}

// collectTokens gathers every host and address string the error chain and
// the caller name, longest first, so that "host:port" is replaced before
// "host" alone.
func collectTokens(err error, hosts []string) []string {
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		seen[s] = true
		if h, _, splitErr := net.SplitHostPort(s); splitErr == nil && h != "" {
			seen[h] = true
		}
	}
	addURL := func(raw string) {
		if !strings.Contains(raw, "://") {
			add(raw)
			return
		}
		if u, parseErr := url.Parse(raw); parseErr == nil {
			add(u.Host)
			add(u.Hostname())
		}
	}
	for _, h := range hosts {
		addURL(h)
	}

	var ue *url.Error
	if errors.As(err, &ue) {
		addURL(ue.URL)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Addr != nil {
			add(opErr.Addr.String())
		}
		if opErr.Source != nil {
			add(opErr.Source.String())
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		add(dnsErr.Name)
		add(dnsErr.Server)
	}
	var addrErr *net.AddrError
	if errors.As(err, &addrErr) {
		add(addrErr.Addr)
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		add(hostErr.Host)
		addCertificate(add, hostErr.Certificate)
	}
	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		addCertificate(add, authErr.Cert)
	}
	var invalidErr x509.CertificateInvalidError
	if errors.As(err, &invalidErr) {
		addCertificate(add, invalidErr.Cert)
	}

	tokens := make([]string, 0, len(seen))
	for s := range seen {
		tokens = append(tokens, s)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i]) != len(tokens[j]) {
			return len(tokens[i]) > len(tokens[j])
		}
		return tokens[i] < tokens[j]
	})
	return tokens
}

// addCertificate adds the names a certificate is issued for: a hostname
// error lists them all ("valid for a, b, not c").
func addCertificate(add func(string), cert *x509.Certificate) {
	if cert == nil {
		return
	}
	add(cert.Subject.CommonName)
	for _, n := range cert.DNSNames {
		add(n)
	}
	for _, ip := range cert.IPAddresses {
		add(ip.String())
	}
}

// scrub replaces each token, case-insensitively and only where it is not
// part of a longer name, then any IPv4 address left over.
func scrub(msg string, tokens []string) string {
	for _, tok := range tokens {
		msg = replaceToken(msg, tok, placeholderFor(tok))
	}
	return ipv4Re.ReplaceAllString(msg, addrPlaceholder)
}

func placeholderFor(tok string) string {
	h := tok
	if host, _, err := net.SplitHostPort(tok); err == nil {
		h = host
	}
	if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return addrPlaceholder
	}
	return hostPlaceholder
}

// replaceToken replaces tok in s where neither neighbour is a letter, digit
// or hyphen, so a short host name does not cut into a longer word.
func replaceToken(s, tok, repl string) string {
	lower, ltok := asciiLower(s), asciiLower(tok)
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], ltok)
		if j < 0 {
			b.WriteString(s[i:])
			return b.String()
		}
		start, end := i+j, i+j+len(ltok)
		if (start > 0 && isNameByte(lower[start-1])) || (end < len(lower) && isNameByte(lower[end])) {
			b.WriteString(s[i : start+1])
			i = start + 1
			continue
		}
		b.WriteString(s[i:start])
		b.WriteString(repl)
		i = end
	}
}

// asciiLower lowers ASCII letters only, so byte offsets stay those of s
// (strings.ToLower may change the length of non-ASCII text).
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
}
