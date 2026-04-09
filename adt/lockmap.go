package adt

import (
	"context"
	"fmt"
	"sync"
)

// LockState holds the lock handle and ETag for a locked object.
type LockState struct {
	LockHandle string
	ETag       string
}

// LockMap is a thread-safe map tracking active locks per system:objectURI.
type LockMap struct {
	mu    sync.RWMutex
	locks map[string]LockState
}

// NewLockMap creates a new empty LockMap.
func NewLockMap() *LockMap {
	return &LockMap{locks: make(map[string]LockState)}
}

// Set stores or overwrites a lock entry.
func (m *LockMap) Set(key, lockHandle, etag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locks[key] = LockState{LockHandle: lockHandle, ETag: etag}
}

// Get retrieves a lock entry. Returns false if not found.
func (m *LockMap) Get(key string) (LockState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.locks[key]
	return s, ok
}

// UpdateETag updates only the ETag for an existing entry. No-op if key is missing.
func (m *LockMap) UpdateETag(key, etag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.locks[key]; ok {
		s.ETag = etag
		m.locks[key] = s
	}
}

// Delete removes a lock entry.
func (m *LockMap) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.locks, key)
}

// LockKey returns a system-qualified key for the lock map.
func LockKey(systemName, objectURI string) string {
	return systemName + ":" + objectURI
}

// ResolveLock returns a lock handle. Priority: explicitHandle > cached > auto-lock.
// If auto-locking succeeds, the lock is stored in the map.
func (m *LockMap) ResolveLock(ctx context.Context, locker LockClient, key, objectURI, explicitHandle string) (string, error) {
	if explicitHandle != "" {
		return explicitHandle, nil
	}
	if state, ok := m.Get(key); ok && state.LockHandle != "" {
		return state.LockHandle, nil
	}
	handle, err := locker.LockObject(ctx, objectURI)
	if err != nil {
		return "", fmt.Errorf("auto-lock %s: %w", objectURI, err)
	}
	m.Set(key, handle, "")
	return handle, nil
}

// ResolveETag returns a cached ETag or fetches one via GetSource.
//
// If GetSource fails (e.g. 400 for CLAS on S/4, 404 for DTEL/DOMA — these
// object types don't have /source/main), the function tries FetchETag as
// a fallback. FetchETag GETs the bare object URI with the type-appropriate
// Accept header, which works for ALL object types. The fallback is
// discovered via an interface assertion, so callers that pass a plain
// SourceClient (e.g. test mocks) don't need to implement it — the fallback
// is simply skipped. See adtler#9, adtler#14.
func (m *LockMap) ResolveETag(ctx context.Context, reader SourceClient, key, objectURI string) (string, error) {
	if state, ok := m.Get(key); ok && state.ETag != "" {
		return state.ETag, nil
	}
	result, err := reader.GetSource(ctx, objectURI)
	if err == nil {
		m.UpdateETag(key, result.ETag)
		return result.ETag, nil
	}
	// GetSource failed — try fetching the ETag from the bare object URI
	// if the reader also implements FetchETag (all concrete clients do;
	// test mocks may not, in which case this is skipped).
	type etagFetcher interface {
		FetchETag(ctx context.Context, objectURI string) (string, error)
	}
	if f, ok := reader.(etagFetcher); ok {
		etag, fetchErr := f.FetchETag(ctx, objectURI)
		if fetchErr == nil && etag != "" {
			m.UpdateETag(key, etag)
			return etag, nil
		}
	}
	return "", fmt.Errorf("fetch ETag for %s: %w", objectURI, err)
}
