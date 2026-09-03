package cas

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func newCachedSuite(t *testing.T) (*Store[testNote], *CachedStore[testNote]) {
	store, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return store, NewCachedStore(store)
}

func TestCachedObjectLazyLoad(t *testing.T) {
	ctx := context.Background()
	store, c := newCachedSuite(t)
	h, err := store.Put(ctx, testNote{Title: "lazy"})
	if err != nil {
		t.Fatal(err)
	}

	co, err := c.Proxy(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if co.IsLoaded() {
		t.Fatal("CachedObject must start unloaded")
	}
	obj, err := co.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !co.IsLoaded() {
		t.Fatal("Load must mark the object loaded")
	}
	if obj.Type() != "note@1" {
		t.Fatalf("Type() = %q", obj.Type())
	}

	// Memoization: Load again returns the same result without a store hit.
	// Delete the object underneath — the memoized value must survive.
	if err := store.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	again, err := co.Load(ctx)
	if err != nil {
		t.Fatalf("memoized Load must not re-read the store: %v", err)
	}
	if again != obj {
		t.Fatal("memoized value differs")
	}
}

func TestCachedStoreGet(t *testing.T) {
	ctx := context.Background()
	store, c := newCachedSuite(t)
	h, err := store.Put(ctx, testNote{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	obj, err := c.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Type() != "note@1" {
		t.Fatalf("Type() = %q", obj.Type())
	}
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err) // second read is a cache hit
	}
	st := c.CacheStats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("stats = %+v, want 1 hit 1 miss", st)
	}
	if st.HitRate != 0.5 {
		t.Fatalf("hit rate = %v, want 0.5", st.HitRate)
	}
	if st.Size != 1 {
		t.Fatalf("size = %d, want 1", st.Size)
	}
}

func TestCachedStoreMissingObject(t *testing.T) {
	ctx := context.Background()
	_, c := newCachedSuite(t)
	missing, _ := ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := c.Get(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}
	// Missing objects are not cached.
	if _, err := c.Get(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Get(missing) = %v", err)
	}
}

func TestCachedStorePreload(t *testing.T) {
	ctx := context.Background()
	store, c := newCachedSuite(t)
	var hashes []Hash
	for i := 0; i < 20; i++ {
		h, err := store.Put(ctx, testNote{Title: string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	if err := c.Preload(ctx, hashes); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 20 {
		t.Fatalf("after Preload size = %d, want 20", st.Size)
	}
}

func TestCachedStorePreloadRecursive(t *testing.T) {
	ctx := context.Background()
	ns, err := NewStore(NewMemoryRawStore(), JSONCodec[testNode]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	cn := NewCachedStore(ns)
	hc, err := ns.Put(ctx, testNode{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ns.Put(ctx, testNode{Name: "b", Refs: []Hash{hc}})
	if err != nil {
		t.Fatal(err)
	}
	ha, err := ns.Put(ctx, testNode{Name: "a", Refs: []Hash{hb}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cn.PreloadRecursive(ctx, ha, 2); err != nil {
		t.Fatal(err)
	}
	if st := cn.CacheStats(); st.Size != 3 {
		t.Fatalf("after recursive preload size = %d, want 3", st.Size)
	}
	// depth 0 loads only the root.
	cn.Clear()
	if err := cn.PreloadRecursive(ctx, ha, 0); err != nil {
		t.Fatal(err)
	}
	if st := cn.CacheStats(); st.Size != 1 {
		t.Fatalf("depth-0 preload size = %d, want 1", st.Size)
	}
}

func TestCachedStoreEvictClearWarmup(t *testing.T) {
	ctx := context.Background()
	store, c := newCachedSuite(t)
	h1, _ := store.Put(ctx, testNote{Title: "one"})
	h2, _ := store.Put(ctx, testNote{Title: "two"})

	if err := c.Warmup(ctx, []Hash{h1, h2}); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("after Warmup size = %d, want 2", st.Size)
	}
	c.Evict(h1)
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("after Evict size = %d, want 1", st.Size)
	}
	if st := c.CacheStats(); st.Evicts == 0 {
		t.Fatal("Evict must count an eviction")
	}
	c.Evict(h1) // evicting again is a no-op, no double count
	if st := c.CacheStats(); st.Evicts != 1 {
		t.Fatalf("Evicts = %d, want 1", st.Evicts)
	}
	c.Clear()
	if st := c.CacheStats(); st.Size != 0 {
		t.Fatalf("after Clear size = %d", st.Size)
	}
}

func TestLRUCache(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	lru, err := NewLRUCache(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []Hash
	for i := 0; i < 3; i++ {
		h, err := store.Put(ctx, testNote{Title: string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}

	// Fill beyond capacity: accessing a, b, c evicts a.
	for _, h := range hashes {
		if _, err := lru.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := lru.CacheStats(); st.Size != 2 {
		t.Fatalf("LRU size = %d, want 2", st.Size)
	}
	if st := lru.CacheStats(); st.Evicts != 1 {
		t.Fatalf("LRU evicts = %d, want 1", st.Evicts)
	}
	// 'a' was evicted — Get re-fetches (a miss), 'c' is a hit.
	if _, err := lru.Get(ctx, hashes[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := lru.Get(ctx, hashes[2]); err != nil {
		t.Fatal(err)
	}
	st := lru.CacheStats()
	if st.Misses != 4 || st.Hits != 1 {
		t.Fatalf("stats = %+v, want 4 misses 1 hit", st)
	}
}

func TestLRUCacheRejectsBadSize(t *testing.T) {
	store, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLRUCache(store, 0); err == nil {
		t.Fatal("maxSize 0 must be rejected")
	}
	if _, err := NewLRUCache(store, -1); err == nil {
		t.Fatal("negative maxSize must be rejected")
	}
}

func TestCachedStoreConcurrent(t *testing.T) {
	ctx := context.Background()
	store, c := newCachedSuite(t)
	var hashes []Hash
	for i := 0; i < 10; i++ {
		h, err := store.Put(ctx, testNote{Title: string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h := hashes[j%len(hashes)]
				if _, err := c.Get(ctx, h); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

// TestLRUCacheBoundViaAllAccessors guards the regression where Get,
// Preload and Warmup bypassed the LRU policy (they inserted into the
// embedded CachedStore without bookkeeping, so the bound never held).
func TestLRUCacheBoundViaAllAccessors(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	lru, err := NewLRUCache(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []Hash
	for i := 0; i < 5; i++ {
		h, err := store.Put(ctx, testNote{Title: string(rune('a' + i))})
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}

	// Get (previously bypassing the bound) keeps the cache ≤ maxSize.
	for _, h := range hashes {
		if _, err := lru.Get(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := lru.CacheStats(); st.Size > 2 {
		t.Fatalf("Get exceeded bound: size = %d, want <= 2", st.Size)
	}
	// Evicted entries reload correctly through Get.
	if _, err := lru.Get(ctx, hashes[0]); err != nil {
		t.Fatalf("reload after eviction: %v", err)
	}
	if st := lru.CacheStats(); st.Size > 2 {
		t.Fatalf("reload exceeded bound: size = %d, want <= 2", st.Size)
	}

	// Warmup inserts must respect the bound too.
	warm, err := NewLRUCache(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Warmup(ctx, hashes); err != nil {
		t.Fatal(err)
	}
	if st := warm.CacheStats(); st.Size > 2 {
		t.Fatalf("Warmup exceeded bound: size = %d, want <= 2", st.Size)
	}
}
