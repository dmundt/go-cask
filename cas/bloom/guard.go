package bloom

import (
	"context"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Guard is an optional, advisory front-end for a cas.Backend.
//
// This layer is optional: if it is not enabled, the underlying backend behaves
// exactly as before. The filter adds a probabilistic pre-check only; it never
// changes object identity, validation, or the authoritative semantics of the
// underlying store. A positive result is only a hint, and the real backend still
// decides the final answer.
type guardFilter interface {
	Add(cas.Digest)
	Contains(cas.Digest) bool
}

type removerFilter interface {
	Remove(cas.Digest)
}

type Guard struct {
	backend cas.Backend
	filter  guardFilter
}

// NewGuard wraps backend in a bloom-backed advisory guard.
func NewGuard(backend cas.Backend, filter guardFilter) *Guard {
	if backend == nil {
		panic("bloom: backend is nil")
	}
	if filter == nil {
		panic("bloom: filter is nil")
	}
	return &Guard{backend: backend, filter: filter}
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
func (g *Guard) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if d.IsZero() {
		return false, nil
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

// Filter returns the underlying advisory filter.
func (g *Guard) Filter() guardFilter {
	return g.filter
}
