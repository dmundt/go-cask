package memory

import (
	"context"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// prefetchTimeout bounds every asynchronous prefetch launched by SmartCache.
const prefetchTimeout = 5 * time.Second

// SmartCache[T] wraps a CachedStore[T] and adds prefetch-on-access:
// GetWithPrefetch loads the requested object and then asynchronously
// prefetches its references (to prefetchDepth levels) so later reads hit
// the cache. Prefetching never blocks or fails the caller.
type SmartCache[T cas.Object[T]] struct {
	store         *CachedStore[T]
	prefetchDepth int
}

// NewSmartCache wraps store with reference prefetching to prefetchDepth
// levels. A depth <= 0 disables prefetching.
func NewSmartCache[T cas.Object[T]](store *CachedStore[T], prefetchDepth int) *SmartCache[T] {
	return &SmartCache[T]{store: store, prefetchDepth: prefetchDepth}
}

// GetWithPrefetch loads the object at h and, if prefetching is enabled,
// asynchronously warms the cache with every reachable reference up to
// prefetchDepth levels.
func (c *SmartCache[T]) GetWithPrefetch(ctx context.Context, h cas.Hash) (T, error) {
	loaded, err := c.store.Get(ctx, h)
	if err != nil {
		var zero T
		return zero, err
	}
	if c.prefetchDepth <= 0 {
		return loaded, nil
	}
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), prefetchTimeout)
		defer cancel()
		c.prefetchRecursive(pctx, loaded, c.prefetchDepth)
	}()
	return loaded, nil
}

func (c *SmartCache[T]) prefetchRecursive(ctx context.Context, obj T, depth int) {
	if depth <= 0 {
		return
	}
	for _, ref := range obj.References() {
		loaded, err := c.store.Get(ctx, ref)
		if err != nil {
			continue
		}
		c.prefetchRecursive(ctx, loaded, depth-1)
	}
}
