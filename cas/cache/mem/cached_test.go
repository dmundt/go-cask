package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	cachemem "github.com/dmundt/go-cask/cas/cache/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
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
	s, err := cas.New(mem.New(), jsoncodec.New[testObject](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCachedObjectLazyLoad(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
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
	if _, err := co.Load(ctx); err != nil {
		t.Fatalf("memoized Load: %v", err)
	}
}

func TestCachedObjectConcurrentLoad(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
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
		t.Fatalf("size = %d", st.Size)
	}
}

func TestCachedStoreGet(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
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
	c := cachemem.New(newStore(t))
	m, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := c.Get(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}

func TestCachedStorePreload(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
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

func TestCachedStorePreloadRecursiveDepth0(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "only")
	if err := c.PreloadRecursive(ctx, h, 0); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("depth-0 size = %d", st.Size)
	}
}

func TestCachedStorePreloadRecursiveDepth1(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "root")
	if err := c.PreloadRecursive(ctx, h, 1); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("depth-1 size = %d", st.Size)
	}
}

func TestCachedStorePreloadEmpty(t *testing.T) {
	c := cachemem.New(newStore(t))
	if err := c.Preload(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCachedStorePreloadMissError(t *testing.T) {
	ctx := context.Background()
	c := cachemem.New(newStore(t))
	h, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err := c.Preload(ctx, []cas.Hash{h}); err == nil {
		t.Fatal("Preload of missing must return error")
	}
}

func TestCachedStoreEvictClearWarmup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
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

func TestCachedStoreEvictMissing(t *testing.T) {
	c := cachemem.New(newStore(t))
	h, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	c.Evict(h)
	if st := c.CacheStats(); st.Evicts != 0 {
		t.Fatalf("Evict on missing = %d", st.Evicts)
	}
}

func TestCachedStoreWarmupMissing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "exists")
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err := c.Warmup(ctx, []cas.Hash{h, missing}); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 1 {
		t.Fatalf("Warmup with missing size = %d", st.Size)
	}
}

func TestCachedStoreConcurrent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
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

func TestCachedStoreStatsZero(t *testing.T) {
	c := cachemem.New(newStore(t))
	st := c.CacheStats()
	if st.Hits != 0 || st.Misses != 0 || st.Size != 0 {
		t.Fatalf("fresh cache stats = %+v", st)
	}
	if st.HitRate != 0 {
		t.Fatalf("fresh hit rate = %v", st.HitRate)
	}
}

func TestCachedObjectHash(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "hashed")
	co, err := c.Proxy(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if got := co.Hash(); got.String() != h.String() {
		t.Fatalf("Hash() = %v, want %v", got, h)
	}
}

func TestCachedStoreLookup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "lookup")
	key := h.String()

	if co := c.Lookup(key); co != nil {
		t.Fatalf("Lookup before Proxy must be nil, got %+v", co)
	}

	if _, err := c.Proxy(ctx, h); err != nil {
		t.Fatal(err)
	}
	co := c.Lookup(key)
	if co == nil {
		t.Fatal("Lookup after Proxy must return the cached object")
	}
	obj, err := co.Load(ctx)
	if err != nil || obj.Name != "lookup" {
		t.Fatalf("loaded via Lookup = %+v, %v", obj, err)
	}

	c.EvictKey(key)
	if co := c.Lookup(key); co != nil {
		t.Fatalf("Lookup after EvictKey must be nil, got %+v", co)
	}
}

func TestCachedStoreOnNew(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)

	var mu sync.Mutex
	var called []string
	c.OnNew(func(key string) {
		mu.Lock()
		called = append(called, key)
		mu.Unlock()
	})

	h := put(t, s, "one")
	key := h.String()
	if _, err := c.Proxy(ctx, h); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(called) != 1 || called[0] != key {
		t.Fatalf("OnNew calls = %v, want [%s]", called, key)
	}
	mu.Unlock()

	// A cache hit must not fire OnNew again.
	if _, err := c.Proxy(ctx, h); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(called) != 1 {
		t.Fatalf("OnNew fired on cache hit: %v", called)
	}
	mu.Unlock()

	// A different new key fires OnNew a second time.
	h2 := put(t, s, "two")
	if _, err := c.Proxy(ctx, h2); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(called) != 2 {
		t.Fatalf("OnNew calls = %v, want 2", called)
	}
	mu.Unlock()
}

func TestCachedStoreIncrEvictsAndEvictKey(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "evictme")
	key := h.String()

	if _, err := c.Proxy(ctx, h); err != nil {
		t.Fatal(err)
	}
	if st := c.CacheStats(); st.Size != 1 || st.Evicts != 0 {
		t.Fatalf("before evict stats = %+v", st)
	}

	c.IncrEvicts()
	c.IncrEvicts()
	c.EvictKey(key)

	st := c.CacheStats()
	if st.Evicts != 2 {
		t.Fatalf("Evicts = %d, want 2", st.Evicts)
	}
	if st.Size != 0 {
		t.Fatalf("Size = %d, want 0 after EvictKey", st.Size)
	}
}

func TestCachedObjectLoadErrorMemoized(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "doomed")

	co, err := c.Proxy(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	// Remove the object from the underlying store between Proxy (Exists ok)
	// and the first Load, so the first Load records a not-found error.
	if err := s.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := co.Load(ctx); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("first Load = %v, want ErrNotFound", err)
	}

	// Restore the object so a fresh fetch would now succeed; a memoized
	// error must still be returned without a second store hit.
	put(t, s, "doomed")
	if _, err := co.Load(ctx); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("second Load = %v, want memoized ErrNotFound", err)
	}
	if !co.IsLoaded() {
		t.Fatal("error load must still mark the object loaded")
	}
}

func TestCachedStoreFullStats(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := cachemem.New(s)
	h := put(t, s, "stats")

	// First Get: one miss (proxy) and a successful load.
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}
	// Second Get: a cache hit on the proxy; Load is memoized.
	if _, err := c.Get(ctx, h); err != nil {
		t.Fatal(err)
	}

	st := c.CacheStats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("Hits/Misses = %d/%d, want 1/1", st.Hits, st.Misses)
	}
	if st.Loads != 0 {
		t.Fatalf("Loads = %d, want 0", st.Loads)
	}
	if st.Evicts != 0 {
		t.Fatalf("Evicts = %d, want 0", st.Evicts)
	}
	if st.HitRate != 0.5 {
		t.Fatalf("HitRate = %v, want 0.5", st.HitRate)
	}
	if st.Size != 1 {
		t.Fatalf("Size = %d, want 1", st.Size)
	}

	// Add an eviction and confirm it is reflected in the snapshot.
	c.IncrEvicts()
	c.Evict(h)
	st = c.CacheStats()
	if st.Evicts != 2 {
		t.Fatalf("after evict Evicts = %d, want 2", st.Evicts)
	}
	if st.Size != 0 {
		t.Fatalf("after evict Size = %d, want 0", st.Size)
	}
}
