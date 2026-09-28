// Package fs provides the filesystem Backend for the cas core.
package fs

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/internal/atomicfile"
)

// Default fan-out parameters, Git-like: <base>/<2 hex>/<full hex>.
const (
	// DefaultFanOut is the default number of digest characters per directory.
	DefaultFanOut = 2
	// DefaultFanLevels is the default number of fan-out directory levels.
	DefaultFanLevels = 1
	// MaxFanDepth is the fan-out bound: FanLevels × FanOut must not exceed the
	// hex digest width. Go-cask's own clients digest with sha256 (64 hex chars),
	// so a configured layout normally covers the digest exactly. A client whose
	// digest is shorter must keep FanOut × FanLevels within its own width: the
	// backend cannot know the algorithm, but it CAN see the key it is handed, so
	// a key too short for the configured layout is rejected with
	// ErrInvalidDigest (checkKey) instead of being sliced out of range.
	MaxFanDepth = 64
)

// config is the filesystem backend's own configuration. Option is a func over
// this concrete type, so only fs options can configure fs — passing another
// backend's option is a compile-time error rather than a no-op.
type config struct {
	fanOut    int
	fanLevels int
	dirSync   bool
}

// Option configures a filesystem Backend.
type Option func(*config)

// WithFanOut sets the number of hex characters per fan-out directory level.
// 0 means "flat" (no fan-out directories).
func WithFanOut(n int) Option {
	return func(c *config) { c.fanOut = n }
}

// WithFanLevels sets the number of fan-out directory levels. 0 means "flat".
func WithFanLevels(n int) Option {
	return func(c *config) { c.fanLevels = n }
}

// WithDirSync enables a best-effort fsync of the parent directory after the
// atomic rename that publishes an object.
func WithDirSync() Option {
	return func(c *config) { c.dirSync = true }
}

// Backend is the filesystem backend: each object is one file under
// <base>/<fan-out dirs>/<full-hex-digest>. There is no algorithm directory —
// the core names no algorithm (cas-core §4.2).
type Backend struct {
	base      string
	fanOut    int
	fanLevels int
	dirSync   bool

	// walk traverses the store tree for Clean, List and Stats. It is a
	// struct-held seam: a nil field means filepath.WalkDir, so a zero-value
	// Backend stays safe to use, and a test can install a scripted walk to
	// reach the branches a real scan hits only when an entry vanishes between
	// the directory read and the stat (a concurrent Delete/Put) — a race no
	// test can win on purpose. It lives on the struct rather than in a
	// package-level variable so two backends in one process cannot interfere
	// (cas/backend/packfs keeps its seams the same way).
	walk func(root string, fn fs.WalkDirFunc) error

	mu sync.Mutex // Put/Delete only
}

// Compile-time check that Backend satisfies the cas.Backend interface
// (including Stats), so dropping a method breaks this package's build. The
// optional capabilities are named too: this backend opts into both metadata
// interfaces, and cas.PhysicalStatter must stay the combined form of
// cas.Statter's two calls (go-cask#373).
var _ cas.Backend = (*Backend)(nil)
var _ cas.Cleaner = (*Backend)(nil)
var _ cas.Statter = (*Backend)(nil)
var _ cas.PhysicalStatter = (*Backend)(nil)

// New creates a filesystem backend rooted at basePath, creating the
// directory tree. Options default to the Git-like fan-out (2,1).
//
// basePath is checked with ValidateBase before anything is created, because the
// base belongs to exactly this one backend: List/Stats report every digest-named
// file beneath it at any depth and Clean reclaims every *.tmp beneath it
// (cas-core §4.4). A path that would make the backend own more than the caller
// named — "", ".", "..", a parent-traversal path or a volume root — is rejected
// and nothing is created. A nested directory is a valid base: only the caller
// knows whether it already belongs to another store.
func New(basePath string, opts ...Option) (*Backend, error) {
	cfg := config{fanOut: DefaultFanOut, fanLevels: DefaultFanLevels}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.fanOut < 0 || cfg.fanLevels < 0 {
		return nil, fmt.Errorf("cas: negative fan-out parameters (fanOut=%d, fanLevels=%d)", cfg.fanOut, cfg.fanLevels)
	}
	if cfg.fanOut*cfg.fanLevels > MaxFanDepth {
		return nil, fmt.Errorf("cas: fan-out %d×%d exceeds max depth %d", cfg.fanOut, cfg.fanLevels, MaxFanDepth)
	}
	if err := ValidateBase(basePath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("cas: create store base: %w", err)
	}
	return &Backend{base: basePath, fanOut: cfg.fanOut, fanLevels: cfg.fanLevels, dirSync: cfg.dirSync}, nil
}

// BasePath returns the directory this backend's objects live under: the path
// passed to New, where Get/Put/List/Stats/Clean all operate. A maintenance layer
// above the backend needs it (cas/verify/sidecar stores its records there), and
// a decorator reports the same path so a second layer cannot silently point at
// another store.
func (s *Backend) BasePath() string { return s.base }

// walkDir walks root, using the installed walk seam when a test set one and
// filepath.WalkDir otherwise. The nil fallback (rather than a field filled in
// by New) keeps a Backend a test builds by hand — see FuzzPathRoundTrip — and
// the zero value safe to walk.
func (s *Backend) walkDir(root string, fn fs.WalkDirFunc) error {
	if s.walk == nil {
		return filepath.WalkDir(root, fn)
	}
	return s.walk(root, fn)
}

// addressable reports whether d is long enough for the configured layout: its
// hex form must fill FanLevels chunks of FanOut characters. The rule is a
// property of the layout, not of an algorithm, so the backend can apply it
// without knowing which hasher produced the key.
func (s *Backend) addressable(d cas.Digest) bool {
	return len(d)*2 >= s.fanOut*s.fanLevels
}

// checkKey validates a caller-supplied key: present (cas.CheckDigest) and long
// enough for the configured layout (addressable), because digestPath slices its
// hex form. A key that cannot name an object is ErrInvalidDigest, exactly like
// an absent one — the backend must never panic on a malformed key, and a client
// with a digest shorter than the layout (the core names no algorithm, so any
// width is legal) gets a clear error instead of a slice-bounds panic.
func (s *Backend) checkKey(d cas.Digest, what string) error {
	if err := cas.CheckDigest(d, what); err != nil {
		return err
	}
	if !s.addressable(d) {
		return fmt.Errorf("%w: %s: digest has %d hex chars, layout %d×%d needs %d",
			cas.ErrInvalidDigest, what, len(d)*2, s.fanOut, s.fanLevels, s.fanOut*s.fanLevels)
	}
	return nil
}

// digestPath returns the on-disk path for a digest: the store base, then the
// fan-out directory chunks, then the lowercase-hex digest as the file name.
// There is no algorithm directory — the backend does not know which algorithm
// produced a key (cas-core §4.2) — and a digest is hex by construction
// (cas.Digest.UnmarshalText), so no path element needs sanitizing.
// The caller MUST have admitted the key first (checkKey, or addressable in a
// sweep), so every chunk is in range.
func (s *Backend) digestPath(d cas.Digest) string {
	hexDigest := hex.EncodeToString(d)
	p := s.base
	if s.fanOut > 0 && s.fanLevels > 0 {
		for i := 0; i < s.fanLevels; i++ {
			p = filepath.Join(p, hexDigest[i*s.fanOut:(i+1)*s.fanOut])
		}
	}
	return filepath.Join(p, hexDigest)
}

// pathToDigest rebuilds a digest from a path relative to the store base: the
// last element is the hex digest, any leading elements are fan-out chunks.
// strings.Split always yields at least one element, so only the digest itself
// can be malformed.
func pathToDigest(rel string) (cas.Digest, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return cas.ParseDigest(parts[len(parts)-1])
}

// Put stores the bytes read from r under d (atomic temp-file write + rename).
//
// The publish itself — temp file, fsync, rename, and the optional
// parent-directory fsync — is atomicfile.Publish (go-cask#339), the one
// implementation cas/backend/fs, cas/pack and cas/refs share. This method keeps
// the backend's own decisions: the object's mode, the idempotent-rename rule
// below, and the operator-facing error text for the phase that failed.
func (s *Backend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKey(d, "fs: put"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	err := atomicfile.Publish(ctx, s.digestPath(d), r, atomicfile.Options{
		Mode:    0o644,
		SyncDir: s.dirSync,
		// The content address makes the object already stored, so an existing
		// regular file satisfies an idempotent Put (cas-core §4.4). On Windows
		// the rename can also fail because another reader holds the file open
		// — the same answer, for the same reason.
		ExistingIsSuccess: true,
	})
	if err != nil {
		return putError(err)
	}
	return nil
}

// putError names the phase of a failed atomicfile.Publish in the backend's
// operator-facing vocabulary, so moving the publish into the shared
// implementation changed no message this backend reports (go-cask#339).
func putError(err error) error {
	phase, cause, ok := atomicfile.FailedPhase(err)
	if !ok {
		return err
	}
	switch phase {
	case atomicfile.PhaseDir:
		return fmt.Errorf("cas: create object dir: %w", cause)
	case atomicfile.PhaseTemp:
		return fmt.Errorf("cas: create temp file: %w", cause)
	case atomicfile.PhaseWrite:
		return fmt.Errorf("cas: write object: %w", cause)
	case atomicfile.PhaseSync:
		return fmt.Errorf("cas: sync object: %w", cause)
	case atomicfile.PhaseClose:
		return fmt.Errorf("cas: close object: %w", cause)
	case atomicfile.PhasePublish:
		return fmt.Errorf("cas: publish object: %w", cause)
	case atomicfile.PhaseDirSync:
		return fmt.Errorf("cas: sync object dir: %w", cause)
	default:
		return err
	}
}

// Get returns a stream of the object's bytes; the caller MUST close it.
func (s *Backend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkKey(d, "fs: get"); err != nil {
		return nil, err
	}
	path := s.digestPath(d)
	f, err := openWithRetry(os.Open, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", cas.ErrNotFound, d)
		}
		return nil, fmt.Errorf("cas: open object: %w", err)
	}
	return f, nil
}

// openWithRetry opens an object file for reading, retrying briefly while the
// file exists but cannot be opened. On Windows a concurrent atomic rename makes
// the destination momentarily unopenable ("access is denied" / "being used by
// another process"), so a lock-free reader may need a moment before it sees the
// old or the new file (cas-core §4.4 rename caveat). A missing file is reported
// immediately. open is injectable so the transient-failure path is testable on
// every platform.
func openWithRetry(open func(string) (*os.File, error), path string) (*os.File, error) {
	const attempts = 20
	var err error
	for range attempts {
		var f *os.File
		f, err = open(path)
		if err == nil {
			return f, nil
		}
		if isNotExist(err) {
			return nil, err
		}
		if _, statErr := os.Stat(path); statErr != nil {
			return nil, err // gone or unreadable: not transient contention
		}
		time.Sleep(time.Millisecond)
	}
	return nil, err
}

func isNotExist(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err))
}

// Exists reports whether the object is stored. Lock-free.
func (s *Backend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := s.checkKey(d, "fs: exists"); err != nil {
		return false, err
	}
	_, err := os.Stat(s.digestPath(d))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("cas: stat object: %w", err)
}

// Delete removes the object. A missing object is a no-op.
func (s *Backend) Delete(ctx context.Context, d cas.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKey(d, "fs: delete"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.digestPath(d)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cas: delete object: %w", err)
	}
	return nil
}

// physicalStat is the one os.Stat behind Size, ModTime and Stat: what names the
// caller in a key error (the guard's message is operator output, so Size and
// ModTime keep their own names), and the two values every one of them reads.
// A missing object is ErrNotFound, every other failure is the stat's own.
func (s *Backend) physicalStat(ctx context.Context, d cas.Digest, what string) (int64, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return 0, time.Time{}, err
	}
	if err := s.checkKey(d, what); err != nil {
		return 0, time.Time{}, err
	}
	fi, err := os.Stat(s.digestPath(d))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, time.Time{}, fmt.Errorf("%w: %s", cas.ErrNotFound, d)
		}
		return 0, time.Time{}, fmt.Errorf("cas: stat object: %w", err)
	}
	return fi.Size(), fi.ModTime(), nil
}

// Size returns the stored object's size in bytes. A missing object returns
// ErrNotFound. ctx is honored at entry for cancellation.
func (s *Backend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	size, _, err := s.physicalStat(ctx, d, "fs: size")
	return size, err
}

// ModTime returns the filesystem modification time of a stored object. This
// is physical backend metadata, not a content-addressed object field.
func (s *Backend) ModTime(ctx context.Context, d cas.Digest) (time.Time, error) {
	_, written, err := s.physicalStat(ctx, d, "fs: mod time")
	return written, err
}

// Stat returns the stored object's size and its filesystem modification time
// from one os.Stat, so a caller that needs both does not stat the same path
// twice (cas.PhysicalStatter, go-cask#373).
func (s *Backend) Stat(ctx context.Context, d cas.Digest) (int64, time.Time, error) {
	return s.physicalStat(ctx, d, "fs: stat")
}

// Clean removes orphan temp files (crash leftovers) older than olderThan
// (olderThan <= 0 removes them all). It removes both "<hex>.tmp" and the
// collision fallbacks "<hex>.tmp.<n>" that atomicfile.Publish may leave behind.
// Walk and removal errors are returned, not swallowed.
//
// The sweep is cleanTemp (policy.go), the one implementation of the temp-file
// convention: Clean runs it over this backend's base with this backend's walk
// seam, and fs.CleanTemp runs the same rules over another tree that follows the
// same convention (cas/backend/packfs sweeps its pack directory with it).
func (s *Backend) Clean(ctx context.Context, olderThan time.Duration) (int, error) {
	return cleanTemp(ctx, s.base, olderThan, s.walk)
}

// isTempFile reports whether name is an object temp file: "<hex>.tmp" or a
// collision fallback "<hex>.tmp.<n>" (atomicfile.createTemp). The predicate is
// atomicfile.IsTempFile, the one rule the packages that publish a file through
// that package share (go-cask#339), so this backend's sweep and cas/refs's
// listing cannot drift on what counts as scratch.
func isTempFile(name string) bool {
	return atomicfile.IsTempFile(name)
}

// List returns every stored digest, sorted. Digests are rebuilt from their
// on-disk paths; a file whose name is not a hex digest (a foreign file, a temp
// leftover) is skipped rather than reported. A path the walk cannot make
// relative to the store base is skipped the same way. An entry that vanishes
// mid-walk (a concurrent Delete) is tolerated, because the list it produced is
// still a truthful snapshot of what was there; every other walk failure is
// returned rather than reported as a short list. The backend cannot filter by
// algorithm: it does not know which one produced a key (cas-core §4.2).
func (s *Backend) List(ctx context.Context) ([]cas.Digest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.base); err != nil {
		return nil, fmt.Errorf("cas: list objects: %w", err)
	}
	var digests []cas.Digest
	err := s.walkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if isNotExist(err) {
				return nil // entry vanished during concurrent mutation
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.base, path)
		if err != nil {
			return nil
		}
		digest, err := pathToDigest(rel)
		if err != nil {
			return nil
		}
		digests = append(digests, digest)
		return nil
	})
	if err != nil {
		if isNotExist(err) {
			return digests, nil
		}
		return nil, fmt.Errorf("cas: list objects: %w", err)
	}
	// Hex order equals byte order (the hex alphabet '0'-'9','a'-'f' is ordinal
	// and nibble-preserving), so comparing raw digest bytes sorts exactly like
	// comparing the rendered hex strings the tests assert on — without
	// allocating two strings per comparison.
	slices.SortFunc(digests, func(a, b cas.Digest) int { return bytes.Compare(a, b) })
	return digests, nil
}

// Stats walks the tree and returns the object count and total size. An entry or
// object that vanishes mid-walk — a concurrent Delete — is skipped, because the
// store allows deletion while readers run and a walk cannot assume the tree
// holds still; any other walk or stat failure is returned rather than silently
// undercounting the store.
func (s *Backend) Stats(ctx context.Context) (*cas.Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.base); err != nil {
		return nil, fmt.Errorf("cas: stats: %w", err)
	}
	st := &cas.Stats{}
	err := s.walkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if isNotExist(err) {
				return nil // file vanished during concurrent mutation
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.base, path)
		if err != nil {
			return nil
		}
		if _, err := pathToDigest(rel); err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if isNotExist(err) {
				return nil // file vanished during concurrent Delete/Put; treat as transient
			}
			return err
		}
		st.TotalSize += info.Size()
		st.ObjectCount++
		return nil
	})
	if err != nil {
		if isNotExist(err) {
			return st, nil
		}
		return nil, fmt.Errorf("cas: stats: %w", err)
	}
	return st, nil
}

// Verify re-reads the object and recomputes its digest with the client's
// Hasher, streaming so a large object never buffered. It reports
// ErrDigestMismatch when the stored bytes no longer digest to d.
func (s *Backend) Verify(ctx context.Context, d cas.Digest, hasher cas.Hasher) error {
	if s == nil {
		return fmt.Errorf("cas: verify: nil backend")
	}
	if err := s.checkKey(d, "fs: verify"); err != nil {
		return err
	}
	return cas.Verify(ctx, s, d, hasher)
}

// GC performs mark-and-sweep garbage collection: every addressable digest not
// present in reachable is deleted. reachable MUST already be the complete,
// transitively-closed set of live digests (every object still needed, not
// just entry-point roots) — GC never follows References() itself. Passing
// only roots silently deletes anything they reference; use cas.Reachable (or
// an equivalent typed walk over Object[T].References()) to expand roots into
// the reachable set first.
func (s *Backend) GC(ctx context.Context, reachable map[string]bool) error {
	digests, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !s.addressable(d) {
			continue // a digest-named file this layout cannot address: List reports it, no sweep may touch it (cas-core §4.4)
		}
		if !reachable[d.String()] {
			if err := s.Delete(ctx, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// Prune deletes objects absent from reachable AND older than minAge (age
// gives a concurrent writer's fresh, not-yet-referenced objects a grace
// period). Like GC, reachable MUST already be the complete,
// transitively-closed set of live digests — Prune never follows
// References() itself. Passing only entry-point roots silently deletes
// everything they reference; use cas.Reachable (or an equivalent typed walk)
// to expand roots into the reachable set first.
func (s *Backend) Prune(ctx context.Context, reachable map[string]bool, minAge time.Duration, dryRun bool) ([]cas.Digest, error) {
	digests, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var doomed []cas.Digest
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !s.addressable(d) {
			continue // not an object of this layout: no canonical path to age-check or delete
		}
		if reachable[d.String()] {
			continue
		}
		info, err := os.Stat(s.digestPath(d))
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < minAge {
			continue
		}
		doomed = append(doomed, d)
	}
	if !dryRun {
		for _, d := range doomed {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := s.Delete(ctx, d); err != nil {
				return nil, err
			}
		}
	}
	return doomed, nil
}
