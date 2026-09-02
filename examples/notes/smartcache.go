package main

import (
	"context"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// prefetchTimeout bounds every asynchronous prefetch.
const prefetchTimeout = 5 * time.Second

// SmartCache[T] wraps a cas.CachedStore[T] and adds prefetch-on-access:
// GetWithPrefetch loads the requested object and then asynchronously
// prefetches its references (to prefetchDepth levels) so later reads hit
// the cache. Prefetching never blocks or fails the caller — it runs in a
// detached goroutine with a 5 s timeout, and errors are dropped. This is
// the example's own prefetch recipe (formerly cas/extra), inlined per the
// self-contained-examples rule.
type SmartCache[T any] struct {
	store         *cas.CachedStore[T]
	prefetchDepth int
}

// NewSmartCache wraps store with reference prefetching to prefetchDepth
// levels. A depth <= 0 disables prefetching (GetWithPrefetch then behaves
// like GetTyped).
func NewSmartCache[T any](store *cas.CachedStore[T], prefetchDepth int) *SmartCache[T] {
	return &SmartCache[T]{store: store, prefetchDepth: prefetchDepth}
}

// GetWithPrefetch loads the object at h and, if the load succeeds,
// asynchronously prefetches the object's references to the configured depth.
// It returns the loaded object; prefetch progress is observable through the
// cache's stats.
func (c *SmartCache[T]) GetWithPrefetch(ctx context.Context, h cas.Hash) (cas.Object[T], error) {
	obj, err := c.store.GetTyped(ctx, h)
	if err != nil {
		return nil, err
	}
	if c.prefetchDepth > 0 {
		go func() {
			pctx, cancel := context.WithTimeout(context.Background(), prefetchTimeout)
			defer cancel()
			for _, ref := range obj.References() {
				select {
				case <-pctx.Done():
					return
				default:
				}
				_ = c.store.PreloadRecursive(pctx, ref, c.prefetchDepth-1)
			}
		}()
	}
	return obj, nil
}
