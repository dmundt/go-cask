// Package memory provides a lazy-loading, in-memory cache layer for the cas
// core. It is not part of the stable cas surface (cas-core §4.10): it wraps a
// typed *cas.Store[T] and is generic over the object type.
//
// CachedObject[T] is a lazy proxy for one object — Load uses double-checked
// locking, fetches from the store exactly once, and memoizes the value and any
// error; IsLoaded reports state without loading. CachedStore[T] wraps a
// Store[T] with a sync.Map of CachedObject values plus atomic metrics (exposed
// by CacheStats); build one with New(store). Preload and PreloadRecursive warm
// the cache ahead of use.
//
// The size-bounded lru.Cache (cas/cache/lru) builds on this package, and the
// prefetch recipe (cas/cache/prefetch) demonstrates warming it from references.
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
	Hits   atomic.Uint64 // Proxy found the object already cached
	Misses atomic.Uint64 // Proxy had to create a cache entry
	Loads  atomic.Uint64 // CachedObject.Load fetched the object from the store
	Evicts atomic.Uint64 // entries removed by a policy or by Evict
}

// CacheStats is a point-in-time snapshot of cache behavior.
type CacheStats struct {
	Hits    uint64  // Proxy hits
	Misses  uint64  // Proxy misses
	Loads   uint64  // store fetches by Load
	Evicts  uint64  // entries evicted
	HitRate float64 // Hits / (Hits + Misses), 0 when there was no access
	Size    int     // entries currently cached
}

// CachedObject[T] is a lazy proxy for one digest: it loads the object from the
// underlying Store[T] exactly once (double-checked locking) and memoizes the
// result.
type CachedObject[T cas.Object[T]] struct {
	store   *cas.Store[T]
	metrics *CacheMetrics
	digest  cas.Digest
	mu      sync.RWMutex
	obj     T
	err     error
	loaded  bool
}

// Load returns the object, loading it from the underlying store on first
// access and memoizing the result. The first Load for a digest records one
// CacheMetrics.Loads, whether or not the store fetch succeeds; later calls are
// served from the memoized value.
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
	obj, err := c.store.Get(ctx, c.digest)
	c.obj, c.err, c.loaded = obj, err, true
	if c.metrics != nil {
		c.metrics.Loads.Add(1)
	}
	return obj, err
}

// Digest returns the digest this object is memoized for.
func (c *CachedObject[T]) Digest() cas.Digest { return c.digest }

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

// OnNew sets the callback called when a new key is added to the cache. Set it
// during construction, before the cache is used concurrently.
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

// Proxy returns the (possibly not-yet-loaded) CachedObject for d.
func (c *CachedStore[T]) Proxy(ctx context.Context, d cas.Digest) (*CachedObject[T], error) {
	key := d.String()
	if v, ok := c.cache.Load(key); ok {
		c.metrics.Hits.Add(1)
		return v.(*CachedObject[T]), nil
	}
	c.metrics.Misses.Add(1)
	exists, err := c.store.Exists(ctx, d)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("cache: %w: %s", cas.ErrNotFound, d)
	}
	co := &CachedObject[T]{store: c.store, metrics: &c.metrics, digest: d}
	actual, loaded := c.cache.LoadOrStore(key, co)
	if !loaded && c.onNew != nil {
		c.onNew(key)
	}
	return actual.(*CachedObject[T]), nil
}

// Get returns the loaded object for d: Proxy + Load.
func (c *CachedStore[T]) Get(ctx context.Context, d cas.Digest) (T, error) {
	co, err := c.Proxy(ctx, d)
	if err != nil {
		var zero T
		return zero, err
	}
	return co.Load(ctx)
}

// Preload loads every digest in parallel.
func (c *CachedStore[T]) Preload(ctx context.Context, digests []cas.Digest) error {
	const workers = 8
	sem := make(chan struct{}, workers)
	errCh := make(chan error, len(digests))
	var wg sync.WaitGroup
	for _, d := range digests {
		d := d
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := c.Get(ctx, d); err != nil {
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

// PreloadRecursive loads the object at d and, to the given depth, every
// object it references.
func (c *CachedStore[T]) PreloadRecursive(ctx context.Context, d cas.Digest, depth int) error {
	obj, err := c.Get(ctx, d)
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

// Warmup preloads digests into the cache; missing objects are tolerated.
func (c *CachedStore[T]) Warmup(ctx context.Context, digests []cas.Digest) error {
	const workers = 8
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, d := range digests {
		d := d
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			co, err := c.Proxy(ctx, d)
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

// Evict removes the cached object for d, if present.
func (c *CachedStore[T]) Evict(d cas.Digest) {
	if _, ok := c.cache.LoadAndDelete(d.String()); ok {
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
