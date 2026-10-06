package adt

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// tokenSource holds an OAuth2 access token and the callback that refreshes it.
//
// A client and every freshSession derived from it share one tokenSource, while
// each keeps its own cookie jar and CSRF token. Refreshes therefore serialize
// across all of them, and a token refreshed in a debug session or a RunClass
// session reaches the parent too (issue #197). Before, each session refreshed
// under its own c.mu: a parent and a clone getting a 401 at once called the
// callback concurrently with the same refresh token, which an identity provider
// that rotates refresh tokens answers by rejecting the second call.
//
// A nil *tokenSource stands for Basic Auth: no token, nothing to refresh.
type tokenSource struct {
	token   atomic.Pointer[string]
	mu      sync.Mutex // serializes refresh; never held while taking an httpClient's c.mu
	refresh func(string) (string, error)
}

func newTokenSource(token string, refresh func(string) (string, error)) *tokenSource {
	s := &tokenSource{refresh: refresh}
	s.token.Store(&token)
	return s
}

// current returns the access token, or "" for Basic Auth.
//
// The token is atomic rather than guarded by a mutex because setAuth reads it
// for every request, including fetchCSRFToken's, which already runs under
// c.mu, and because a refresh holding s.mu must not block unrelated requests.
func (s *tokenSource) current() string {
	if s == nil {
		return ""
	}
	if t := s.token.Load(); t != nil {
		return *t
	}
	return ""
}

// refreshAfter401 calls the refresh callback after a request that sent token
// sent came back 401. When the token has changed since, another request, on
// this session or any other sharing the source, already refreshed it while this
// one waited, and retrying with the current token is enough: N concurrent 401s
// cost one refresh, not N (issue #193). The check compares tokens, so a refresh
// that returns the token it was given does not count as done, and the next
// waiter refreshes again.
//
// The callers hold their session's c.mu, so while a refresh is in flight every
// session of the client that got a 401 waits for it, up to the refresh
// request's own timeout. Sessions that got no 401 are not blocked.
func (s *tokenSource) refreshAfter401(sent string) error {
	if s == nil || s.refresh == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current() != sent {
		return nil
	}
	newToken, err := s.refresh(sent)
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}
	s.token.Store(&newToken)
	return nil
}
