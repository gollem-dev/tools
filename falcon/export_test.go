package falcon

import "time"

// SafeClose exposes the unexported safeClose for testing.
var SafeClose = safeClose

// ClampLimit exposes the unexported clampLimit for testing.
var ClampLimit = clampLimit

// ClampIDs exposes the unexported clampIDs for testing.
var ClampIDs = clampIDs

// SplitAndTrim exposes the unexported splitAndTrim for testing.
var SplitAndTrim = splitAndTrim

// TestPageStore is a thin handle exposing the unexported pageStore so the
// external test package can exercise put/take, LRU eviction, and TTL pruning.
type TestPageStore struct {
	s *pageStore
}

// NewTestPageStore builds a pageStore with an injectable clock for TTL tests. A
// nil now keeps the real clock.
func NewTestPageStore(max int, ttl time.Duration, now func() time.Time) *TestPageStore {
	s := newPageStore(max, ttl)
	if now != nil {
		s.now = now
	}
	return &TestPageStore{s: s}
}

func (h *TestPageStore) Put(records []any) (string, error)           { return h.s.put(records) }
func (h *TestPageStore) Take(token string, n int) ([]any, int, bool) { return h.s.take(token, n) }

// Len returns the number of live entries held in the store.
func (h *TestPageStore) Len() int { return len(h.s.entries) }
