package cache

import (
	"container/list"
	"context"
	"fmt"
	"sync"

	"github.com/dmundt/go-cask/cas"
)

// LRUCache[T] is a size-bounded cache: it embeds CachedStore[T] (lazy
// CachedObject[T] semantics) and adds an LRU eviction policy with a maximum
// number of entries.
type LRUCache[T cas.Object[T]] struct {
	*CachedStore[T]
	mu      sync.Mutex
	maxSize int
	list    *list.List
	index   map[string]*list.Element
}

// NewLRUCache wraps store in a size-bounded cache. maxSize must be > 0.
func NewLRUCache[T cas.Object[T]](store *cas.Store[T], maxSize int) (*LRUCache[T], error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("cache: LRU maxSize must be > 0, got %d", maxSize)
	}
	cs := NewCachedStore(store)
	c := &LRUCache[T]{
		CachedStore: cs,
		maxSize:     maxSize,
		list:        list.New(),
		index:       make(map[string]*list.Element),
	}
	cs.onNew = c.note
	return c, nil
}

func (c *LRUCache[T]) note(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touchLocked(key)
	for c.list.Len() > c.maxSize {
		last := c.list.Back()
		if last == nil {
			break
		}
		c.list.Remove(last)
		evicted := last.Value.(*CachedObject[T])
		evictedKey := evicted.hash.String()
		delete(c.index, evictedKey)
		c.CachedStore.evictKey(evictedKey)
		c.CachedStore.metrics.Evicts.Add(1)
	}
}

func (c *LRUCache[T]) touchLocked(key string) {
	if el, ok := c.index[key]; ok {
		c.list.MoveToFront(el)
		return
	}
	co, ok := c.cache.Load(key)
	if !ok {
		return
	}
	c.index[key] = c.list.PushFront(co.(*CachedObject[T]))
}

// Proxy returns the (possibly not-yet-loaded) CachedObject for h, promoting
// it to the most-recent position.
func (c *LRUCache[T]) Proxy(ctx context.Context, h cas.Hash) (*CachedObject[T], error) {
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
func (c *LRUCache[T]) Get(ctx context.Context, h cas.Hash) (T, error) {
	co, err := c.Proxy(ctx, h)
	if err != nil {
		var zero T
		return zero, err
	}
	return co.Load(ctx)
}
