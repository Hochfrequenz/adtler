package adt

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrCrossOriginRedirect is returned when the SAP system answers with a
// redirect to a different scheme or host than the request it redirects.
// Such a redirect is not followed: the caller's RoundTripper would be
// applied to the next request too, and a transport that attaches proxy
// credentials per request (for example SAP BTP's Connectivity proxy)
// would send them to a host nobody configured. net/http drops
// Authorization and cookies on a cross-domain redirect, but not what a
// RoundTripper adds. Redirects that stay on the same scheme and host are
// still followed. Match it with errors.Is; ClassifyError reports it as
// ErrorUnknown, like any other transport-level error.
var ErrCrossOriginRedirect = errors.New("adt: redirect to a different scheme or host")

// maxRedirects matches net/http's default limit, which a custom
// CheckRedirect replaces and must therefore re-implement.
const maxRedirects = 10

// sameOriginRedirect is the CheckRedirect of every http.Client this
// package builds: follow a redirect only while it stays on the scheme
// and host of the original request (via[0]), up to maxRedirects hops.
// Hosts compare case-insensitively; an explicit default port still counts
// as a different host (fail closed).
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	// Origin first, so a cross-origin redirect reports the sentinel even
	// when it is also the hop that reaches the limit.
	origin := via[0].URL
	if req.URL.Scheme != origin.Scheme || !strings.EqualFold(req.URL.Host, origin.Host) {
		hint := ""
		if origin.Scheme == "http" && req.URL.Scheme == "https" && strings.EqualFold(req.URL.Hostname(), origin.Hostname()) {
			hint = " (the system enforces HTTPS; configure the host as https://" + req.URL.Host + ")"
		}
		return fmt.Errorf("%w: %s://%s redirected to %s://%s%s",
			ErrCrossOriginRedirect, origin.Scheme, origin.Host, req.URL.Scheme, req.URL.Host, hint)
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("adt: stopped after %d redirects", maxRedirects)
	}
	return nil
}

// newShortClient and newLongClient build every http.Client this package
// uses, so that each one carries sameOriginRedirect by construction.
// The long client has no timeout; its callers bound it by context.
func newShortClient(transport http.RoundTripper, jar http.CookieJar, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transport, Jar: jar, CheckRedirect: sameOriginRedirect}
}

func newLongClient(transport http.RoundTripper, jar http.CookieJar) *http.Client {
	return &http.Client{Transport: transport, Jar: jar, CheckRedirect: sameOriginRedirect}
}
