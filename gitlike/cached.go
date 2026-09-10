package gitlike

import (
	"context"
	"sync"

	"github.com/dmundt/go-cask/cas"
	lru "github.com/dmundt/go-cask/cas/cache/lru"
)

// CachedRepository wraps a Repository with per-type LRUCache[T] wrappers and
// exposes convenience, fully typed getters that hit the caches. It also
// carries an internal Resolver for cross-type resolution.
type CachedRepository struct {
	repo     *Repository
	Blobs    *lru.Cache[*Blob]
	Trees    *lru.Cache[*Tree]
	Commits  *lru.Cache[*Commit]
	Tags     *lru.Cache[*Tag]
	resolver *Resolver
}

// NewCachedRepository wraps repo with per-type LRU caches of maxSize entries
// each. maxSize must be > 0.
func NewCachedRepository(repo *Repository, maxSize int) (*CachedRepository, error) {
	blobs, err := lru.New(repo.Blobs, maxSize)
	if err != nil {
		return nil, err
	}
	trees, err := lru.New(repo.Trees, maxSize)
	if err != nil {
		return nil, err
	}
	commits, err := lru.New(repo.Commits, maxSize)
	if err != nil {
		return nil, err
	}
	tags, err := lru.New(repo.Tags, maxSize)
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

// GetCommit returns the commit at d via the commit cache.
func (c *CachedRepository) GetCommit(ctx context.Context, d cas.Digest) (*Commit, error) {
	return c.Commits.Get(ctx, d)
}

// GetTree returns the tree at d via the tree cache.
func (c *CachedRepository) GetTree(ctx context.Context, d cas.Digest) (*Tree, error) {
	return c.Trees.Get(ctx, d)
}

// GetBlob returns the blob at d via the blob cache.
func (c *CachedRepository) GetBlob(ctx context.Context, d cas.Digest) (*Blob, error) {
	return c.Blobs.Get(ctx, d)
}

// ResolveAny resolves d to any supported object type, using the caches where
// possible.
func (c *CachedRepository) ResolveAny(ctx context.Context, d cas.Digest) (*ResolvedObject, error) {
	return c.resolver.ResolveAny(ctx, d)
}

// Preloader is a background worker pool that preloads commit graphs into a
// CachedRepository: it consumes digests from a channel and runs
// Commits.PreloadRecursive(ctx, d, 2) for each. Preload is non-blocking;
// Stop cancels the workers and waits for them to drain.
type Preloader struct {
	cached *CachedRepository
	jobs   chan cas.Digest
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
	p := &Preloader{cached: cached, jobs: make(chan cas.Digest, 64), cancel: cancel}
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
		case d, ok := <-p.jobs:
			if !ok {
				return
			}
			// Best-effort: reference types this store cannot decode (trees,
			// blobs) simply fail to load here and are dropped.
			_ = p.cached.Commits.PreloadRecursive(ctx, d, 2)
		}
	}
}

// Preload enqueues d for preloading without blocking: if the queue is full,
// the digest is skipped (prefetching must never block the hot path).
func (p *Preloader) Preload(d cas.Digest) {
	select {
	case p.jobs <- d:
	default:
	}
}

// Stop cancels the workers and waits for them to exit.
func (p *Preloader) Stop() {
	p.cancel()
	p.wg.Wait()
}
