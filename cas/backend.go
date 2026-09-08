package cas

import (
	"context"
	"io"
)

// Backend is the non-generic byte-storage contract. Every backend (FS,
// memory, S3, …) implements these five methods; the typed layer above
// (Store[T]) and every application works unchanged over any backend.
//
// Per-method contracts every backend MUST honor:
//
//   - Put: idempotent — the same hash always means the same bytes, so a
//     repeated Put of an identical hash is safe.
//   - Get: returns a stream the caller MUST close. A missing object returns
//     ErrNotFound (wrapped with %w).
//   - Exists: boolean presence check.
//   - Delete: a missing object is a no-op (no error).
//   - List: returns all stored hashes; algo != "" filters by algorithm.
type Backend interface {
	Put(ctx context.Context, h Hash, r io.Reader) error
	Get(ctx context.Context, h Hash) (io.ReadCloser, error)
	Exists(ctx context.Context, h Hash) (bool, error)
	Delete(ctx context.Context, h Hash) error
	List(ctx context.Context, algo string) ([]Hash, error)
}
