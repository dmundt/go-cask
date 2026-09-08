package cas

import (
	"context"
	"io"
)

// Backend stores immutable content-addressed blobs.
//
// A blob is identified solely by its Hash. Backends are responsible only
// for storing and retrieving bytes; they know nothing about envelopes,
// codecs, object types, or generics.
//
// Integrity is provided by the address hash: the hash is the content,
// so any corruption is detected the moment the bytes are read — the
// calling layer can recompute the hash and compare.
//
// Implementations must be safe for concurrent use.
//
// Backend guarantees — every implementation MUST provide:
//
//   - Content-addressability: the hash IS the object identity. Same hash
//     always means same bytes. A backend must never return bytes different
//     from those associated with the hash.
//   - Immutability: objects are immutable once written. A repeated Put of
//     identical content is safe (same hash → same bytes). A conflict
//     (different bytes, same hash) must be impossible by construction:
//     the hash is the hash of the content; see ErrHashMismatch.
//   - Idempotent writes: Put(ctx, h, r) called twice on the same hash
//     produces the same final state.
//   - Streaming: implementations stream from r and serve Get readers
//     without buffering the entire object in memory.
//   - Concurrent safety: safe for concurrent goroutines without external
//     synchronization.
//   - Stable errors: a missing object returns ErrNotFound (wrapped with
//     %w). Delete of a missing object is a no-op (returns nil).
//   - List returns hashes only — no storage metadata (paths, timestamps,
//     permissions, S3 keys).
//
// Separation of responsibilities:
//
//	Backend:  Hash → Bytes
//	Store:    Object ↔ Codec ↔ Envelope ↔ Bytes
//
// This keeps the backend contract stable while allowing codecs, envelopes,
// caches, and object models to evolve independently.
type Backend interface {
	Put(ctx context.Context, h Hash, r io.Reader) error
	Get(ctx context.Context, h Hash) (io.ReadCloser, error)
	Exists(ctx context.Context, h Hash) (bool, error)
	Delete(ctx context.Context, h Hash) error
	List(ctx context.Context, algo string) ([]Hash, error)
}
