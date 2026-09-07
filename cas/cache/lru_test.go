package cache_test

import (
	"context"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/cache"
)

func TestLRUCache(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	lru, err := cache.NewLRUCache(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 3; i++ {
		hs = append(hs, put(t, s, string(rune('a'+i))))
	}
	for _, h := range hs {
		if _, err := lru.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := lru.CacheStats(); st.Size != 2 {
		t.Fatalf("size = %d", st.Size)
	}
	if st := lru.CacheStats(); st.Evicts != 1 {
		t.Fatalf("evicts = %d", st.Evicts)
	}
	lru.Get(ctx, hs[0])
	lru.Get(ctx, hs[2])
	st := lru.CacheStats()
	if st.Misses != 4 || st.Hits != 1 {
		t.Fatalf("misses=%d hits=%d", st.Misses, st.Hits)
	}
}

func TestLRUCacheRejectsBadSize(t *testing.T) {
	s := newStore(t)
	if _, err := cache.NewLRUCache(s, 0); err == nil {
		t.Fatal("maxSize 0 rejected")
	}
	if _, err := cache.NewLRUCache(s, -1); err == nil {
		t.Fatal("negative maxSize rejected")
	}
}

func TestLRUCacheBoundViaAllAccessors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	lru, err := cache.NewLRUCache(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 5; i++ {
		hs = append(hs, put(t, s, string(rune('a'+i))))
	}
	for _, h := range hs {
		if _, err := lru.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := lru.CacheStats(); st.Size > 2 {
		t.Fatalf("Get exceeded bound: size = %d", st.Size)
	}
	warm, err := cache.NewLRUCache(s, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Warmup(ctx, hs); err != nil {
		t.Fatal(err)
	}
	if st := warm.CacheStats(); st.Size > 2 {
		t.Fatalf("Warmup exceeded bound: size = %d", st.Size)
	}
}

func TestLRUCachePreloadRespectsBound(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	lru, err := cache.NewLRUCache(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 10; i++ {
		hs = append(hs, put(t, s, string(rune('a'+i))))
	}
	if err := lru.Preload(ctx, hs); err != nil {
		t.Fatal(err)
	}
	if st := lru.CacheStats(); st.Size > 3 {
		t.Fatalf("Preload exceeded bound: size = %d", st.Size)
	}
}

func TestLRUCacheTouch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	lru, err := cache.NewLRUCache(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	h := put(t, s, "x")
	lru.Get(ctx, h)
	lru.Proxy(ctx, h)
	if st := lru.CacheStats(); st.Size != 1 {
		t.Fatalf("touch size = %d", st.Size)
	}
}

func TestLRUCacheEvictCountAfterGet(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	lru, err := cache.NewLRUCache(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	h := put(t, s, "a")
	lru.Get(ctx, h)
	lru.Evict(h)
	if st := lru.CacheStats(); st.Evicts != 1 {
		t.Fatalf("Evicts = %d", st.Evicts)
	}
}

func TestLRUCachePromoteExisting(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	lru, err := cache.NewLRUCache(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	var hs []cas.Hash
	for i := 0; i < 3; i++ {
		hs = append(hs, put(t, s, string(rune('a'+i))))
	}
	lru.Get(ctx, hs[0])
	lru.Get(ctx, hs[1])
	lru.Get(ctx, hs[0])
	lru.Get(ctx, hs[2])
	if st := lru.CacheStats(); st.Size != 3 {
		t.Fatalf("promote size = %d", st.Size)
	}
}
