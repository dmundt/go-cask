package cas

import (
	"context"
	"io"
)

// Backend stores immutable content-addressed blobs.
//
// A blob is identified solely by its Digest — an opaque key the client computed
// with whatever hash algorithm it chose. Backends are responsible only for
// storing and retrieving bytes; they know nothing about envelopes, codecs,
// object types, generics, or hash algorithms.
//
// Integrity is provided by the key: the digest IS the content's digest, so any
// conflict is impossible by construction. Backends store and return bytes
// without recomputing the digest — explicit integrity validation is a separate
// maintenance layer (cas.Verify / cas.NewVerifier / fs.Backend.Verify with the
// client's Hasher), which recomputes the digest and reports ErrDigestMismatch
// on corruption.
//
// Implementations must be safe for concurrent use.
//
// Backend guarantees — every implementation MUST provide:
//
//   - Content-addressability: the digest IS the object identity. Same digest
//     always means same bytes. A backend must never return bytes different
//     from those associated with the key.
//   - Immutability: objects are immutable once written. A repeated Put of
//     identical content is safe (same digest → same bytes). A conflict
//     (different bytes, same digest) must be impossible by construction:
//     the digest is the digest of the content; see ErrDigestMismatch.
//   - Idempotent writes: Put(ctx, d, r) called twice on the same digest
//     produces the same final state.
//   - Streaming: implementations stream from r and serve Get readers
//     without buffering the entire object in memory.
//   - Concurrent safety: safe for concurrent goroutines without external
//     synchronization.
//   - Stable errors: a missing object returns ErrNotFound (wrapped with
//     %w). Delete of a missing object is a no-op (returns nil).
//   - List returns digests only — no storage metadata (paths, timestamps,
//     permissions, S3 keys).
//   - Stats returns a Stats summary (object count and total size).
//
// Separation of responsibilities:
//
//	Backend:  Digest → Bytes (+ Stats)
//	Store:    Object ↔ Codec ↔ Envelope ↔ Bytes ↔ client Hasher
//
// This keeps the backend contract stable while allowing codecs, envelopes,
// caches, object models and hash algorithms to evolve independently.
//
// Reading many objects: Get is one object per call, and the measured cost of
// loading a store is per open rather than per byte, so a caller loading a whole
// revision should not loop over Get. The package-level GetMany (batch.go)
// serves a batch of digests and dispatches to the optional BatchGetter
// interface — packfs, for example, opens one pack file for many adjacent
// objects — and falls back to a sequential Get loop for every other backend.
// A caller that wants the typed or parallel path uses the caching layer
// instead: cachemem.CachedStore[T] (cas/cache/mem), lru.Cache[T] (cas/cache/lru)
// and prefetch.SmartCache[T] (cas/cache/prefetch), sized from Stats. cas-core
// §4.13 records the recipe. Neither GetMany nor this interface changes the
// Backend method set.
type Backend interface {
	// Put stores bytes from r under d.
	Put(ctx context.Context, d Digest, r io.Reader) error
	// Get returns a reader for bytes stored under d.
	Get(ctx context.Context, d Digest) (io.ReadCloser, error)
	// Exists reports whether d is stored.
	Exists(ctx context.Context, d Digest) (bool, error)
	// Delete removes d when it exists.
	Delete(ctx context.Context, d Digest) error
	// List returns every stored digest.
	List(ctx context.Context) ([]Digest, error)
	// Stats returns stored object and byte totals.
	Stats(ctx context.Context) (*Stats, error)
}
