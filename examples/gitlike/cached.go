package gitlike

import (
	"context"
	"sync"

	"github.com/dmundt/go-cask/cas"
)

// CachedRepository wraps a Repository with per-type LRUCache[T] wrappers and
// exposes convenience, fully typed getters that hit the caches. It also
// carries an internal Resolver for cross-type resolution.
type CachedRepository struct {
	repo     *Repository
	Blobs    *cas.LRUCache[*Blob]
	Trees    *cas.LRUCache[*Tree]
	Commits  *cas.LRUCache[*Commit]
	Tags     *cas.LRUCache[*Tag]
	resolver *Resolver
}

// NewCachedRepository wraps repo with per-type LRU caches of maxSize entries
// each. maxSize must be > 0.
func NewCachedRepository(repo *Repository, maxSize int) (*CachedRepository, error) {
	blobs, err := cas.NewLRUCache(repo.Blobs, maxSize)
	if err != nil {
		return nil, err
	}
	trees, err := cas.NewLRUCache(repo.Trees, maxSize)
	if err != nil {
		return nil, err
	}
	commits, err := cas.NewLRUCache(repo.Commits, maxSize)
	if err != nil {
		return nil, err
	}
	tags, err := cas.NewLRUCache(repo.Tags, maxSize)
	if err != nil {
		return nil, err
	}
	return &CachedRepository{
		repo:     repo,
		Blobs:    blobs,
		Trees:    trees,
		Commits:  commits,
		Tags:     tags,
		resolver: NewResolver(repo),
	}, nil
}

// GetCommit returns the commit at h via the commit cache.
func (c *CachedRepository) GetCommit(ctx context.Context, h cas.Hash) (*Commit, error) {
	obj, err := c.Commits.GetTyped(ctx, h)
	if err != nil {
		return nil, err
	}
	return obj.(*Commit), nil
}

// GetTree returns the tree at h via the tree cache.
func (c *CachedRepository) GetTree(ctx context.Context, h cas.Hash) (*Tree, error) {
	obj, err := c.Trees.GetTyped(ctx, h)
	if err != nil {
		return nil, err
	}
	return obj.(*Tree), nil
}

// GetBlob returns the blob at h via the blob cache.
func (c *CachedRepository) GetBlob(ctx context.Context, h cas.Hash) (*Blob, error) {
	obj, err := c.Blobs.GetTyped(ctx, h)
	if err != nil {
		return nil, err
	}
	return obj.(*Blob), nil
}

// ResolveAny resolves h to any supported object type, using the caches where
// possible.
func (c *CachedRepository) ResolveAny(ctx context.Context, h cas.Hash) (*ResolvedObject, error) {
	return c.resolver.ResolveAny(ctx, h)
}

// Preloader is a background worker pool that preloads commit graphs into a
// CachedRepository: it consumes hashes from a channel and runs
// Commits.PreloadRecursive(ctx, h, 2) for each. Preload is non-blocking;
// Stop cancels the workers and waits for them to drain.
type Preloader struct {
	cached *CachedRepository
	jobs   chan cas.Hash
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPreloader starts workers goroutines (default 2 when workers <= 0)
// preloading into cached.
func NewPreloader(cached *CachedRepository, workers int) *Preloader {
	if workers <= 0 {
		workers = 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Preloader{cached: cached, jobs: make(chan cas.Hash, 64), cancel: cancel}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}
	return p
}

func (p *Preloader) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case h, ok := <-p.jobs:
			if !ok {
				return
			}
			// Best-effort: reference types this store cannot decode (trees,
			// blobs) simply fail to load here and are dropped.
			_ = p.cached.Commits.PreloadRecursive(ctx, h, 2)
		}
	}
}

// Preload enqueues h for preloading without blocking: if the queue is full,
// the hash is skipped (prefetching must never block the hot path).
func (p *Preloader) Preload(h cas.Hash) {
	select {
	case p.jobs <- h:
	default:
	}
}

// Stop cancels the workers and waits for them to exit.
func (p *Preloader) Stop() {
	p.cancel()
	p.wg.Wait()
}
