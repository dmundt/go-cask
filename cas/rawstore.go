package cas

import (
	"context"
	"io"
)

// RawStore is the non-generic byte-storage contract. Every backend (FS,
// memory, S3, …) implements these five methods; the typed layer above
// (Store[T]) and every application works unchanged over any backend.
//
// Per-method contracts every backend MUST honor:
//
//   - Put: idempotent — the same hash always means the same bytes, so a
//     repeated Put of an identical hash is safe (it may overwrite with
//     identical bytes). Implementations MUST stream r without buffering it
//     fully (the hash in h is trusted as the address; Verify is the
//     integrity check).
//   - Get: returns a stream the caller MUST close. A missing object returns
//     ErrNotFound (wrapped with %w).
//   - Exists: boolean presence check.
//   - Delete: a missing object is a no-op (no error).
//   - List: returns all stored hashes; algo != "" filters by algorithm.
type RawStore interface {
	Put(ctx context.Context, h Hash, r io.Reader) error
	Get(ctx context.Context, h Hash) (io.ReadCloser, error)
	Exists(ctx context.Context, h Hash) (bool, error)
	Delete(ctx context.Context, h Hash) error
	List(ctx context.Context, algo string) ([]Hash, error)
}
