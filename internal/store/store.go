// Package store opens and maintains the content-addressable store the cask CLI
// speaks to.
//
// It is the CLI's single backend-selection seam: Open maps a -backend name to a
// concrete cas.Backend and reports the maintenance capabilities that backend
// supports (cas.CapabilitiesOf), so every subcommand is backend-agnostic —
// put/get/list/meta/stats use the minimal Backend contract, and
// verify/gc/prune/clean run through the portable maintenance layer
// (cas.Verify/cas.VerifyAll, cas.Sweep, cas.Cleaner) when a backend has no
// native implementation of its own (backend-architecture §5, cli.md §2).
//
// An operation the selected backend cannot support fails with an error that
// names the operation and the backend and wraps cas.ErrUnsupported, rather than
// being silently skipped or reported as success.
//
// The package is module-internal and used only by the cask CLI: it is the
// backend-selection seam, not a public library surface. Library consumers
// construct their backend directly.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	"github.com/dmundt/go-cask/cas/backend/packfs"
)

// Kind names a selectable storage backend. Its values are exactly the ones the
// CLI's -backend flag accepts.
type Kind string

const (
	// KindFS is the loose filesystem backend, cas/backend/fs: Git-like fan-out
	// directories, one file per object. It is the default.
	KindFS Kind = "fs"
	// KindPackFS is the packfile filesystem backend, cas/backend/packfs: the
	// same loose objects plus append-only pack files and a JSON index.
	KindPackFS Kind = "packfs"
)

// DefaultKind is the backend a store opens when -backend is not given: the
// loose filesystem backend, so an existing store keeps the behavior it had
// before the flag existed.
const DefaultKind = KindFS

// ParseKind validates a -backend value. The empty name selects DefaultKind, so
// an absent flag keeps the documented default.
func ParseKind(name string) (Kind, error) {
	switch Kind(name) {
	case "":
		return DefaultKind, nil
	case KindFS, KindPackFS:
		return Kind(name), nil
	default:
		return "", fmt.Errorf("unknown backend %q (want %q or %q)", name, KindFS, KindPackFS)
	}
}

// Options selects the store to open.
type Options struct {
	// Kind is the storage backend to open. The zero value selects
	// DefaultKind.
	Kind Kind
	// Path is the store directory.
	Path string
}

// Store is an opened store: the storage backend plus the maintenance
// capabilities it supports.
//
// It embeds the backend, so Put/Get/Exists/Delete/List/Stats are the backend's
// own methods, while Size, ModTime, Verify, VerifyAll, Sweep and Clean are the
// portable wrappers the CLI runs. A backend that cannot support one of them is
// reported through cas.ErrUnsupported instead of a missing method or a silent
// partial result.
type Store struct {
	// Backend is the opened backend every operation runs against.
	cas.Backend
	// Capabilities reports which optional maintenance operations Backend
	// supports, as cas.CapabilitiesOf reports them.
	Capabilities cas.Capabilities
	// Kind names the backend the store was opened as; it is the name an
	// unsupported-operation error reports.
	Kind Kind

	closeOnce sync.Once
	closeErr  error
}

// pruner is a backend-native age-based sweep — fs.Backend implements it.
// Backends without one (packfs) are served by the portable cas.Sweep.
type pruner interface {
	Prune(ctx context.Context, reachable map[string]bool, minAge time.Duration, dryRun bool) ([]cas.Digest, error)
}

// The binding is compile-time: if fs.Backend.Prune's signature moves, the build
// fails here instead of Store.Sweep silently taking the portable path and
// running a different sweep than the backend's own.
var _ pruner = (*fsbackend.Backend)(nil)

// Open opens the store selected by opts over path. A missing path or an
// unknown kind is a caller error; a backend failure is returned as it is, so
// the CLI can classify it (cli.md §3).
func Open(ctx context.Context, opts Options) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind, err := ParseKind(string(opts.Kind))
	if err != nil {
		return nil, err
	}
	if opts.Path == "" {
		return nil, errors.New("cask: store path is required")
	}
	switch kind {
	case KindFS:
		backend, err := fsbackend.New(opts.Path)
		if err != nil {
			return nil, err
		}
		return newStore(kind, backend), nil
	case KindPackFS:
		// Packing is the point of selecting packfs: every Put also appends to
		// the active pack file, so the CLI maintains a genuinely packed store.
		backend, err := packfs.New(opts.Path, packfs.WithEnabled())
		if err != nil {
			return nil, err
		}
		return newStore(kind, backend), nil
	default:
		// ParseKind accepts only the kinds above, so this is unreachable for
		// a caller that went through it; a Store built by hand may still carry
		// another name, and a switch that names every case keeps the compiler
		// checking it.
		return nil, fmt.Errorf("unknown backend %q (want %q or %q)", kind, KindFS, KindPackFS)
	}
}

// OpenViewer opens the store the embedded viewer reads and returns the
// filesystem backend it needs.
//
// The viewer reads per-object physical metadata (Size, ModTime) and lists
// through the concrete fs.Backend (internal/web.New), so a backend with no
// filesystem view cannot serve it: selecting one reports ErrUnsupported naming
// the operation and the backend rather than silently reading a different
// directory than -store named.
func OpenViewer(ctx context.Context, opts Options) (*fsbackend.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind, err := ParseKind(string(opts.Kind))
	if err != nil {
		return nil, err
	}
	if kind != KindFS {
		return nil, unsupported("web", kind)
	}
	if opts.Path == "" {
		return nil, errors.New("cask: store path is required")
	}
	return fsbackend.New(opts.Path)
}

// newStore wraps an opened backend with the maintenance capabilities the CLI
// consults.
func newStore(kind Kind, backend cas.Backend) *Store {
	return &Store{
		Backend:      backend,
		Capabilities: cas.CapabilitiesOf(backend),
		Kind:         kind,
	}
}

// unsupported reports op as unsupported by the named backend. The message names
// both the operation and the backend, and the error wraps cas.ErrUnsupported so
// a caller can classify it with errors.Is.
func unsupported(op string, kind Kind) error {
	return fmt.Errorf("cask: %s is not supported by the %q backend: %w", op, kind, cas.ErrUnsupported)
}

// unsupported names op and the store's own backend.
func (s *Store) unsupported(op string) error { return unsupported(op, s.Kind) }

// Size returns the stored object's size in bytes. It needs the backend to
// implement cas.Statter (physical per-object metadata); a backend that does not
// reports ErrUnsupported naming the operation.
func (s *Store) Size(ctx context.Context, d cas.Digest) (int64, error) {
	statter, ok := s.Backend.(cas.Statter)
	if !ok {
		return 0, s.unsupported("size")
	}
	return statter.Size(ctx, d)
}

// ModTime returns the object's physical modification time. It needs the
// backend to implement cas.Statter; a backend that does not reports
// ErrUnsupported naming the operation.
func (s *Store) ModTime(ctx context.Context, d cas.Digest) (time.Time, error) {
	statter, ok := s.Backend.(cas.Statter)
	if !ok {
		return time.Time{}, s.unsupported("mod time")
	}
	return statter.ModTime(ctx, d)
}

// Verify re-reads the object at d and recomputes its digest with hasher through
// the portable cas.Verify, so the check works against every backend whether or
// not it has a backend-native Verify (Capabilities.Verify is always true).
func (s *Store) Verify(ctx context.Context, d cas.Digest, hasher cas.Hasher) error {
	return cas.Verify(ctx, s.Backend, d, hasher)
}

// VerifyAll re-reads every object the backend lists and reports which digests
// no longer match their address (the portable cas.VerifyAll sweep).
func (s *Store) VerifyAll(ctx context.Context, hasher cas.Hasher) (*cas.Report, error) {
	return cas.VerifyAll(ctx, s.Backend, hasher)
}

// Sweep reclaims objects absent from reachable and older than minAge, returning
// the digests it deleted — or would delete, with dryRun.
//
// It uses the backend's native Prune when the backend has one (fs) and the
// portable cas.Sweep otherwise (packfs), so both backends get the same
// mark-and-sweep semantics. op is the subcommand that asked ("gc" or "prune"),
// so an age-gated sweep against a backend with no physical timestamps fails
// with an error naming both the operation and the backend instead of sweeping
// without the grace period.
func (s *Store) Sweep(ctx context.Context, op string, reachable map[string]bool, minAge time.Duration, dryRun bool) ([]cas.Digest, error) {
	if native, ok := s.Backend.(pruner); ok {
		return native.Prune(ctx, reachable, minAge, dryRun)
	}
	if minAge > 0 && !s.Capabilities.Stat {
		return nil, s.unsupported(op)
	}
	return cas.Sweep(ctx, s.Backend, reachable, cas.SweepOptions{MinAge: minAge, DryRun: dryRun})
}

// Clean removes the backend's own orphaned scratch state older than olderThan
// (olderThan <= 0 removes it all). It needs the backend to implement
// cas.Cleaner; a backend that leaves no scratch state (mem) reports
// ErrUnsupported naming the operation.
func (s *Store) Clean(ctx context.Context, olderThan time.Duration) (int, error) {
	cleaner, ok := s.Backend.(cas.Cleaner)
	if !ok {
		return 0, s.unsupported("clean")
	}
	return cleaner.Clean(ctx, olderThan)
}

// Close releases the backend's resources. fs and mem hold none, so Close
// reports nil for them; packfs closes its active pack file here, and the CLI
// reaches Close on every store-operation path, so a writer never leaves the
// append handle open behind it (cli.md §2).
//
// Close is idempotent and returns the backend's close error to every caller.
func (s *Store) Close() error {
	if s == nil || s.Backend == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if closer, ok := s.Backend.(io.Closer); ok {
			s.closeErr = closer.Close()
		}
	})
	return s.closeErr
}
