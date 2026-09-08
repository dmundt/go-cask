// Package lru provides a size-bounded LRU cache that wraps a memory.CachedStore.
package lru

import (
	"container/list"
	"context"
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/cache/memory"
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
		c.EvictKey(evictedKey)
		c.IncrEvicts()
	}
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
