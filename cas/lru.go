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
//
// The bound is enforced for EVERY insertion path: CachedStore calls the
// policy hook on each newly cached key, so Get, GetTyped, Preload, Warmup
// and PreloadRecursive all funnel through the same LRU bookkeeping — the
// cache can never exceed maxSize. Get additionally promotes existing
// entries, and GetTyped is overridden to route through Get so its loads
// respect the same policy.
type LRUCache[T Object[T]] struct {
	*CachedStore[T]
	mu      sync.Mutex
	maxSize int
	list    *list.List // *CachedObject[T], front = most recent
	index   map[string]*list.Element
}

// NewLRUCache wraps store in a size-bounded cache. maxSize must be > 0.
func NewLRUCache[T Object[T]](store *Store[T], maxSize int) (*LRUCache[T], error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("cas: LRU maxSize must be > 0, got %d", maxSize)
	}
	cs := NewCachedStore(store)
	c := &LRUCache[T]{
		CachedStore: cs,
		maxSize:     maxSize,
		list:        list.New(),
		index:       make(map[string]*list.Element),
	}
	cs.onNew = c.note // every new cached key goes through the LRU policy
	return c, nil
}

// note records a newly cached key as most-recent, evicting the
// least-recently used entry while the cache exceeds maxSize.
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
		c.metrics.Evicts.Add(1)
	}
}

// touchLocked moves key to the most-recent position (adding it if absent).
// Caller holds c.mu.
func (c *LRUCache[T]) touchLocked(key string) {
	if el, ok := c.index[key]; ok {
		c.list.MoveToFront(el)
		return
	}
	co, ok := c.cache.Load(key)
	if !ok {
		return // not cached (yet): nothing to order
	}
	c.index[key] = c.list.PushFront(co.(*CachedObject[T]))
}

// Get returns the (possibly not-yet-loaded) cached object for h, promoting
// it to the most-recent position. A missing object returns ErrNotFound. New
// entries were already recorded and bounded by the policy hook.
func (c *LRUCache[T]) Get(ctx context.Context, h Hash) (*CachedObject[T], error) {
	co, err := c.CachedStore.Get(ctx, h)
	if err != nil {
		return nil, err
	}
	key := h.String()
	c.mu.Lock()
	c.touchLocked(key)
	c.mu.Unlock()
	return co, nil
}

// GetTyped returns the loaded object, routing through Get so the load
// respects the LRU policy (the embedded CachedStore.GetTyped would insert
// without bound).
func (c *LRUCache[T]) GetTyped(ctx context.Context, h Hash) (Object[T], error) {
	co, err := c.Get(ctx, h)
	if err != nil {
		return nil, err
	}
	return co.Load(ctx)
}
