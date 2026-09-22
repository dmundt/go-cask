package bloom

import (
	"context"
	"errors"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Filter is the advisory membership filter a Guard consults before it touches
// the wrapped backend.
//
// A Filter is a hint structure, never an authority: Add records that a digest
// was written, and Contains reports whether the digest may be present. A false
// result from Contains must be definitive for absence, while a true result is
// only a hint that still requires a backend lookup. Implementations must be safe
// for concurrent use, because a Guard may be shared by multiple goroutines.
type Filter interface {
	// Add records a digest as (probably) present in the filter.
	Add(cas.Digest)
	// Contains reports whether the filter may hold the digest. A false result
	// must mean the digest is absent; a true result is only a hint.
	Contains(cas.Digest) bool
}

// removerFilter is the optional capability a Filter may additionally provide.
// It is unexported on purpose: removal is supported only by filters that can
// implement it without false negatives (for example the counting variant), and
// a Guard checks for it with a type assertion.
type removerFilter interface {
	// Remove removes a digest from the filter.
	Remove(cas.Digest)
}

// Guard wraps a backend with an advisory Bloom-filter pre-check.
type Guard struct {
	backend cas.Backend
	filter  Filter
}

// NewGuard wraps backend in a bloom-backed advisory guard.
//
// It returns an error when backend or filter is nil; the guard has no sensible
// degraded mode for either argument, and a nil argument is a caller mistake
// rather than a runtime condition.
func NewGuard(backend cas.Backend, filter Filter) (*Guard, error) {
	if backend == nil {
		return nil, errors.New("bloom: backend is nil")
	}
	if filter == nil {
		return nil, errors.New("bloom: filter is nil")
	}
	return &Guard{backend: backend, filter: filter}, nil
}

// Put records the digest in the filter after a successful backend write.
func (g *Guard) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if err := g.backend.Put(ctx, d, r); err != nil {
		return err
	}
	g.filter.Add(d)
	return nil
}

// Get delegates to the underlying backend.
func (g *Guard) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return g.backend.Get(ctx, d)
}

// Exists performs the probability check first, then confirms against the real
// store. A negative bloom result is definitive for absence; a positive bloom
// result is advisory and still requires a backend lookup.
//
// The digest contract matches every other cas.Backend: an absent digest is
// rejected with cas.ErrInvalidDigest rather than reported as "not present", so a
// Guard is a drop-in replacement for the backend it wraps.
func (g *Guard) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if err := cas.CheckDigest(d, "bloom: exists"); err != nil {
		return false, err
	}
	if !g.filter.Contains(d) {
		return false, nil
	}
	return g.backend.Exists(ctx, d)
}

// Delete delegates to the underlying backend.
//
// Bloom filters do not support safe deletion without a counting structure, so
// this method intentionally leaves the filter unchanged. The wrapped backend is
// still the source of truth.
func (g *Guard) Delete(ctx context.Context, d cas.Digest) error {
	if err := g.backend.Delete(ctx, d); err != nil {
		return err
	}
	if r, ok := g.filter.(removerFilter); ok {
		r.Remove(d)
	}
	return nil
}

// List delegates to the underlying backend.
func (g *Guard) List(ctx context.Context) ([]cas.Digest, error) {
	return g.backend.List(ctx)
}

// Stats delegates to the underlying backend.
func (g *Guard) Stats(ctx context.Context) (*cas.Stats, error) {
	return g.backend.Stats(ctx)
}

// Filter returns the advisory Filter the guard consults, which is the exact
// value that was passed to NewGuard.
//
// The returned filter is the caller's own value: the guard never wraps or
// copies it, so mutating it also changes what the guard sees, and callers that
// share a filter between guards must respect its concurrency guarantees.
func (g *Guard) Filter() Filter {
	return g.filter
}
