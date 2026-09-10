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

// Default fan-out parameters, Git-like: <base>/<algo>/<2 hex>/<full hex>.
const (
	DefaultFanOut    = 2
	DefaultFanLevels = 1
	// MaxFanDepth is the fan-out bound: FanLevels × FanOut must not exceed the
	// hex digest width. With the core's one algorithm (sha256, 64 hex chars)
	// this makes a configured layout exactly cover the digest, so hashPath
	// never runs past its end.
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
// <base>/<algorithm>/<fan-out dirs>/<full-hex-digest>.
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

// hashPath returns the on-disk path for h. Every caller has already rejected
// the absent hash (cas.CheckHash), and a present one carries a validated
// lowercase-alphanumeric algorithm name (cas.ParseHash/NewHash), so the
// algorithm is a single safe path element. WithFanOut/WithFanLevels bound
// FanOut × FanLevels to the digest width (MaxFanDepth), so every chunk is in
// range.
func (s *Backend) hashPath(h cas.Hash) string {
	hexDigest := hex.EncodeToString(h.Bytes())
	p := filepath.Join(s.base, h.Algorithm())
	if s.fanOut > 0 && s.fanLevels > 0 {
		for i := 0; i < s.fanLevels; i++ {
			p = filepath.Join(p, hexDigest[i*s.fanOut:(i+1)*s.fanOut])
		}
	}
	return filepath.Join(p, hexDigest)
}

// safeAlgo was removed: Hash is a concrete type with unexported fields, so an
// algorithm name outside `^[a-z0-9]+$` is no longer constructible and the
// path element needs no sanitizing.

// pathToHash rebuilds a Hash from a path relative to the store base.
func pathToHash(rel string) (cas.Hash, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return cas.Hash{}, cas.ErrInvalidHash
	}
	return cas.ParseHash(parts[0] + ":" + parts[len(parts)-1])
}

// Put stores the bytes read from r under h (atomic temp-file write + rename).
func (s *Backend) Put(ctx context.Context, h cas.Hash, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckHash(h, "fs: put"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.hashPath(h)
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
func (s *Backend) Get(ctx context.Context, h cas.Hash) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cas.CheckHash(h, "fs: get"); err != nil {
		return nil, err
	}
	path := s.hashPath(h)
	f, err := openObject(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", cas.ErrNotFound, h)
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
func (s *Backend) Exists(ctx context.Context, h cas.Hash) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := cas.CheckHash(h, "fs: exists"); err != nil {
		return false, err
	}
	_, err := os.Stat(s.hashPath(h))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("cas: stat object: %w", err)
}

// Delete removes the object. A missing object is a no-op.
func (s *Backend) Delete(ctx context.Context, h cas.Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckHash(h, "fs: delete"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.hashPath(h)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cas: delete object: %w", err)
	}
	return nil
}

// Size returns the stored object's size in bytes. A missing object returns
// ErrNotFound. ctx is honored at entry for cancellation.
func (s *Backend) Size(ctx context.Context, h cas.Hash) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := cas.CheckHash(h, "fs: size"); err != nil {
		return 0, err
	}
	fi, err := os.Stat(s.hashPath(h))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("%w: %s", cas.ErrNotFound, h)
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

// List returns every stored hash, filtered by algorithm when algo != "".
// Hashes are rebuilt from their on-disk paths; a path whose algorithm this
// build does not implement (a store written by a build with another algorithm)
// cannot be parsed back into a Hash, so it is skipped rather than reported.
func (s *Backend) List(ctx context.Context, algo string) ([]cas.Hash, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var hashes []cas.Hash
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
		h, err := pathToHash(rel)
		if err != nil {
			return nil
		}
		if algo != "" && h.Algorithm() != algo {
			return nil
		}
		hashes = append(hashes, h)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cas: list objects: %w", err)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].String() < hashes[j].String() })
	return hashes, nil
}

// Stats walks the tree and returns per-algorithm counts and total size. Like
// List, it can only account for objects whose algorithm this build implements;
// others are skipped.
func (s *Backend) Stats(ctx context.Context) (*cas.Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st := &cas.Stats{AlgorithmCounts: map[string]int{}}
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
		h, err := pathToHash(rel)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st.AlgorithmCounts[h.Algorithm()]++
		st.TotalSize += info.Size()
		st.ObjectCount++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cas: stats: %w", err)
	}
	return st, nil
}

// Verify re-reads the object and recomputes its address, streaming so a large
// object is never buffered. It reports ErrHashMismatch when the stored bytes no
// longer hash to h.
func (s *Backend) Verify(ctx context.Context, h cas.Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cas.CheckHash(h, "fs: verify"); err != nil {
		return err
	}
	rc, err := s.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()

	hasher := cas.NewHasher()
	if _, err := io.Copy(hasher, rc); err != nil {
		return fmt.Errorf("cas: verify read: %w", err)
	}
	actual, err := cas.NewHash(hasher.Sum(nil))
	if err != nil {
		return err // unreachable: a sha256 sum always has sha256.Size bytes
	}
	if !actual.Equal(h) {
		return fmt.Errorf("%w: %s", cas.ErrHashMismatch, h)
	}
	return nil
}

// GC performs mark-and-sweep garbage collection.
func (s *Backend) GC(ctx context.Context, reachable map[string]bool) error {
	hashes, err := s.List(ctx, "")
	if err != nil {
		return err
	}
	for _, h := range hashes {
		if !reachable[h.String()] {
			if err := s.Delete(ctx, h); err != nil {
				return err
			}
		}
	}
	return nil
}

// Prune deletes objects not reachable from roots AND older than minAge.
func (s *Backend) Prune(ctx context.Context, roots []cas.Hash, minAge time.Duration, dryRun bool) ([]cas.Hash, error) {
	reachable := make(map[string]bool, len(roots))
	for _, r := range roots {
		reachable[r.String()] = true
	}
	hashes, err := s.List(ctx, "")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var doomed []cas.Hash
	for _, h := range hashes {
		if reachable[h.String()] {
			continue
		}
		info, err := os.Stat(s.hashPath(h))
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < minAge {
			continue
		}
		doomed = append(doomed, h)
	}
	if !dryRun {
		for _, h := range doomed {
			if err := s.Delete(ctx, h); err != nil {
				return nil, err
			}
		}
	}
	return doomed, nil
}
