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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

	// path is the resolved store directory: the -store path with every symbolic
	// link in it followed once, at Open. Every operation is built from it, and
	// Path reports it to a command that names the directory it acted on.
	path string

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
//
// The path is resolved once, before the backend is opened (ResolveBase), and the
// backend is opened over the resolved directory. A symbolic link standing where
// -store points is therefore followed deliberately and visibly: every operation
// runs on the directory the link names, and Store.Path reports that directory so
// a destructive command can print the tree it acted on.
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
	resolved, err := ResolveBase(opts.Path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case KindFS:
		backend, err := fsbackend.New(resolved)
		if err != nil {
			return nil, err
		}
		return newStore(kind, resolved, backend), nil
	case KindPackFS:
		// Packing is the point of selecting packfs: every Put also appends to
		// the active pack file, so the CLI maintains a genuinely packed store.
		backend, err := packfs.New(resolved, packfs.WithEnabled())
		if err != nil {
			return nil, err
		}
		return newStore(kind, resolved, backend), nil
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
// filesystem view cannot serve it. A packed object has no file of its own to
// stat — packfs keeps a loose copy at write time and the payload in an
// append-only pack, so "size" and "mod time" would describe whichever of the two
// the viewer picked — and selecting packs is therefore refused with
// viewerRefusal rather than silently reading a different directory than -store
// named, or serving numbers about a pack file instead of the object (cli.md §1,
// §2).
func OpenViewer(ctx context.Context, opts Options) (*fsbackend.Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind, err := ParseKind(string(opts.Kind))
	if err != nil {
		return nil, err
	}
	if kind != KindFS {
		return nil, viewerRefusal(kind)
	}
	if opts.Path == "" {
		return nil, errors.New("cask: store path is required")
	}
	// The same resolution Open performs, so the viewer reads the directory the
	// resolved -store names and not whatever a link at that path points at
	// silently (cli.md §1, §2; backend.BasePath reports it).
	resolved, err := ResolveBase(opts.Path)
	if err != nil {
		return nil, err
	}
	return fsbackend.New(resolved)
}

// ResolveBase returns the physical store directory base names: every symbolic
// link in the path is followed once, here, so the store the CLI opens and the
// tree its destructive sweeps act on are the directory the operator is really
// pointing at rather than a link standing in for it. Following is deliberate and
// reported — clean/gc/prune and the viewer print the resolved base — not silent.
//
// The last component of base need not exist: the CLI creates a store directory
// on first use, so the longest existing prefix is resolved and the missing tail
// is appended unchanged (a path that is not there cannot be a link). An empty or
// whitespace-only base is returned as it is — the caller rejects it — and a path
// that cannot be resolved at all (an unreadable component, a regular file where
// a directory is needed) is an error rather than a half-resolved base.
//
// It performs I/O and no mutation: nothing is created, and the one-time cost is
// paid where a store is opened, not on any operation.
func ResolveBase(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return base, nil
	}
	cur := filepath.Clean(base)
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if len(tail) == 0 {
				return resolved, nil
			}
			// The missing tail can only be created beneath a directory, and the
			// platforms disagree about how they report the alternative: POSIX
			// fails the whole lookup with ENOTDIR, while Windows resolves a
			// regular file and leaves the child to fail later. Checking here
			// keeps the answer the same on both.
			info, statErr := os.Stat(resolved)
			if statErr != nil {
				return "", fmt.Errorf("cask: resolve store base %q: %w", base, statErr)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("cask: resolve store base %q: %s is not a directory", base, resolved)
			}
			return filepath.Join(append([]string{resolved}, tail...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("cask: resolve store base %q: %w", base, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Nothing on the path exists (a missing volume root): there is no
			// link to follow, and the backend's own creation step reports what
			// is wrong with the path.
			return filepath.Clean(base), nil
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

// viewerRefusal is the viewer's one refusal: it wraps cas.ErrUnsupported (so a
// caller still classifies it with errors.Is), names the operation and the
// backend, and tells the operator what to do instead. Creating a packed store
// with `cask -backend packfs` and then being unable to inspect it is the likely
// support question, so the message answers it where the operator reads it
// (cli.md §1, §2).
func viewerRefusal(kind Kind) error {
	return fmt.Errorf("cask: web is not supported by the %q backend: the viewer reads loose objects only (per-object size and mod time, no pack index), so open a loose store with -backend %s or -store with a loose store directory: %w",
		kind, KindFS, cas.ErrUnsupported)
}

// newStore wraps an opened backend with the maintenance capabilities the CLI
// consults. path is the resolved store directory the backend was opened over.
func newStore(kind Kind, path string, backend cas.Backend) *Store {
	return &Store{
		Backend:      backend,
		Capabilities: cas.CapabilitiesOf(backend),
		Kind:         kind,
		path:         path,
	}
}

// Path returns the store directory this store was opened over, with every
// symbolic link in the -store path resolved once at Open: the directory the
// backend's bytes really live in, and the one a maintenance command names before
// it sweeps (cli.md §2). It is the resolved form of Options.Path, not the string
// the operator typed — an intentional symlinked store keeps working, and the
// redirection is visible instead of silent.
//
// A Store assembled by hand — a test, a decorator — carries no recorded path and
// reports its backend's BasePath instead, so a caller that only opened a backend
// still gets the directory its bytes live under (for packfs that is the loose
// tree, since its pack files are an append-only mirror).
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	if s.path != "" {
		return s.path
	}
	if reporter, ok := s.Backend.(interface{ BasePath() string }); ok {
		return reporter.BasePath()
	}
	return ""
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
