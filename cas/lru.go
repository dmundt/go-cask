package cas

import (
	"container/list"
	"context"
	"fmt"
	"sync"
)

// LRUCache[T] is a size-bounded cache: it embeds CachedStore[T] (lazy
// CachedObject[T] semantics) and adds an LRU eviction policy with a maximum
// number of entries. The LRU is an in-tree std-lib implementation
// (container/list + map) — no external dependency (coding-guidelines §3).
// Get is overridden to promote existing entries and evict the least-recently
// used entry when the cache exceeds maxSize; evicted entries are removed
// from the underlying map cache as well, so cache size never exceeds
// maxSize.
type LRUCache[T any] struct {
	*CachedStore[T]
	mu      sync.Mutex
	maxSize int
	list    *list.List // *CachedObject[T], front = most recent
	index   map[string]*list.Element
}

// NewLRUCache wraps store in a size-bounded cache. maxSize must be > 0.
func NewLRUCache[T any](store *Store[T], maxSize int) (*LRUCache[T], error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("cas: LRU maxSize must be > 0, got %d", maxSize)
	}
	return &LRUCache[T]{
		CachedStore: NewCachedStore(store),
		maxSize:     maxSize,
		list:        list.New(),
		index:       make(map[string]*list.Element),
	}, nil
}

// Get returns the (possibly not-yet-loaded) cached object for h, promoting
// it to the most-recent position and evicting the least-recently used entry
// if the cache exceeds maxSize. A missing object returns ErrNotFound.
func (c *LRUCache[T]) Get(ctx context.Context, h Hash) (*CachedObject[T], error) {
	co, err := c.CachedStore.Get(ctx, h)
	if err != nil {
		return nil, err
	}
	key := h.String()

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.list.MoveToFront(el)
		return co, nil
	}
	c.index[key] = c.list.PushFront(co)
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
		c.metrics.Evicts.Add(1)
	}
	return co, nil
}
