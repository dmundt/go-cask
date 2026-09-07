package cache_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/cache"
)

type testObject struct {
	Name string
	Refs []cas.Hash
}

func (o testObject) Type() string           { return "test@1" }
func (o testObject) References() []cas.Hash { return o.Refs }

func put(t *testing.T, s *cas.Store[testObject], name string, refs ...cas.Hash) cas.Hash {
	t.Helper()
	h, err := s.Put(context.Background(), testObject{Name: name, Refs: refs})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func newStore(t *testing.T) *cas.Store[testObject] {
	t.Helper()
	s, err := cas.NewStore(cas.NewMemoryRawStore(), cas.JSONCodec[testObject]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCachedObjectLazyLoad(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	h := put(t, s, "lazy")
	co, err := c.Proxy(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if co.IsLoaded() {
		t.Fatal("must start unloaded")
	}
	if _, err := co.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !co.IsLoaded() {
		t.Fatal("Load must mark loaded")
	}
	if err := s.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	// Delete under store - memoized Load survives.
	if _, err := co.Load(ctx); err != nil {
		t.Fatalf("memoized Load: %v", err)
	}
}

func TestCachedObjectConcurrentLoad(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	h := put(t, s, "concurrent")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			co, err := c.Proxy(ctx, h)
			if err != nil {
				t.Error(err)
				return
			}
			co.Load(ctx)
		}()
	}
	wg.Wait()
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("size = %d, want 1", st.Size)
	}
}

func TestCachedStoreGet(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	h := put(t, s, "alpha")
	obj, err := c.Get(ctx, h)
	if err != nil || obj.Name != "alpha" {
		t.Fatalf("Get = %+v, %v", obj, err)
	}
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	st := c.CacheStats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestCachedStoreMissingObject(t *testing.T) {
	ctx := context.Background()
	c := cache.NewCachedStore(newStore(t))
	m, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := c.Get(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}

func TestCachedStorePreload(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	var hs []cas.Hash
	for i := 0; i < 20; i++ {
		hs = append(hs, put(t, s, string(rune('a'+i))))
	}
	if err := c.Preload(ctx, hs); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 20 {
		t.Fatalf("size = %d", st.Size)
	}
}

func TestCachedStoreEvictClearWarmup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	h1 := put(t, s, "one")
	h2 := put(t, s, "two")
	if err := c.Warmup(ctx, []cas.Hash{h1, h2}); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 2 {
		t.Fatalf("Warmup size = %d", st.Size)
	}
	c.Evict(h1)
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("Evict size = %d", st.Size)
	}
	c.Evict(h1)
	if st := c.CacheStats(); st.Evicts != 1 {
		t.Fatalf("Evicts = %d", st.Evicts)
	}
	c.Clear()
	if st := c.CacheStats(); st.Size != 0 {
		t.Fatalf("Clear size = %d", st.Size)
	}
}

func TestCachedStoreConcurrent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	var hs []cas.Hash
	for i := 0; i < 10; i++ {
		hs = append(hs, put(t, s, string(rune('a'+i))))
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := c.Get(ctx, hs[j%len(hs)]); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

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

func TestSmartCache(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cs := cache.NewCachedStore(s)
	sc := cache.NewSmartCache(cs, 2)
	h := put(t, s, "root")
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "root" {
		t.Fatalf("got %q", obj.Name)
	}
}

func TestSmartCacheZeroDepth(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cs := cache.NewCachedStore(s)
	sc := cache.NewSmartCache(cs, 0)
	h := put(t, s, "only")
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "only" {
		t.Fatalf("got %q", obj.Name)
	}
}

func TestSmartCacheMissing(t *testing.T) {
	ctx := context.Background()
	cs := cache.NewCachedStore(newStore(t))
	sc := cache.NewSmartCache(cs, 2)
	m, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := sc.GetWithPrefetch(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}

func TestCachedStoreWarmupMissing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	h := put(t, s, "exists")
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err := c.Warmup(ctx, []cas.Hash{h, missing}); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("Warmup with missing size = %d, want 1", st.Size)
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
	lru.Proxy(ctx, h) // touch existing
	if st := lru.CacheStats(); st.Size != 1 {
		t.Fatalf("touch size = %d", st.Size)
	}
}

func TestCachedStoreStatsZero(t *testing.T) {
	c := cache.NewCachedStore(newStore(t))
	st := c.CacheStats()
	if st.Hits != 0 || st.Misses != 0 || st.Size != 0 {
		t.Fatalf("fresh cache stats = %+v", st)
	}
	if st.HitRate != 0 {
		t.Fatalf("fresh hit rate = %v, want 0", st.HitRate)
	}
}

func TestCachedStorePreloadEmpty(t *testing.T) {
	c := cache.NewCachedStore(newStore(t))
	if err := c.Preload(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCachedStoreEvictMissing(t *testing.T) {
	c := cache.NewCachedStore(newStore(t))
	h, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	c.Evict(h)
	if st := c.CacheStats(); st.Evicts != 0 {
		t.Fatalf("Evict on missing counted = %d", st.Evicts)
	}
}

func TestCachedStorePreloadMissError(t *testing.T) {
	ctx := context.Background()
	c := cache.NewCachedStore(newStore(t))
	h, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err := c.Preload(ctx, []cas.Hash{h}); err == nil {
		t.Fatal("Preload of missing should return error")
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
		t.Fatalf("LRU manual evicts = %d, want 1", st.Evicts)
	}
}

func TestCachedStorePreloadRecursiveDeepRead(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cache.NewCachedStore(s)
	h := put(t, s, "root")
	if err := c.PreloadRecursive(ctx, h, 1); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("depth-1 size = %d, want 1", st.Size)
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
	// Load twice to promote.
	lru.Get(ctx, hs[0])
	lru.Get(ctx, hs[1])
	lru.Get(ctx, hs[0]) // promote a back to front
	lru.Get(ctx, hs[2])
	if st := lru.CacheStats(); st.Size != 3 {
		t.Fatalf("promote size = %d", st.Size)
	}
}

func TestSmartCachePrefetchGoroutine(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cs := cache.NewCachedStore(s)
	sc := cache.NewSmartCache(cs, 1)
	h := put(t, s, "root")
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "root" {
		t.Fatalf("got %q", obj.Name)
	}
}
