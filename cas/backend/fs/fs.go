// Package fs provides the filesystem Backend for the cas core.
package fs

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
)

// Default fan-out parameters, Git-like: <base>/<2 hex>/<full hex>.
const (
	DefaultFanOut    = 2
	DefaultFanLevels = 1
	// MaxFanDepth is the fan-out bound: FanLevels × FanOut must not exceed the
	// hex digest width. Go-cask's own clients digest with sha256 (64 hex chars),
	// so this makes a configured layout cover the digest exactly and digestPath
	// never runs past its end. A client whose digest is shorter must keep
	// FanOut × FanLevels within its own width.
	MaxFanDepth = 64
)

// config is the filesystem backend's own configuration, applied via
// backend.Option functions. Options are backend-specific; memory and s3
// define their own config types.
type config struct {
	fanOut    int
	fanLevels int
	dirSync   bool
}

// WithFanOut sets the number of hex characters per fan-out directory level.
// 0 means "flat" (no fan-out directories).
func WithFanOut(n int) backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.fanOut = n
		}
	}
}

// WithFanLevels sets the number of fan-out directory levels. 0 means "flat".
func WithFanLevels(n int) backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.fanLevels = n
		}
	}
}

// WithDirSync enables a best-effort fsync of the parent directory after the
// atomic rename that publishes an object.
func WithDirSync() backend.Option {
	return func(cfg any) {
		if c, ok := cfg.(*config); ok {
			c.dirSync = true
		}
	}
}

// Backend is the filesystem backend: each object is one file under
// <base>/<fan-out dirs>/<full-hex-digest>. There is no algorithm directory —
// the core names no algorithm (cas-core §4.2).
type Backend struct {
	base      string
	fanOut    int
	fanLevels int
	dirSync   bool

	mu sync.Mutex // Put/Delete only
}

// Compile-time check that Backend satisfies the cas.Backend interface
// (including Stats), so dropping a method breaks this package's build.
var _ cas.Backend = (*Backend)(nil)

// New creates a filesystem backend rooted at basePath, creating the
// directory tree. Options default to the Git-like fan-out (2,1).
func New(basePath string, opts ...backend.Option) (*Backend, error) {
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
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("cas: create store base: %w", err)
	}
	return &Backend{base: basePath, fanOut: cfg.fanOut, fanLevels: cfg.fanLevels, dirSync: cfg.dirSync}, nil
}

// syncParentDir fsyncs the directory containing path.
func syncParentDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// digestPath returns the on-disk path for a digest: the store base, then the
// fan-out directory chunks, then the lowercase-hex digest as the file name.
// There is no algorithm directory — the backend does not know which algorithm
// produced a key (cas-core §4.2) — and a digest is hex by construction
// (cas.Digest.UnmarshalText), so no path element needs sanitizing.
// WithFanOut/WithFanLevels bound FanOut × FanLevels to the digest width
// (MaxFanDepth), so every chunk is in range.
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
func (s *Backend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "fs: put"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.digestPath(d)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cas: create object dir: %w", err)
	}
	f, tmp, err := createTempExcl(path)
	if err != nil {
		return fmt.Errorf("cas: create temp file: %w", err)
	}
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if _, err := io.Copy(f, ctxReader{ctx: ctx, r: r}); err != nil {
		cleanup()
		return fmt.Errorf("cas: write object: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("cas: sync object: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cas: close object: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows cannot always replace a file another reader has open; the
		// content address makes the object already stored, so an existing
		// regular file satisfies an idempotent Put (cas-core §4.4). Anything
		// else at the path (a directory, a device) is a real failure.
		if fi, statErr := os.Stat(path); statErr == nil && fi.Mode().IsRegular() {
			os.Remove(tmp)
			return nil
		}
		os.Remove(tmp)
		return fmt.Errorf("cas: publish object: %w", err)
	}
	if s.dirSync {
		if err := syncParentDir(path); err != nil {
			return fmt.Errorf("cas: sync object dir: %w", err)
		}
	}
	return nil
}

// ctxReader aborts a copy once ctx is canceled, so a canceled Put does not
// keep streaming and publishing an object it no longer needs (Get honors
// context at entry only, because a returned reader outlives the call).
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// createTempExcl creates a uniquely named temp file for an object write.
func createTempExcl(path string) (*os.File, string, error) {
	base := path + ".tmp"
	f, err := os.OpenFile(base, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		return f, base, nil
	}
	if !os.IsExist(err) {
		return nil, "", err
	}
	for i := 1; i < 10000; i++ {
		name := fmt.Sprintf("%s.%d", base, i)
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return f, name, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("temp name exhausted for %s", path)
}

// Get returns a stream of the object's bytes; the caller MUST close it.
func (s *Backend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cas.CheckDigest(d, "fs: get"); err != nil {
		return nil, err
	}
	path := s.digestPath(d)
	f, err := openObject(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", cas.ErrNotFound, d)
		}
		return nil, fmt.Errorf("cas: open object: %w", err)
	}
	return f, nil
}

// openObject opens an object file for reading, retrying briefly while the file
// exists but cannot be opened. On Windows a concurrent atomic rename makes the
// destination momentarily unopenable ("access is denied" / "being used by
// another process"), so a lock-free reader may need a moment before it sees
// the old or the new file (cas-core §4.4 rename caveat). A missing file is
// reported immediately.
func openObject(path string) (*os.File, error) {
	return openWithRetry(os.Open, path)
}

// openWithRetry implements openObject with an injectable open function, so the
// transient-failure path is testable on every platform.
func openWithRetry(open func(string) (*os.File, error), path string) (*os.File, error) {
	const attempts = 20
	var err error
	for i := 0; i < attempts; i++ {
		var f *os.File
		f, err = open(path)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if _, statErr := os.Stat(path); statErr != nil {
			return nil, err // gone or unreadable: not transient contention
		}
		time.Sleep(time.Millisecond)
	}
	return nil, err
}

// Exists reports whether the object is stored. Lock-free.
func (s *Backend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := cas.CheckDigest(d, "fs: exists"); err != nil {
		return false, err
	}
	_, err := os.Stat(s.digestPath(d))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("cas: stat object: %w", err)
}

// Delete removes the object. A missing object is a no-op.
func (s *Backend) Delete(ctx context.Context, d cas.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "fs: delete"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.digestPath(d)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cas: delete object: %w", err)
	}
	return nil
}

// Size returns the stored object's size in bytes. A missing object returns
// ErrNotFound. ctx is honored at entry for cancellation.
func (s *Backend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := cas.CheckDigest(d, "fs: size"); err != nil {
		return 0, err
	}
	fi, err := os.Stat(s.digestPath(d))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("%w: %s", cas.ErrNotFound, d)
		}
		return 0, fmt.Errorf("cas: stat object: %w", err)
	}
	return fi.Size(), nil
}

// Clean removes orphan temp files (crash leftovers) older than olderThan
// (olderThan <= 0 removes them all). It removes both "<hex>.tmp" and the
// collision fallbacks "<hex>.tmp.<n>" that createTempExcl may leave behind.
// Walk and removal errors are returned, not swallowed.
func (s *Backend) Clean(ctx context.Context, olderThan time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err := filepath.WalkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isTempFile(d.Name()) {
			return nil
		}
		if olderThan > 0 {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if fi.ModTime().After(cutoff) {
				return nil
			}
		}
		if err := os.Remove(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // removed by a concurrent sweep
			}
			return err
		}
		removed++
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return removed, nil // the store directory itself is gone: nothing to clean
		}
		return removed, fmt.Errorf("cas: clean: %w", err)
	}
	return removed, nil
}

// isTempFile reports whether name is an object temp file: "<hex>.tmp" or a
// collision fallback "<hex>.tmp.<n>" (createTempExcl).
func isTempFile(name string) bool {
	i := strings.Index(name, ".tmp")
	if i < 0 {
		return false
	}
	rest := name[i+len(".tmp"):]
	if rest == "" {
		return true
	}
	if rest[0] != '.' {
		return false
	}
	_, err := strconv.Atoi(rest[1:])
	return err == nil
}

// List returns every stored digest, sorted. Digests are rebuilt from their
// on-disk paths; a file whose name is not a hex digest (a foreign file, a temp
// leftover) is skipped rather than reported. The backend cannot filter by
// algorithm: it does not know which one produced a key (cas-core §4.2).
func (s *Backend) List(ctx context.Context) ([]cas.Digest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var digests []cas.Digest
	err := filepath.WalkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
		return nil, fmt.Errorf("cas: list objects: %w", err)
	}
	sort.Slice(digests, func(i, j int) bool { return digests[i].String() < digests[j].String() })
	return digests, nil
}

// Stats walks the tree and returns the object count and total size.
func (s *Backend) Stats(ctx context.Context) (*cas.Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st := &cas.Stats{}
	err := filepath.WalkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
			return nil
		}
		st.TotalSize += info.Size()
		st.ObjectCount++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cas: stats: %w", err)
	}
	return st, nil
}

// Verify re-reads the object and recomputes its digest with the client's
// Hasher, streaming so a large object never buffered. It reports
// ErrDigestMismatch when the stored bytes no longer digest to d.
func (s *Backend) Verify(ctx context.Context, d cas.Digest, hasher cas.Hasher) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckDigest(d, "fs: verify"); err != nil {
		return err
	}
	if err := hasher.Validate(d); err != nil {
		return err
	}
	rc, err := s.Get(ctx, d)
	if err != nil {
		return err
	}
	defer rc.Close()

	actual, err := hasher.Digest(rc)
	if err != nil {
		return fmt.Errorf("cas: verify read: %w", err)
	}
	if !actual.Equal(d) {
		return fmt.Errorf("%w: %s", cas.ErrDigestMismatch, d)
	}
	return nil
}

// GC performs mark-and-sweep garbage collection.
func (s *Backend) GC(ctx context.Context, reachable map[string]bool) error {
	digests, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, d := range digests {
		if !reachable[d.String()] {
			if err := s.Delete(ctx, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// Prune deletes objects not reachable from roots AND older than minAge.
func (s *Backend) Prune(ctx context.Context, roots []cas.Digest, minAge time.Duration, dryRun bool) ([]cas.Digest, error) {
	reachable := make(map[string]bool, len(roots))
	for _, r := range roots {
		reachable[r.String()] = true
	}
	digests, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var doomed []cas.Digest
	for _, d := range digests {
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
			if err := s.Delete(ctx, d); err != nil {
				return nil, err
			}
		}
	}
	return doomed, nil
}
