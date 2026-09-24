package cas

import (
	"context"
	"fmt"
	"io"
)

// BatchGetter is the optional interface a backend implements to serve a batch
// of digests its own way — one open or one request for many objects — instead
// of one Get per object. packfs, for example, reads one pack file for many
// adjacent objects.
//
// GetMany backs the package-level GetMany function and carries exactly its
// contract: stream the requested objects in any order, call fn once per served
// object, and close each reader handed to fn after fn returns. Implementing
// BatchGetter is optional and purely an optimization — GetMany falls back to a
// sequential Get loop, so every Backend already satisfies the contract without
// implementing this interface (mirroring the optional Cleaner and Statter
// capabilities).
type BatchGetter interface {
	// GetMany streams the objects stored at digests, in any order, calling fn
	// once per served object. The reader handed to fn is closed by GetMany
	// after fn returns, so fn must consume what it needs before returning.
	// The first error from a read or from fn stops the batch and is returned;
	// a canceled ctx stops it and returns ctx.Err().
	GetMany(ctx context.Context, digests []Digest, fn func(Digest, io.ReadCloser) error) error
}

// GetMany streams the objects stored at digests, in any order, calling fn once
// per served object with a reader positioned at the object's first byte.
//
// A Backend that implements BatchGetter serves the batch its own way (packfs
// reads one pack file for many adjacent objects); every other backend runs the
// default sequential loop of Get, so GetMany works against any Backend and is
// correct for all of them. The default loop is the reference for the contract
// below.
//
// Ownership is explicit: GetMany closes each reader it hands to fn, after fn
// returns. A caller therefore cannot leak a reader, and fn MUST consume
// everything it needs from the reader before returning, because the reader is
// closed as soon as fn does.
//
// Order and call count are unspecified: every requested digest the backend can
// serve is served, but a BatchGetter MAY serve the objects in a different order
// than requested, and MAY coalesce a digest that appears more than once (the
// default loop makes one call per appearance, in the order given). Callers that
// need a particular order handle it themselves; do not assume the order given.
//
// Errors stop the batch. The first error from a read or from fn is returned —
// fn's own error unwrapped, a read error wrapped with %w and the digest it
// failed on. ctx is checked before each object, and a canceled ctx stops the
// loop and returns ctx.Err(). An absent digest fails the batch with
// ErrInvalidDigest (CheckDigest) rather than addressing an object that cannot
// exist, and a digest that is not stored fails it with the backend's
// ErrNotFound; GetMany does not skip missing objects.
//
// Concurrency is deliberately not part of GetMany: the function sequences the
// batch, because the win the issue is after is fewer opens in the backend, not
// more goroutines in the core. A caller that wants parallel loads uses the
// caching layer instead — warm a cache with cachemem.CachedStore.Preload
// (cas/cache/mem), lru.Cache (cas/cache/lru) or prefetch.SmartCache
// (cas/cache/prefetch), and size it from the backend's Stats before a large
// traversal. cas-core §4.13 records the prefetch recipe.
func GetMany(ctx context.Context, raw Backend, digests []Digest, fn func(Digest, io.ReadCloser) error) error {
	if raw == nil {
		return fmt.Errorf("cas: get many: nil backend")
	}
	if fn == nil {
		return fmt.Errorf("cas: get many: nil fn")
	}
	if batch, ok := raw.(BatchGetter); ok {
		return batch.GetMany(ctx, digests, fn)
	}
	return getManyEach(ctx, raw, digests, fn)
}

// getManyEach is the default GetMany: a sequential Get loop that every Backend
// supports. It implements the documented contract literally — requested order,
// one call per appearance, each reader closed exactly once, and the first error
// from Get, from fn or from ctx returned.
func getManyEach(ctx context.Context, raw Backend, digests []Digest, fn func(Digest, io.ReadCloser) error) error {
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := CheckDigest(d, "cas: get many"); err != nil {
			return err
		}
		reader, err := raw.Get(ctx, d)
		if err != nil {
			return fmt.Errorf("cas: get many: %s: %w", d, err)
		}
		if err := closeAfterFn(fn, d, reader); err != nil {
			return err
		}
	}
	return nil
}

// closeAfterFn calls fn with the reader and closes the reader exactly once,
// whether fn returned an error or panicked. fn's error wins; a failing Close is
// reported only when fn itself succeeded, so a close failure never masks the
// real cause.
func closeAfterFn(fn func(Digest, io.ReadCloser) error, d Digest, reader io.ReadCloser) (err error) {
	defer func() {
		if closeErr := reader.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("cas: get many: close %s: %w", d, closeErr)
		}
	}()
	return fn(d, reader)
}
