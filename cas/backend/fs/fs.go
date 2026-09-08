// Package fs provides the filesystem Backend for the cas core.
package fs

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	// MaxFanDepth is the fan-out bound: FanLevels × FanOut must not exceed
	// the hex digest length.
	MaxFanDepth = 64
)

// Backend is the filesystem backend: each object is one file under
// <base>/<algorithm>/<fan-out dirs>/<full-hex-digest>.
type Backend struct {
	base    string
	cfg     backend.Config
	options []backend.Option

	mu sync.Mutex // Put/Delete only
}

// New creates a filesystem backend rooted at basePath, creating the
// directory tree. Options default to the Git-like fan-out (2,1).
func New(basePath string, opts ...backend.Option) (*Backend, error) {
	cfg := backend.Config{FanOut: DefaultFanOut, FanLevels: DefaultFanLevels}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.FanOut < 0 || cfg.FanLevels < 0 {
		return nil, fmt.Errorf("cas: negative fan-out parameters (fanOut=%d, fanLevels=%d)", cfg.FanOut, cfg.FanLevels)
	}
	if cfg.FanOut*cfg.FanLevels > MaxFanDepth {
		return nil, fmt.Errorf("cas: fan-out %d×%d exceeds max depth %d", cfg.FanOut, cfg.FanLevels, MaxFanDepth)
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("cas: create store base: %w", err)
	}
	return &Backend{base: basePath, cfg: cfg}, nil
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

// hashPath returns the on-disk path for h.
func (s *Backend) hashPath(h cas.Hash) string {
	hexDigest := hex.EncodeToString(h.Bytes())
	p := filepath.Join(s.base, h.Algorithm())
	if s.cfg.FanOut > 0 && s.cfg.FanLevels > 0 {
		for i := 0; i < s.cfg.FanLevels; i++ {
			start := i * s.cfg.FanOut
			if start >= len(hexDigest) {
				break
			}
			end := start + s.cfg.FanOut
			if end > len(hexDigest) {
				end = len(hexDigest)
			}
			p = filepath.Join(p, hexDigest[start:end])
		}
	}
	return filepath.Join(p, hexDigest)
}

// pathToHash rebuilds a Hash from a path relative to the store base.
func pathToHash(rel string) (cas.Hash, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return nil, cas.ErrInvalidHash
	}
	return cas.ParseHash(parts[0] + ":" + parts[len(parts)-1])
}

// Put stores the bytes read from r under h (atomic temp-file write + rename).
func (s *Backend) Put(ctx context.Context, h cas.Hash, r io.Reader) error {
	if err := ctx.Err(); err != nil {
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
	if _, err := io.Copy(f, r); err != nil {
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
		os.Remove(tmp)
		return fmt.Errorf("cas: publish object: %w", err)
	}
	if s.cfg.DirSync {
		if err := syncParentDir(path); err != nil {
			return fmt.Errorf("cas: sync object dir: %w", err)
		}
	}
	return nil
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
	f, err := os.Open(s.hashPath(h))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", cas.ErrNotFound, h)
		}
		return nil, fmt.Errorf("cas: open object: %w", err)
	}
	return f, nil
}

// Exists reports whether the object is stored. Lock-free.
func (s *Backend) Exists(ctx context.Context, h cas.Hash) (bool, error) {
	if err := ctx.Err(); err != nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.hashPath(h)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cas: delete object: %w", err)
	}
	return nil
}

// Clean removes leftover *.tmp files older than olderThan.

// Size returns the stored object's size in bytes. A missing object returns
// ErrNotFound. ctx is honored at entry for cancellation.
func (s *Backend) Size(ctx context.Context, h cas.Hash) (int64, error) {
	if err := ctx.Err(); err != nil {
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

func (s *Backend) Clean(ctx context.Context, olderThan time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err := filepath.WalkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}
		if olderThan > 0 {
			fi, err := d.Info()
			if err != nil || fi.ModTime().After(cutoff) {
				return nil
			}
		}
		if err := os.Remove(path); err == nil {
			removed++
		}
		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("cas: clean: %w", err)
	}
	return removed, nil
}

// List returns every stored hash, filtered by algorithm when algo != "".
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

// StoreStats summarizes the store contents.
type StoreStats struct {
	AlgorithmCounts map[string]int
	TotalSize       int64
	ObjectCount     int64
}

// String renders a one-line human summary of the stats.
func (st StoreStats) String() string {
	algos := make([]string, 0, len(st.AlgorithmCounts))
	for a := range st.AlgorithmCounts {
		algos = append(algos, a)
	}
	sort.Strings(algos)
	parts := make([]string, 0, len(algos))
	for _, a := range algos {
		parts = append(parts, fmt.Sprintf("%s=%d", a, st.AlgorithmCounts[a]))
	}
	return fmt.Sprintf("%d objects, %d bytes [%s]", st.ObjectCount, st.TotalSize, strings.Join(parts, ", "))
}

// Stats walks the tree and returns per-algorithm counts and total size.
func (s *Backend) Stats(ctx context.Context) (*StoreStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st := &StoreStats{AlgorithmCounts: map[string]int{}}
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

// Verify re-reads the object and recomputes its hash.
func (s *Backend) Verify(ctx context.Context, h cas.Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rc, err := s.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()

	if newStream, ok := cas.LookupStreamHash(h.Algorithm()); ok {
		hasher := newStream()
		if _, err := io.Copy(hasher, rc); err != nil {
			return fmt.Errorf("cas: verify read: %w", err)
		}
		actual, err := cas.NewHash(h.Algorithm(), hasher.Sum(nil))
		if err != nil {
			return err
		}
		if !actual.Equal(h) {
			return fmt.Errorf("%w: %s", cas.ErrHashMismatch, h)
		}
		return nil
	}
	hashFn, ok := cas.LookupHash(h.Algorithm())
	if !ok {
		return fmt.Errorf("%w: %q", cas.ErrUnknownAlgorithm, h.Algorithm())
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("cas: verify read: %w", err)
	}
	if !hashFn(data).Equal(h) {
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
