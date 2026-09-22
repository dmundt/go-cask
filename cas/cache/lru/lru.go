// Package lru provides a size-bounded LRU cache for the cas core. It is not
// part of the stable cas surface (cas-core §4.10); Cache[T] wraps a
// memory.CachedStore[T] and adds a most-recently-used eviction policy over the
// cached objects.
//
// New(store, maxSize) builds a cache and rejects maxSize <= 0. The access path
// tracks and promotes entries on use, evicting the least-recently-used object
// when the cache is at capacity. Clear/Evict/EvictKey also drop the LRU
// bookkeeping, so a cleared entry stops occupying a slot and becomes eligible
// for garbage collection.
//
// Cache's surface is deliberately its own: Get, Proxy, Lookup, CacheStats,
// Preload, PreloadRecursive, Warmup, Clear, Evict, and EvictKey. The wrapped
// store is reachable through the CachedStore method for observers, but the
// store's construction hook (OnNew) and metric bump (IncrEvicts) stay internal
// to the eviction policy that owns them. Preload and PreloadRecursive report
// failures joined (errors.Join); Warmup tolerates missing objects and joins the
// rest.
package lru

import (
	"container/list"
	"context"
	"sync"

	"github.com/dmundt/go-cask/cas"
	cachepkg "github.com/dmundt/go-cask/cas/cache"
	"github.com/dmundt/go-cask/cas/cache/mem"
)

// Cache[T] is a size-bounded cache with LRU eviction: it owns a
// memory.CachedStore[T] (lazy CachedObject[T] semantics) and adds a
// most-recently-used eviction policy with a maximum number of entries.
//
// The wrapped store is an unexported named field, not an embedded one, so
// Cache's method set is exactly the API it means to own: the warmup, read and
// eviction methods below, each of which keeps the recency bookkeeping in step
// with the map. Reading the wrapped store directly bypasses that bookkeeping,
// which is why observers reach it deliberately through CachedStore().
type Cache[T cas.Object[T]] struct {
	cached  *memory.CachedStore[T]
	mu      sync.Mutex
	maxSize int
	list    *list.List
	index   map[string]*list.Element
}

// New wraps store in a size-bounded cache. maxSize must be > 0.
func New[T cas.Object[T]](store *cas.Store[T], maxSize int) (*Cache[T], error) {
	if err := cachepkg.ValidateMaxSize(maxSize, "cache/lru"); err != nil {
		return nil, err
	}
	cs := memory.New(store)
	c := &Cache[T]{
		cached:  cs,
		maxSize: maxSize,
		list:    list.New(),
		index:   make(map[string]*list.Element),
	}
	cs.OnNew(c.note)
	return c, nil
}

// CachedStore returns the wrapped lazy-loading store. It exists for observers
// that need the underlying value — a metrics monitor reading CacheStats, or a
// Lookup of a key — not as the cache's data path: reading through it records no
// use and enforces no bound. Use Get or Proxy for that.
func (c *Cache[T]) CachedStore() *memory.CachedStore[T] { return c.cached }

func (c *Cache[T]) note(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touchLocked(key, nil)
	for c.list.Len() > c.maxSize {
		last := c.list.Back()
		if last == nil {
			break
		}
		c.list.Remove(last)
		evicted := last.Value.(*memory.CachedObject[T])
		evictedKey := evicted.Digest().String()
		delete(c.index, evictedKey)
		// note holds c.mu, so use the wrapped (unlocked) removal.
		c.cached.EvictKey(evictedKey)
		c.cached.IncrEvicts()
	}
}

// forget drops the LRU bookkeeping for key, if present.
func (c *Cache[T]) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.list.Remove(el)
		delete(c.index, key)
	}
}

// Evict removes the cached object for d together with its LRU bookkeeping.
func (c *Cache[T]) Evict(d cas.Digest) {
	c.cached.Evict(d)
	c.forget(d.String())
}

// EvictKey removes the entry for key together with its LRU bookkeeping,
// without counting eviction metrics.
func (c *Cache[T]) EvictKey(key string) {
	c.cached.EvictKey(key)
	c.forget(key)
}

// Clear removes every cached object and resets the LRU bookkeeping, so the
// evicted objects are no longer retained by the recency list.
func (c *Cache[T]) Clear() {
	c.cached.Clear()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list.Init()
	c.index = make(map[string]*list.Element)
}

func (c *Cache[T]) touchLocked(key string, co *memory.CachedObject[T]) {
	if el, ok := c.index[key]; ok {
		c.list.MoveToFront(el)
		return
	}
	if co == nil {
		co = c.Lookup(key)
	}
	if co == nil {
		return
	}
	c.index[key] = c.list.PushFront(co)
}

// Lookup returns the cached object for key, or nil if absent. It does not
// promote the entry, so it neither records a use nor changes what the next
// eviction picks; use Proxy or Get to record a use.
func (c *Cache[T]) Lookup(key string) *memory.CachedObject[T] {
	return c.cached.Lookup(key)
}

// CacheStats returns a snapshot of the cache counters and the current number of
// cached entries; the eviction policy keeps that size at or below maxSize.
func (c *Cache[T]) CacheStats() memory.CacheStats {
	return c.cached.CacheStats()
}

// Preload loads every digest in the slice through the cache, so each inserted
// entry is promoted and the size bound is enforced as it grows; the returned
// error is errors.Join of the digests that failed to load.
func (c *Cache[T]) Preload(ctx context.Context, digests []cas.Digest) error {
	return c.cached.Preload(ctx, digests)
}

// PreloadRecursive loads the object at d and, to the given depth, every object
// it references, enforcing the size bound as each entry is inserted.
func (c *Cache[T]) PreloadRecursive(ctx context.Context, d cas.Digest, depth int) error {
	return c.cached.PreloadRecursive(ctx, d, depth)
}

// Warmup preloads digests into the cache, tolerating missing objects, and
// enforces the size bound as each entry is inserted.
func (c *Cache[T]) Warmup(ctx context.Context, digests []cas.Digest) error {
	return c.cached.Warmup(ctx, digests)
}

// Proxy returns the (possibly not-yet-loaded) CachedObject for d, promoting
// it to the most-recent position.
func (c *Cache[T]) Proxy(ctx context.Context, d cas.Digest) (*memory.CachedObject[T], error) {
	co, err := c.cached.Proxy(ctx, d)
	if err != nil {
		return nil, err
	}
	key := d.String()
	c.mu.Lock()
	c.touchLocked(key, co)
	c.mu.Unlock()
	return co, nil
}

// Get returns the loaded object, routing through Proxy so existing entries
// are promoted on every access.
func (c *Cache[T]) Get(ctx context.Context, d cas.Digest) (T, error) {
	co, err := c.Proxy(ctx, d)
	if err != nil {
		var zero T
		return zero, err
	}
	return co.Load(ctx)
}
