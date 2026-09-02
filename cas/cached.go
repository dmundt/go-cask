package cas

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// CacheMetrics are atomic counters tracking cache behavior: hits, misses,
// loads (objects fetched from the underlying store) and evictions.
type CacheMetrics struct {
	Hits   atomic.Uint64
	Misses atomic.Uint64
	Loads  atomic.Uint64
	Evicts atomic.Uint64
}

// CacheStats is a point-in-time snapshot of cache behavior, as returned by
// CachedStore.CacheStats (consumed by cache-observability tooling such as the
// artifacts example monitor).
type CacheStats struct {
	Hits    uint64
	Misses  uint64
	Loads   uint64
	Evicts  uint64
	HitRate float64 // Hits / (Hits + Misses); 0 when no lookups happened
	Size    int     // number of cached objects
}

// CachedObject[T] is a lazy proxy for one hash: it loads the object from the
// underlying Store[T] exactly once (double-checked locking) and memoizes the
// result — object AND error — for every later Load. IsLoaded reports state
// without loading.
type CachedObject[T Object[T]] struct {
	store  *Store[T]
	hash   Hash
	mu     sync.RWMutex
	obj    Object[T]
	err    error
	loaded bool
}

// Load returns the object, loading it from the underlying store on first
// access and memoizing the result (including errors) for later calls.
func (c *CachedObject[T]) Load(ctx context.Context) (Object[T], error) {
	c.mu.RLock()
	if c.loaded {
		obj, err := c.obj, c.err
		c.mu.RUnlock()
		return obj, err
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded { // double-checked locking
		return c.obj, c.err
	}
	obj, err := c.store.Get(ctx, c.hash)
	c.obj, c.err, c.loaded = obj, err, true
	return obj, err
}

// IsLoaded reports whether the object has been loaded (successfully or not)
// without triggering a load.
func (c *CachedObject[T]) IsLoaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}

// CachedStore[T] wraps a Store[T] with a sync.Map of CachedObject[T] keyed by
// h.String(). Get returns a not-yet-loaded reference (verifying existence
// first); GetTyped loads it. Preload loads many objects in parallel.
type CachedStore[T Object[T]] struct {
	store   *Store[T]
	cache   sync.Map // string → *CachedObject[T]
	metrics CacheMetrics
	onNew   func(key string) // policy hook: called once per newly cached key
}

// NewCachedStore wraps store in a lazy-loading cache.
func NewCachedStore[T Object[T]](store *Store[T]) *CachedStore[T] {
	return &CachedStore[T]{store: store}
}

// Get returns the (possibly not-yet-loaded) cached object for h. It verifies
// existence first: a missing object returns ErrNotFound and is not cached.
func (c *CachedStore[T]) Get(ctx context.Context, h Hash) (*CachedObject[T], error) {
	key := h.String()
	if v, ok := c.cache.Load(key); ok {
		c.metrics.Hits.Add(1)
		return v.(*CachedObject[T]), nil
	}
	c.metrics.Misses.Add(1)
	exists, err := c.store.Exists(ctx, h)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("cas: %w: %s", ErrNotFound, h)
	}
	co := &CachedObject[T]{store: c.store, hash: h}
	actual, loaded := c.cache.LoadOrStore(key, co)
	if !loaded && c.onNew != nil {
		c.onNew(key)
	}
	return actual.(*CachedObject[T]), nil
}

// GetTyped returns the loaded object for h: Get + Load. A missing object
// returns ErrNotFound.
func (c *CachedStore[T]) GetTyped(ctx context.Context, h Hash) (Object[T], error) {
	co, err := c.Get(ctx, h)
	if err != nil {
		return nil, err
	}
	return co.Load(ctx)
}

// Preload loads every hash in parallel (bounded worker goroutines) so
// subsequent GetTyped calls hit the cache. It returns the first error
// encountered; the other loads still complete.
func (c *CachedStore[T]) Preload(ctx context.Context, hashes []Hash) error {
	const workers = 8
	sem := make(chan struct{}, workers)
	errCh := make(chan error, len(hashes))
	var wg sync.WaitGroup
	for _, h := range hashes {
		h := h
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := c.GetTyped(ctx, h); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// PreloadRecursive loads the object at h and, to the given depth, every
// object it references. depth <= 0 loads only h. A missing object returns
// ErrNotFound.
func (c *CachedStore[T]) PreloadRecursive(ctx context.Context, h Hash, depth int) error {
	obj, err := c.GetTyped(ctx, h)
	if err != nil {
		return err
	}
	if depth <= 0 {
		return nil
	}
	for _, ref := range obj.References() {
		if err := c.PreloadRecursive(ctx, ref, depth-1); err != nil {
			return err
		}
	}
	return nil
}

// Warmup preloads hashes into the cache; missing objects are tolerated (they
// are simply not cached). Use it to populate a cache without failing on
// stale references.
func (c *CachedStore[T]) Warmup(ctx context.Context, hashes []Hash) error {
	const workers = 8
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, h := range hashes {
		h := h
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			co, err := c.Get(ctx, h)
			if err != nil {
				return
			}
			co.Load(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// CacheStats returns a snapshot of the cache metrics and current size.
func (c *CachedStore[T]) CacheStats() CacheStats {
	hits := c.metrics.Hits.Load()
	misses := c.metrics.Misses.Load()
	rate := 0.0
	if hits+misses > 0 {
		rate = float64(hits) / float64(hits+misses)
	}
	size := 0
	c.cache.Range(func(_, _ any) bool { size++; return true })
	return CacheStats{
		Hits:    hits,
		Misses:  misses,
		Loads:   c.metrics.Loads.Load(),
		Evicts:  c.metrics.Evicts.Load(),
		HitRate: rate,
		Size:    size,
	}
}

// Evict removes the cached object for h, if present.
func (c *CachedStore[T]) Evict(h Hash) {
	if _, ok := c.cache.LoadAndDelete(h.String()); ok {
		c.metrics.Evicts.Add(1)
	}
}

// Clear removes every cached object.
func (c *CachedStore[T]) Clear() {
	c.cache.Range(func(k, _ any) bool {
		c.cache.Delete(k)
		return true
	})
}

// evictKey removes the entry for key from the map without metrics (internal;
// used by LRUCache to keep the map and the LRU index consistent).
func (c *CachedStore[T]) evictKey(key string) {
	c.cache.Delete(key)
}
