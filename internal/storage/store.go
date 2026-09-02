// Package storage wires the cas backend for the cask binary (the viewer and
// the CLI's local operations): it owns the filesystem store and exposes the
// byte-level operations the callers need. Per-object sizes are read from
// disk on demand (cas FSRawStore.Size) — no in-memory size map, so objects
// written outside this package always report correct sizes. Only FSRawStore
// is supported — maintenance operations (Verify, GC, Stats) are filesystem
// operations (cas-core §4.11); MemoryRawStore lacks them
// (backend-architecture §5).
package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// Config selects the store backend (backend-architecture §6).
type Config struct {
	Dir string // filesystem store directory (FSRawStore, fan-out default)
}

// Store is the cask binary's storage service over an FSRawStore.
type Store struct {
	raw *cas.FSRawStore
}

// New opens the store at cfg.Dir.
func New(ctx context.Context, cfg Config) (*Store, error) {
	raw, err := cas.NewFSRawStore(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return &Store{raw: raw}, nil
}

// Put stores the bytes read from r under h. Streaming: r is never buffered.
func (s *Store) Put(ctx context.Context, h cas.Hash, r io.Reader) error {
	return s.raw.Put(ctx, h, r)
}

// Get streams the object's bytes; the caller MUST close the ReadCloser.
func (s *Store) Get(ctx context.Context, h cas.Hash) (io.ReadCloser, error) {
	return s.raw.Get(ctx, h)
}

// Exists reports whether the object is stored.
func (s *Store) Exists(ctx context.Context, h cas.Hash) (bool, error) {
	return s.raw.Exists(ctx, h)
}

// Size returns the object's size in bytes, read from disk (ErrNotFound for
// a missing object). Correct for any object, including ones written before
// this process started.
func (s *Store) Size(h cas.Hash) (int64, error) {
	return s.raw.Size(h)
}

// Delete removes the object; a missing object is a no-op.
func (s *Store) Delete(ctx context.Context, h cas.Hash) error {
	return s.raw.Delete(ctx, h)
}

// List returns stored hashes, optionally filtered by algorithm.
func (s *Store) List(ctx context.Context, algo string) ([]cas.Hash, error) {
	return s.raw.List(ctx, algo)
}

// Stats returns per-algorithm counts and total size.
func (s *Store) Stats(ctx context.Context) (*cas.StoreStats, error) {
	return s.raw.Stats(ctx)
}

// Verify recomputes the object's hash (ErrHashMismatch on corruption).
func (s *Store) Verify(ctx context.Context, h cas.Hash) error {
	return s.raw.Verify(ctx, h)
}

// GC deletes every object whose hash is not in reachable; returns the
// number deleted.
func (s *Store) GC(ctx context.Context, reachable map[string]bool) (int64, error) {
	before, err := s.raw.Stats(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.raw.GC(ctx, reachable); err != nil {
		return 0, err
	}
	after, err := s.raw.Stats(ctx)
	if err != nil {
		return 0, err
	}
	return before.ObjectCount - after.ObjectCount, nil
}

// Prune deletes unreachable objects older than minAge (age = file mtime ≈
// first-Put time); dryRun returns the would-be-deleted set without deleting
// (dry-run is the safe default). Reachability is root-only at the byte
// layer — the store cannot interpret references (cas-core §4.11); apps that
// need graph-aware retention compute a reachable set and use GC.
func (s *Store) Prune(ctx context.Context, roots []cas.Hash, minAge time.Duration, dryRun bool) ([]cas.Hash, error) {
	return s.raw.Prune(ctx, roots, minAge, dryRun)
}
