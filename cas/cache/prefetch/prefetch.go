// Package prefetch provides SmartCache, a prefetch-on-access cache for the cas
// core. It is not part of the stable cas surface (cas-core §4.10); SmartCache[T]
// wraps a memory.CachedStore[T] and is an example recipe (see examples/notes).
//
// NewSmartCache(store, depth) returns a cache whose reads load an object and
// then, in the background, warm the cache with its references (prefetchDepth
// levels deep), so later reads hit and the hot read path never blocks on
// preloading. A prefetch is best-effort: it runs on at most
// prefetchConcurrency goroutines at a time, a read that finds them all busy
// drops its prefetch instead of queueing, each digest is prefetched at most
// once per run, and the work stops at prefetchTimeout. The prefetch keeps the
// triggering ctx's values but not its cancellation, so a request-scoped ctx
// that ends when the triggering call returns does not cut the prefetch short;
// only prefetchTimeout bounds it. Prefetching never blocks a read and never
// fails it.
package prefetch

import (
	"context"
	"time"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/cache/mem"
)

const (
	// prefetchTimeout bounds every asynchronous prefetch launched by SmartCache.
	prefetchTimeout = 5 * time.Second
	// prefetchConcurrency caps how many prefetches one SmartCache runs at once.
	prefetchConcurrency = 8
)

// SmartCache[T] wraps a memory.CachedStore[T] and adds prefetch-on-access:
// GetWithPrefetch loads the requested object and then asynchronously
// prefetches its references (to prefetchDepth levels) so later reads hit the
// cache. Prefetching never blocks or fails the caller.
type SmartCache[T cas.Object[T]] struct {
	store         *mem.CachedStore[T]
	prefetchDepth int
	// sem bounds the concurrent prefetches. GetWithPrefetch takes a slot
	// without blocking and drops the prefetch when none is free, so the hot
	// read path never waits and goroutines stay bounded.
	sem chan struct{}
}

// NewSmartCache wraps store with reference prefetching to prefetchDepth
// levels. A depth <= 0 disables prefetching.
func NewSmartCache[T cas.Object[T]](store *mem.CachedStore[T], prefetchDepth int) *SmartCache[T] {
	return &SmartCache[T]{
		store:         store,
		prefetchDepth: prefetchDepth,
		sem:           make(chan struct{}, prefetchConcurrency),
	}
}

// GetWithPrefetch loads the object at d and, if prefetching is enabled,
// asynchronously warms the cache with every reachable reference up to
// prefetchDepth levels. The prefetch is skipped — never waited for — when every
// prefetch slot is busy or ctx is already canceled. The returned object and
// error are those of the load, so prefetching cannot fail the call.
func (c *SmartCache[T]) GetWithPrefetch(ctx context.Context, d cas.Digest) (T, error) {
	loaded, err := c.store.Get(ctx, d)
	if err != nil {
		var zero T
		return zero, err
	}
	if c.prefetchDepth <= 0 || ctx.Err() != nil {
		return loaded, nil
	}
	select {
	case c.sem <- struct{}{}:
	default:
		return loaded, nil // best-effort: busy prefetchers are not worth waiting for
	}
	go func() {
		defer func() { <-c.sem }()
		// WithoutCancel keeps ctx's values but drops its cancellation, so a
		// request-scoped ctx that ends the instant GetWithPrefetch returns
		// (the common case: an HTTP handler's r.Context()) does not kill the
		// prefetch before it starts. prefetchTimeout is the only thing that
		// bounds this background work.
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), prefetchTimeout)
		defer cancel()
		seen := map[string]struct{}{d.String(): {}}
		c.prefetchRecursive(pctx, loaded, c.prefetchDepth, seen)
	}()
	return loaded, nil
}

// prefetchRecursive loads every reference reachable from obj within depth
// levels. Absent references, references this store cannot decode, digests
// already visited in this run (keyed by their rendered form, so a diamond or
// cyclic graph is walked once), and work remaining after ctx is canceled are
// all skipped.
func (c *SmartCache[T]) prefetchRecursive(ctx context.Context, obj T, depth int, seen map[string]struct{}) {
	if depth <= 0 {
		return
	}
	for _, ref := range obj.References() {
		if ctx.Err() != nil {
			return
		}
		if ref.IsZero() {
			continue
		}
		key := ref.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		loaded, err := c.store.Get(ctx, ref)
		if err != nil {
			continue
		}
		c.prefetchRecursive(ctx, loaded, depth-1, seen)
	}
}
