package falcon

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// pageStore holds the overflow records of a search in memory so that later
// pages can be served without re-querying Falcon.
//
// This deliberately keeps cross-call state in process memory, which the global
// stateless-design guideline normally forbids. The risk is bounded by scoping
// the store to a single ToolSet instance (never a package global), guarding it
// with a mutex, and evicting entries by both an LRU count cap and a TTL. The
// ToolSet is therefore expected to be used per agent session, not shared as a
// global singleton across independent concurrent agents.
type pageStore struct {
	mu      sync.Mutex
	entries map[string]*pageEntry
	order   []string // page tokens in insertion order; order[0] is the oldest (LRU victim)
	max     int
	ttl     time.Duration
	now     func() time.Time // injectable clock for tests
}

// pageEntry is the not-yet-returned remainder of one search's records.
type pageEntry struct {
	records   []any
	createdAt time.Time
}

// newPageStore creates a pageStore bounded to max entries with the given TTL.
func newPageStore(max int, ttl time.Duration) *pageStore {
	return &pageStore{
		entries: make(map[string]*pageEntry),
		max:     max,
		ttl:     ttl,
		now:     time.Now,
	}
}

// put stores the remaining records under a fresh opaque token and returns it.
// Callers must only call put when len(records) > 0.
func (s *pageStore) put(records []any) (string, error) {
	token, err := newPageToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneExpiredLocked()
	s.entries[token] = &pageEntry{records: records, createdAt: s.now()}
	s.order = append(s.order, token)
	s.evictLocked()

	return token, nil
}

// take returns up to n records for the given token and keeps the rest for the
// next call. The returned remaining count is how many records are still held
// after this call; when it reaches zero the entry is dropped. ok is false when
// the token is unknown or expired.
func (s *pageStore) take(token string, n int) (page []any, remaining int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneExpiredLocked()

	e, exists := s.entries[token]
	if !exists {
		return nil, 0, false
	}

	if n > len(e.records) {
		n = len(e.records)
	}
	page = e.records[:n]
	e.records = e.records[n:]
	remaining = len(e.records)
	if remaining == 0 {
		s.removeLocked(token)
	}

	return page, remaining, true
}

// pruneExpiredLocked drops entries older than the TTL. The caller must hold mu.
func (s *pageStore) pruneExpiredLocked() {
	if s.ttl <= 0 || len(s.order) == 0 {
		return
	}
	cutoff := s.now().Add(-s.ttl)
	kept := make([]string, 0, len(s.order))
	for _, tok := range s.order {
		e, ok := s.entries[tok]
		if !ok {
			continue
		}
		if e.createdAt.Before(cutoff) {
			delete(s.entries, tok)
			continue
		}
		kept = append(kept, tok)
	}
	s.order = kept
}

// evictLocked drops the oldest entries until the count is within max. The
// caller must hold mu.
func (s *pageStore) evictLocked() {
	for len(s.order) > s.max {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
}

// removeLocked deletes one token from both the map and the order slice. The
// caller must hold mu.
func (s *pageStore) removeLocked(token string) {
	delete(s.entries, token)
	for i, tok := range s.order {
		if tok == token {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// newPageToken returns an unguessable opaque token. The "v1." prefix lets the
// format evolve without colliding with previously issued tokens.
func newPageToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", goerr.Wrap(err, "failed to generate page token")
	}
	return "v1." + hex.EncodeToString(b), nil
}
