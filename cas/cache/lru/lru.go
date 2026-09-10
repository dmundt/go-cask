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
package lru

import (
	"container/list"
	"context"
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/cache/mem"
)

// Cache[T] is a size-bounded cache with LRU eviction: it embeds
// memory.CachedStore[T] (lazy CachedObject[T] semantics) and adds an LRU
// eviction policy with a maximum number of entries.
type Cache[T cas.Object[T]] struct {
	*memory.CachedStore[T]
	mu      sync.Mutex
	maxSize int
	list    *list.List
	index   map[string]*list.Element
}

// New wraps store in a size-bounded cache. maxSize must be > 0.
func New[T cas.Object[T]](store *cas.Store[T], maxSize int) (*Cache[T], error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("cache: LRU maxSize must be > 0, got %d", maxSize)
	}
	cs := memory.New(store)
	c := &Cache[T]{
		CachedStore: cs,
		maxSize:     maxSize,
		list:        list.New(),
		index:       make(map[string]*list.Element),
	}
	cs.OnNew(c.note)
	return c, nil
}

func (c *Cache[T]) note(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touchLocked(key)
	for c.list.Len() > c.maxSize {
		last := c.list.Back()
		if last == nil {
			break
		}
		c.list.Remove(last)
		evicted := last.Value.(*memory.CachedObject[T])
		evictedKey := evicted.Hash().String()
		delete(c.index, evictedKey)
		// note holds c.mu, so use the embedded (unlocked) removal.
		c.CachedStore.EvictKey(evictedKey)
		c.IncrEvicts()
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

// Evict removes the cached object for h together with its LRU bookkeeping.
func (c *Cache[T]) Evict(h cas.Hash) {
	c.CachedStore.Evict(h)
	c.forget(h.String())
}

// EvictKey removes the entry for key together with its LRU bookkeeping,
// without counting eviction metrics.
func (c *Cache[T]) EvictKey(key string) {
	c.CachedStore.EvictKey(key)
	c.forget(key)
}

// Clear removes every cached object and resets the LRU bookkeeping, so the
// evicted objects are no longer retained by the recency list.
func (c *Cache[T]) Clear() {
	c.CachedStore.Clear()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list.Init()
	c.index = make(map[string]*list.Element)
}

func (c *Cache[T]) touchLocked(key string) {
	if el, ok := c.index[key]; ok {
		c.list.MoveToFront(el)
		return
	}
	co := c.Lookup(key)
	if co == nil {
		return
	}
	c.index[key] = c.list.PushFront(co)
}

// Proxy returns the (possibly not-yet-loaded) CachedObject for h, promoting
// it to the most-recent position.
func (c *Cache[T]) Proxy(ctx context.Context, h cas.Hash) (*memory.CachedObject[T], error) {
	co, err := c.CachedStore.Proxy(ctx, h)
	if err != nil {
		return nil, err
	}
	key := h.String()
	c.mu.Lock()
	c.touchLocked(key)
	c.mu.Unlock()
	return co, nil
}

// Get returns the loaded object, routing through Proxy so existing entries
// are promoted on every access.
func (c *Cache[T]) Get(ctx context.Context, h cas.Hash) (T, error) {
	co, err := c.Proxy(ctx, h)
	if err != nil {
		var zero T
		return zero, err
	}
	return co.Load(ctx)
}
