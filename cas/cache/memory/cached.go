// Package memory provides a lazy-loading, in-memory cache for the cas core.
// CachedStore wraps a Store[T] with a sync.Map; CachedObject is a lazy proxy.
// LRU eviction lives in the sibling lru package.
package memory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dmundt/go-cask/cas"
)

// CacheMetrics are atomic counters tracking cache behavior.
type CacheMetrics struct {
	Hits   atomic.Uint64
	Misses atomic.Uint64
	Loads  atomic.Uint64
	Evicts atomic.Uint64
}

// CacheStats is a point-in-time snapshot of cache behavior.
type CacheStats struct {
	Hits    uint64
	Misses  uint64
	Loads   uint64
	Evicts  uint64
	HitRate float64
	Size    int
}

// CachedObject[T] is a lazy proxy for one hash: it loads the object from the
// underlying Store[T] exactly once (double-checked locking) and memoizes the
// result.
type CachedObject[T cas.Object[T]] struct {
	store  *cas.Store[T]
	hash   cas.Hash
	mu     sync.RWMutex
	obj    T
	err    error
	loaded bool
}

// Load returns the object, loading it from the underlying store on first
// access and memoizing the result.
func (c *CachedObject[T]) Load(ctx context.Context) (T, error) {
	c.mu.RLock()
	if c.loaded {
		obj, err := c.obj, c.err
		c.mu.RUnlock()
		return obj, err
	}
	c.mu.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.obj, c.err
	}
	obj, err := c.store.Get(ctx, c.hash)
	c.obj, c.err, c.loaded = obj, err, true
	return obj, err
}

// Hash returns the hash this object is memoized for.
func (c *CachedObject[T]) Hash() cas.Hash { return c.hash }

// IsLoaded reports whether the object has been loaded without triggering a load.
func (c *CachedObject[T]) IsLoaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}

// CachedStore[T] wraps a Store[T] with a sync.Map of CachedObject[T].
type CachedStore[T cas.Object[T]] struct {
	store   *cas.Store[T]
	cache   sync.Map
	metrics CacheMetrics
	onNew   func(key string)
}

// New wraps store in a lazy-loading cache.
func New[T cas.Object[T]](store *cas.Store[T]) *CachedStore[T] {
	return &CachedStore[T]{store: store}
}

// OnNew sets the callback called when a new key is added to the cache.
func (c *CachedStore[T]) OnNew(fn func(key string)) { c.onNew = fn }

// Lookup returns the cached object for key, or nil if absent.
func (c *CachedStore[T]) Lookup(key string) *CachedObject[T] {
	v, ok := c.cache.Load(key)
	if !ok {
		return nil
	}
	return v.(*CachedObject[T])
}

// EvictKey removes the entry for key from the map without counting metrics.
func (c *CachedStore[T]) EvictKey(key string) {
	c.cache.Delete(key)
}

// IncrEvicts adds 1 to the eviction counter.
func (c *CachedStore[T]) IncrEvicts() {
	c.metrics.Evicts.Add(1)
}

// Proxy returns the (possibly not-yet-loaded) CachedObject for h.
func (c *CachedStore[T]) Proxy(ctx context.Context, h cas.Hash) (*CachedObject[T], error) {
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
		return nil, fmt.Errorf("cache: %w: %s", cas.ErrNotFound, h)
	}
	co := &CachedObject[T]{store: c.store, hash: h}
	actual, loaded := c.cache.LoadOrStore(key, co)
	if !loaded && c.onNew != nil {
		c.onNew(key)
	}
	return actual.(*CachedObject[T]), nil
}

// Get returns the loaded object for h: Proxy + Load.
func (c *CachedStore[T]) Get(ctx context.Context, h cas.Hash) (T, error) {
	co, err := c.Proxy(ctx, h)
	if err != nil {
		var zero T
		return zero, err
	}
	return co.Load(ctx)
}

// Preload loads every hash in parallel.
func (c *CachedStore[T]) Preload(ctx context.Context, hashes []cas.Hash) error {
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
			if _, err := c.Get(ctx, h); err != nil {
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
// object it references.
func (c *CachedStore[T]) PreloadRecursive(ctx context.Context, h cas.Hash, depth int) error {
	obj, err := c.Get(ctx, h)
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

// Warmup preloads hashes into the cache; missing objects are tolerated.
func (c *CachedStore[T]) Warmup(ctx context.Context, hashes []cas.Hash) error {
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
			co, err := c.Proxy(ctx, h)
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
func (c *CachedStore[T]) Evict(h cas.Hash) {
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
