package adt

import (
	"context"
	"errors"
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
	refresh func(string) (string, error)

	mu       sync.Mutex   // guards inflight; never held across the refresh call or while taking a c.mu
	inflight *refreshCall // the refresh currently running, nil when none is
}

// refreshCall is one run of the refresh callback. Requests that get a 401
// while it runs wait for it and share its outcome.
type refreshCall struct {
	done chan struct{} // closed when the call has finished and err is set
	err  error
}

// errRefreshAborted is what the requests waiting on a refresh get when the
// refresh callback panicked instead of returning.
var errRefreshAborted = errors.New("token refresh aborted")

func newTokenSource(token string, refresh func(string) (string, error)) *tokenSource {
	s := &tokenSource{refresh: refresh}
	s.token.Store(&token)
	return s
}

// current returns the access token, or "" for Basic Auth.
//
// The token is atomic rather than guarded by a mutex because setAuth reads it
// for every request, including fetchCSRFToken's, which already runs under
// c.mu, and because a running refresh must not block unrelated requests.
func (s *tokenSource) current() string {
	if s == nil {
		return ""
	}
	if t := s.token.Load(); t != nil {
		return *t
	}
	return ""
}

// refreshAfter401 refreshes the token after a request that sent token sent came
// back 401, so the caller can retry with the token current() then returns.
//
// One refresh serves every request rejected with the same token, on this
// session or any other sharing the source (issues #193, #197):
//
//   - If the token has changed since the request sent it, another request
//     already refreshed it, and retrying with the current token is enough.
//   - If a refresh is running, the request waits for it, or for ctx, and gets
//     its outcome,
//     including its error. A failed refresh is therefore not repeated by every
//     request that queued behind it, which with an identity provider that
//     rotates refresh tokens would only spend more attempts on a refresh token
//     that has just failed.
//   - Otherwise the request runs the refresh itself.
//
// A failure is not remembered: the next 401 after the failed call has finished
// starts a fresh attempt, so a transient error does not stick. The check
// compares tokens, so a refresh that returns the token it was given does not
// count as done either.
//
// Call it without holding c.mu. Requests on one session would otherwise reach
// it one at a time, after the refresh they could have joined had finished.
func (s *tokenSource) refreshAfter401(ctx context.Context, sent string) error {
	if s == nil || s.refresh == nil {
		return nil
	}
	s.mu.Lock()
	if s.current() != sent {
		s.mu.Unlock()
		return nil
	}
	// The token only changes when a refresh finishes, so a running refresh
	// was started for this same token.
	if call := s.inflight; call != nil {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{}), err: errRefreshAborted}
	s.inflight = call
	s.mu.Unlock()

	// Finish the call in a defer, so that a panicking callback still clears
	// inflight and releases the waiters (with errRefreshAborted). Otherwise
	// every later 401 with this token would wait forever.
	var newToken string
	defer func() {
		s.mu.Lock()
		if call.err == nil {
			s.token.Store(&newToken)
		}
		s.inflight = nil
		s.mu.Unlock()
		close(call.done)
	}()
	t, err := s.refresh(sent)
	if err != nil {
		call.err = fmt.Errorf("token refresh failed: %w", err)
		return call.err
	}
	newToken, call.err = t, nil
	return nil
}
