package cas

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default fan-out parameters, Git-like: <base>/<algo>/<2 hex>/<full hex>.
const (
	DefaultFanOut    = 2
	DefaultFanLevels = 1
	// MaxFanDepth is the fan-out bound: FanLevels × FanOut must not exceed
	// the hex digest length (64 for SHA-256; SHA-1 digests are 40 chars, so
	// a store using sha1 must stay within that).
	MaxFanDepth = 64
)

// FSOption configures an FSRawStore. Functional options per
// library-design §4; never positional bool/int soup.
type FSOption func(*fsConfig)

type fsConfig struct {
	fanOut    int
	fanLevels int
}

// WithFanOut sets the number of hex characters per fan-out directory level.
// 0 means "flat" (no fan-out directories).
func WithFanOut(n int) FSOption {
	return func(c *fsConfig) { c.fanOut = n }
}

// WithFanLevels sets the number of fan-out directory levels. 0 means "flat".
func WithFanLevels(n int) FSOption {
	return func(c *fsConfig) { c.fanLevels = n }
}

// FSRawStore is the filesystem RawStore backend: each object is one file
// under <base>/<algorithm>/<fan-out dirs>/<full-hex-digest>.
//
// Reads are lock-free by design: writes are atomic (temp file → f.Sync() →
// os.Rename, and os.Rename is atomic), so Get/Exists/List/Stats observe
// either the old or the new file, never a partial one, and take no lock.
// At most a single sync.Mutex coordinates Put/Delete; concurrent writers of
// the same hash are safe because Put is idempotent (same hash ⇒ identical
// bytes). On POSIX, Delete during an in-flight read is safe because
// unlink/rename keep already-open file descriptors valid. Do not add locks
// to the read path — the atomicity argument above is the design contract
// (performance spec §2).
type FSRawStore struct {
	base      string
	fanOut    int
	fanLevels int

	mu sync.Mutex // Put/Delete only
}

// NewFSRawStore creates a filesystem backend rooted at basePath, creating
// the directory tree. Options default to the Git-like fan-out (2,1). It
// returns an error if the fan-out layout is over-deep
// (FanLevels × FanOut > MaxFanDepth) or any parameter is negative.
func NewFSRawStore(basePath string, opts ...FSOption) (*FSRawStore, error) {
	cfg := fsConfig{fanOut: DefaultFanOut, fanLevels: DefaultFanLevels}
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
	return &FSRawStore{base: basePath, fanOut: cfg.fanOut, fanLevels: cfg.fanLevels}, nil
}

// hashPath returns the on-disk path for h:
// <base>/<algo>/<fan-out chunks>/<full-hex-digest>.
func (s *FSRawStore) hashPath(h Hash) string {
	hexDigest := hex.EncodeToString(h.Bytes())
	p := filepath.Join(s.base, h.Algorithm())
	if s.fanOut > 0 && s.fanLevels > 0 {
		for i := 0; i < s.fanLevels; i++ {
			start := i * s.fanOut
			if start >= len(hexDigest) {
				break
			}
			end := start + s.fanOut
			if end > len(hexDigest) {
				end = len(hexDigest)
			}
			p = filepath.Join(p, hexDigest[start:end])
		}
	}
	return filepath.Join(p, hexDigest)
}

// pathToHash rebuilds a Hash from a path relative to the store base: the
// first path element is the algorithm, the last is the full hex digest;
// fan-out directories in between are not needed for reconstruction.
func pathToHash(rel string) (Hash, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return nil, ErrInvalidHash
	}
	return ParseHash(parts[0] + ":" + parts[len(parts)-1])
}

// Put stores the bytes read from r under h. It is idempotent (same hash ⇒
// identical bytes) and atomic: the content is written to a temp file created
// with O_CREATE|O_EXCL next to the final path, fsynced, then renamed over
// it. Temp names are unique per writer: the base name is <path>.tmp, and if
// another writer already holds it (O_EXCL fails — only possible across
// processes, since the in-process mutex serializes Puts) a numeric suffix is
// appended. No two writers ever share a temp inode, so concurrent writers of
// the same hash — even from different OS processes — cannot corrupt each
// other's in-flight write or the stored object. The rename is atomic: on
// POSIX the last writer wins with identical bytes; on Windows a concurrent
// rename-over-existing can transiently fail (no cross-process last-wins), so
// a racing Put MAY return an error — the object is never corrupted and the
// failed writer's temp file is removed. On any failure the temp file is
// removed; readers never observe partial files.
func (s *FSRawStore) Put(ctx context.Context, h Hash, r io.Reader) error {
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
	return nil
}

// createTempExcl creates a uniquely named temp file for an object write:
// <path>.tmp, or <path>.tmp.<n> while another writer holds the base name.
// The in-process mutex means the first attempt always succeeds in-process; a
// suffix is only needed when a DIFFERENT process is writing the same hash.
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

// Get returns a stream of the object's bytes; the caller MUST close it. A
// missing object returns ErrNotFound (wrapped). Lock-free: os.Open observes
// either the old or the new file, never a partial one.
func (s *FSRawStore) Get(ctx context.Context, h Hash) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.hashPath(h))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, h)
		}
		return nil, fmt.Errorf("cas: open object: %w", err)
	}
	return f, nil
}

// Exists reports whether the object is stored. Lock-free.
func (s *FSRawStore) Exists(ctx context.Context, h Hash) (bool, error) {
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

// Delete removes the object. A missing object is a no-op (no error).
func (s *FSRawStore) Delete(ctx context.Context, h Hash) error {
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

// Size returns the stored object's size in bytes. A missing object returns
// ErrNotFound. Lock-free.
func (s *FSRawStore) Size(h Hash) (int64, error) {
	fi, err := os.Stat(s.hashPath(h))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("%w: %s", ErrNotFound, h)
		}
		return 0, fmt.Errorf("cas: stat object: %w", err)
	}
	return fi.Size(), nil
}

// Clean removes leftover *.tmp files (from a crash mid-write) older than
// olderThan, returning how many were removed. *.tmp files are never valid
// objects — List/Stats ignore them — so sweeping is always safe. An
// olderThan <= 0 removes every *.tmp file. Lock-free.
func (s *FSRawStore) Clean(ctx context.Context, olderThan time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	err := filepath.WalkDir(s.base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries; keep sweeping
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
// Files that are not recognizable objects — stray files, leftover *.tmp
// files — are skipped, not errors. Lock-free; walks the tree.
func (s *FSRawStore) List(ctx context.Context, algo string) ([]Hash, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var hashes []Hash
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
			return nil // unrecognized file (stray, *.tmp, ...): skip
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

// StoreStats summarizes the store contents: per-algorithm object counts,
// total size in bytes, and total object count. *.tmp files are ignored.
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
// Lock-free.
func (s *FSRawStore) Stats(ctx context.Context) (*StoreStats, error) {
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
			return nil // unrecognized file: skip
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

// Verify re-reads the object and recomputes its hash with the algorithm from
// the address. A mismatch — any corruption — returns ErrHashMismatch; a
// missing object returns ErrNotFound. The store never "fixes" a broken
// object; the correct content must be re-Put. Built-in algorithms verify in
// a single streaming pass; algorithms registered only as one-shot HashFunc
// are verified by buffering the object.
func (s *FSRawStore) Verify(ctx context.Context, h Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rc, err := s.Get(ctx, h)
	if err != nil {
		return err
	}
	defer rc.Close()

	if newStream, ok := lookupStreamHash(h.Algorithm()); ok {
		hasher := newStream()
		if _, err := io.Copy(hasher, rc); err != nil {
			return fmt.Errorf("cas: verify read: %w", err)
		}
		actual, err := NewHash(h.Algorithm(), hasher.Sum(nil))
		if err != nil {
			return err
		}
		if !actual.Equal(h) {
			return fmt.Errorf("%w: %s", ErrHashMismatch, h)
		}
		return nil
	}

	// Fallback for custom algorithms registered as one-shot HashFunc only.
	hashFn, ok := lookupHash(h.Algorithm())
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAlgorithm, h.Algorithm())
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("cas: verify read: %w", err)
	}
	if !hashFn(data).Equal(h) {
		return fmt.Errorf("%w: %s", ErrHashMismatch, h)
	}
	return nil
}

// GC performs mark-and-sweep garbage collection: it deletes every object
// whose h.String() is not present in reachable. The caller computes the
// reachable set (e.g. by walking References() from application roots);
// reachable is keyed by h.String(). Sweeping is safe against concurrent
// writers — a concurrent Put of a swept hash simply re-creates it
// (idempotent), and a reader holding an open file keeps its bytes (POSIX).
func (s *FSRawStore) GC(ctx context.Context, reachable map[string]bool) error {
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

// Prune deletes objects that are not reachable from roots AND older than
// minAge (age = file mtime ≈ first-Put time). It returns the set of hashes
// it would delete; when dryRun is true nothing is deleted (dry-run is the
// safe default — deleting requires the explicit flag). Reachability is
// limited to the provided roots at the byte layer: the store cannot
// interpret object references, so the transitive closure of a root's
// references is NOT followed here. Applications that need graph-aware
// retention compute a reachable set (walking References()) and use GC
// instead. Unreachable objects younger than minAge are kept, providing a
// recovery grace period.
func (s *FSRawStore) Prune(ctx context.Context, roots []Hash, minAge time.Duration, dryRun bool) ([]Hash, error) {
	reachable := make(map[string]bool, len(roots))
	for _, r := range roots {
		reachable[r.String()] = true
	}
	hashes, err := s.List(ctx, "")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var doomed []Hash
	for _, h := range hashes {
		if reachable[h.String()] {
			continue
		}
		info, err := os.Stat(s.hashPath(h))
		if err != nil {
			continue // vanished concurrently; nothing to prune
		}
		if now.Sub(info.ModTime()) < minAge {
			continue // grace period
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

// hashWriter computes a Hash over the bytes streamed through it (single
// pass, no buffering).
