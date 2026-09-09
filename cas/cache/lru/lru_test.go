package lru_test

import (
	"context"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/cache/lru"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type item struct {
	ID string
}

func (item) Type() string           { return "item@1" }
func (item) References() []cas.Hash { return nil }

func newStore(t *testing.T) *cas.Store[item] {
	t.Helper()
	s, err := cas.New(mem.New(), jsoncodec.New[item](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func putItem(t *testing.T, s *cas.Store[item], id string) cas.Hash {
	t.Helper()
	h, err := s.Put(context.Background(), item{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCache(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 3; i++ {
		hs = append(hs, putItem(t, s, string(rune('a'+i))))
	}
	for _, h := range hs {
		if _, err := c.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("size = %d, want 2", st.Size)
	}
	if st := c.CacheStats(); st.Evicts != 1 {
		t.Fatalf("evicts = %d, want 1", st.Evicts)
	}
}

func TestNewRejectsBadSize(t *testing.T) {
	s := newStore(t)
	if _, err := lru.New(s, 0); err == nil {
		t.Fatal("maxSize 0 must be rejected")
	}
	if _, err := lru.New(s, -1); err == nil {
		t.Fatal("negative maxSize must be rejected")
	}
}

func TestCacheBoundViaAllAccessors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 5; i++ {
		hs = append(hs, putItem(t, s, string(rune('a'+i))))
	}
	for _, h := range hs {
		if _, err := c.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.CacheStats(); st.Size > 2 {
		t.Fatalf("Get exceeded bound: size = %d", st.Size)
	}
}

func TestGetMissingReturnsError(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	// A valid hash whose content was never stored must surface an error from
	// Proxy (and Get) rather than panicking.
	ghost, err := cas.HashBytes("sha256", []byte("never-stored-object"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, ghost); err == nil {
		t.Fatal("Get on an absent object must error")
	}
	if _, err := c.Proxy(ctx, ghost); err == nil {
		t.Fatal("Proxy on an absent object must error")
	}
	// A failed lookup must not cache anything.
	if st := c.CacheStats(); st.Size != 0 || st.Evicts != 0 {
		t.Fatalf("failed lookups must not affect cache: %+v", st)
	}
}

func TestCachePromotesOldKeyAboveNewerOnes(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	hA := putItem(t, s, "a")
	hB := putItem(t, s, "b")
	hC := putItem(t, s, "c")

	// Load a and b (a is oldest, b is MRU).
	if _, err := c.Get(ctx, hA); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, hB); err != nil {
		t.Fatal(err)
	}
	// Re-accessing the oldest key a promotes it above b.
	if _, err := c.Get(ctx, hA); err != nil {
		t.Fatal(err)
	}
	// Adding c evicts the LRU entry, which is now b (not a).
	if _, err := c.Get(ctx, hC); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("size = %d, want 2", st.Size)
	}
	if c.Lookup(hA.String()) == nil {
		t.Fatal("promoted key a should still be cached")
	}
	if c.Lookup(hC.String()) == nil {
		t.Fatal("newest key c should still be cached")
	}
	if c.Lookup(hB.String()) != nil {
		t.Fatal("LRU key b should have been evicted after promotion of a")
	}
}

func TestRepeatedGetOfMRUSurvivesEvictions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	hA := putItem(t, s, "a")
	hB := putItem(t, s, "b")
	hC := putItem(t, s, "c")
	hD := putItem(t, s, "d")

	for _, h := range []cas.Hash{hA, hB, hC} {
		if _, err := c.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	// Repeatedly access a so it is always most-recently used.
	for i := 0; i < 5; i++ {
		if _, err := c.Get(ctx, hA); err != nil {
			t.Fatal(err)
		}
	}
	// Adding d forces an eviction of the LRU key; because a is pinned at the
	// MRU position it must survive, and a + c remain with d.
	if _, err := c.Get(ctx, hD); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 3 {
		t.Fatalf("size = %d, want 3", st.Size)
	}
	if c.Lookup(hA.String()) == nil {
		t.Fatal("repeatedly accessed key a should not be evicted")
	}
	if c.Lookup(hC.String()) == nil {
		t.Fatal("newest key c should not be evicted")
	}
	if c.Lookup(hD.String()) == nil {
		t.Fatal("most recent key d should not be evicted")
	}
	if c.Lookup(hB.String()) != nil {
		t.Fatal("LRU key b should have been evicted")
	}
}

func TestCacheBoundViaWarmupPreload(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c, err := lru.New(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 5; i++ {
		hs = append(hs, putItem(t, s, string(rune('a'+i))))
	}

	if err := c.Warmup(ctx, hs); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 2 || st.Evicts == 0 {
		t.Fatalf("Warmup exceeded bound: %+v", st)
	}

	if err := c.Preload(ctx, hs); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("Preload exceeded bound: %+v", st)
	}

	// PreloadRecursive on an item with no references still goes through the
	// LRU note/eviction path for each cached key.
	for _, h := range hs {
		if err := c.PreloadRecursive(ctx, h, 1); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("PreloadRecursive exceeded bound: %+v", st)
	}
}
