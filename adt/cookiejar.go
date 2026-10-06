package adt

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
)

// resettableJar is an http.CookieJar whose contents can be discarded while
// requests are in flight.
//
// net/http reads Client.Jar on every request, before sending and again when it
// stores the response cookies, without any lock of ours. Reassigning the field
// on a live client is therefore a data race with every concurrent request
// (issue #191). Instead, each client gets one resettableJar for its lifetime,
// and Logout empties it through Reset, which swaps the inner jar under a mutex.
type resettableJar struct {
	mu    sync.Mutex
	inner *cookiejar.Jar
}

func newResettableJar() *resettableJar {
	inner, _ := cookiejar.New(nil)
	return &resettableJar{inner: inner}
}

// jar returns the current inner jar. A request that started before a Reset
// may store its response cookies in the old jar; they are discarded with it.
func (j *resettableJar) jar() *cookiejar.Jar {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.inner
}

func (j *resettableJar) Cookies(u *url.URL) []*http.Cookie {
	return j.jar().Cookies(u)
}

func (j *resettableJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.jar().SetCookies(u, cookies)
}

// Reset discards every stored cookie.
func (j *resettableJar) Reset() {
	inner, _ := cookiejar.New(nil)
	j.mu.Lock()
	j.inner = inner
	j.mu.Unlock()
}
