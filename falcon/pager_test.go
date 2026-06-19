package falcon_test

import (
	"testing"
	"time"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

func TestPageStorePutTake(t *testing.T) {
	ps := falcon.NewTestPageStore(8, time.Minute, nil)

	token := gt.R1(ps.Put([]any{"a", "b", "c"})).NoError(t)
	gt.String(t, token).NotEqual("")

	page, remaining, ok := ps.Take(token, 2)
	gt.True(t, ok)
	gt.Array(t, page).Equal([]any{"a", "b"})
	gt.Number(t, remaining).Equal(1)

	page, remaining, ok = ps.Take(token, 10)
	gt.True(t, ok)
	gt.Array(t, page).Equal([]any{"c"})
	gt.Number(t, remaining).Equal(0)

	// Exhausted entry is dropped: the token is no longer valid.
	_, _, ok = ps.Take(token, 1)
	gt.False(t, ok)
	gt.Number(t, ps.Len()).Equal(0)
}

func TestPageStoreUnknownToken(t *testing.T) {
	ps := falcon.NewTestPageStore(8, time.Minute, nil)
	_, _, ok := ps.Take("v1.deadbeef", 1)
	gt.False(t, ok)
}

func TestPageStoreLRUEviction(t *testing.T) {
	ps := falcon.NewTestPageStore(2, time.Minute, nil)

	t1 := gt.R1(ps.Put([]any{"1"})).NoError(t)
	t2 := gt.R1(ps.Put([]any{"2"})).NoError(t)
	t3 := gt.R1(ps.Put([]any{"3"})).NoError(t)

	// Oldest (t1) is evicted once the third entry pushes past max=2.
	gt.Number(t, ps.Len()).Equal(2)
	_, _, ok := ps.Take(t1, 1)
	gt.False(t, ok)

	_, _, ok = ps.Take(t2, 1)
	gt.True(t, ok)
	_, _, ok = ps.Take(t3, 1)
	gt.True(t, ok)
}

func TestPageStoreTTLExpiry(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	ps := falcon.NewTestPageStore(8, 10*time.Minute, clock)

	token := gt.R1(ps.Put([]any{"x"})).NoError(t)

	// Advance the clock beyond the TTL; the entry must be pruned on access.
	now = now.Add(11 * time.Minute)
	_, _, ok := ps.Take(token, 1)
	gt.False(t, ok)
	gt.Number(t, ps.Len()).Equal(0)
}
