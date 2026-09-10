package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	"github.com/dmundt/go-cask/cas/hash/sha256"
)

// digestOf computes a content digest for the backend tests, using the
// client-side sha256 hasher the core no longer owns.
func digestOf(data []byte) cas.Digest {
	return sha256.Of(data)
}

func readAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

func mustFS(t *testing.T, opts ...backend.Option) *Backend {
	s, err := New(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFanOutLayouts(t *testing.T) {
	digest := strings.Repeat("a1", 32)
	h, err := cas.ParseDigest(digest)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		opts     []backend.Option
		wantPath string
	}{
		{"flat", []backend.Option{WithFanOut(0), WithFanLevels(0)}, digest},
		{"gitlike-default", nil, filepath.Join("a1", digest)},
		{"deep-2-2", []backend.Option{WithFanOut(2), WithFanLevels(2)}, filepath.Join("a1", "a1", digest)},
		{"wide-4-1", []backend.Option{WithFanOut(4), WithFanLevels(1)}, filepath.Join("a1a1", digest)},
	}
	for _, tc := range cases {
		s := mustFS(t, tc.opts...)
		got := s.digestPath(h)
		want := filepath.Join(s.base, tc.wantPath)
		if got != want {
			t.Errorf("%s: digestPath = %q, want %q", tc.name, got, want)
		}
	}
}

func TestFanOutBounds(t *testing.T) {
	for _, tc := range []struct {
		opts []backend.Option
		ok   bool
	}{
		{[]backend.Option{WithFanOut(0), WithFanLevels(0)}, true},
		{[]backend.Option{WithFanOut(33), WithFanLevels(2)}, false},
		{[]backend.Option{WithFanOut(64), WithFanLevels(1)}, true},
		{[]backend.Option{WithFanOut(-1)}, false},
		{[]backend.Option{WithFanLevels(-1)}, false},
	} {
		_, err := New(t.TempDir(), tc.opts...)
		if tc.ok && err != nil {
			t.Errorf("opts %v: unexpected error %v", tc.opts, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("opts %v: expected rejection", tc.opts)
		}
	}
}

func TestLayoutEquivalence(t *testing.T) {
	ctx := context.Background()
	content := []byte("the same bytes")
	h := digestOf(content)
	layouts := [][]backend.Option{
		nil,
		{WithFanOut(0), WithFanLevels(0)},
		{WithFanOut(2), WithFanLevels(2)},
		{WithFanOut(4), WithFanLevels(1)},
	}
	for _, opts := range layouts {
		s := mustFS(t, opts...)
		if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
			t.Fatalf("opts %v: Put: %v", opts, err)
		}
		rc, err := s.Get(ctx, h)
		if err != nil {
			t.Fatalf("opts %v: Get: %v", opts, err)
		}
		got, err := readAllAndClose(rc)
		if err != nil || string(got) != string(content) {
			t.Fatalf("opts %v: read = %q, %v", opts, got, err)
		}
	}
}

func TestPathRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := digestOf([]byte("path round trip"))
	for _, opts := range [][]backend.Option{nil, {WithFanOut(0), WithFanLevels(0)}, {WithFanOut(2), WithFanLevels(2)}, {WithFanOut(4), WithFanLevels(1)}} {
		s := mustFS(t, opts...)
		if err := s.Put(ctx, h, strings.NewReader("path round trip")); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(s.base, s.digestPath(h))
		if err != nil {
			t.Fatal(err)
		}
		back, err := pathToDigest(rel)
		if err != nil {
			t.Fatalf("pathToDigest(%q): %v", rel, err)
		}
		if !back.Equal(h) {
			t.Fatalf("pathToDigest(digestPath(h)) != h: %s vs %s", back, h)
		}
	}
}

// failingReader fails after reading some bytes — exercises the write-path
// error/cleanup branches of Backend.Put.
type failingReader struct {
	data []byte
	off  int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, errors.New("simulated read failure")
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// tmpFilesIn returns the leftover `*.tmp` file names in the fan-out
// directory that would hold h. Put writes uniquely named temps there, so a
// failed write must leave none behind.
func tmpFilesIn(s *Backend, h cas.Digest) []string {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(s.digestPath(h)), "*.tmp"))
	return matches
}

func TestFSPutMkdirError(t *testing.T) {
	// Make the fan-out directory unusable: create a FILE where the first
	// fan-out dir would go, so MkdirAll fails. There is no algorithm
	// directory any more (the digest is the whole address).
	s := mustFS(t)
	h := digestOf([]byte("x"))
	blocker := filepath.Join(s.base, h.String()[:2])
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), h, strings.NewReader("x")); err == nil {
		t.Fatal("Put must fail when the object dir cannot be created")
	}
}

func TestFSPutReaderError(t *testing.T) {
	s := mustFS(t)
	h := digestOf([]byte("x"))
	err := s.Put(context.Background(), h, &failingReader{data: []byte("partial")})
	if err == nil {
		t.Fatal("Put with failing reader must error")
	}
	// The temp file must be cleaned up and the object must not exist.
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("failed Put left objects behind: %v", list)
	}
	if leftovers := tmpFilesIn(s, h); len(leftovers) != 0 {
		t.Fatalf("failed Put left temp files behind: %v", leftovers)
	}
}

func TestFSListIgnoresRootStray(t *testing.T) {
	s := mustFS(t)
	// A stray file directly in the base directory is not an object.
	if err := os.WriteFile(filepath.Join(s.base, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("stray root file listed as object: %v", list)
	}
}

// TestFSDigestPathClamp pins the digest-width invariant that replaced the
// old clamping logic: FanOut × FanLevels is bounded to the digest width
// (MaxFanDepth, checked in New), so every fan-out chunk stays in range and the
// file name is always the full hex digest — there is no algorithm directory.
func TestFSDigestPathClamp(t *testing.T) {
	h := digestOf([]byte("clamp"))
	cases := []struct {
		opts []backend.Option
	}{
		{[]backend.Option{WithFanOut(16), WithFanLevels(3)}}, // 3rd chunk ends at 48
		{[]backend.Option{WithFanOut(16), WithFanLevels(4)}}, // covers all 64 hex chars
		{[]backend.Option{WithFanOut(8), WithFanLevels(8)}},  // many levels, digest exhausted
	}
	for _, tc := range cases {
		s := mustFS(t, tc.opts...)
		p := s.digestPath(h)
		// The file name must still be the full hex digest.
		base := filepath.Base(p)
		if base != h.String() {
			t.Errorf("opts %v: basename = %q, want full digest", tc.opts, base)
		}
		// And the path must round-trip.
		rel, err := filepath.Rel(s.base, p)
		if err != nil {
			t.Fatal(err)
		}
		back, err := pathToDigest(rel)
		if err != nil || !back.Equal(h) {
			t.Errorf("opts %v: pathToDigest(%q) = %v, %v", tc.opts, rel, back, err)
		}
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestContextCancellationFS verifies every Backend operation honors a
// canceled context (no filesystem side effects happen).
func TestContextCancellationFS(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := digestOf([]byte("x"))
	ops := []struct {
		name string
		run  func() error
	}{
		{"Put", func() error { return s.Put(ctx, h, strings.NewReader("x")) }},
		{"Get", func() error { _, err := s.Get(ctx, h); return err }},
		{"Exists", func() error { _, err := s.Exists(ctx, h); return err }},
		{"Delete", func() error { return s.Delete(ctx, h) }},
		{"Size", func() error { _, err := s.Size(ctx, h); return err }},
		{"List", func() error { _, err := s.List(ctx); return err }},
		{"Stats", func() error { _, err := s.Stats(ctx); return err }},
		{"Verify", func() error { return s.Verify(ctx, h, sha256.New()) }},
		{"GC", func() error { return s.GC(ctx, map[string]bool{}) }},
		{"Prune", func() error { _, err := s.Prune(ctx, []cas.Digest{h}, 0, true); return err }},
		{"Clean", func() error { _, err := s.Clean(ctx, 0); return err }},
	}
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// TestFSBackendErrorPaths covers portable Backend failures: constructor over
// a file, Put with a failing reader (temp cleaned up), and Prune at minAge 0.
func TestFSBackendErrorPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file); err == nil {
		t.Fatal("New over an existing file must error")
	}

	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := digestOf([]byte("data"))
	if err := s.Put(ctx, h, errReader{err: io.ErrClosedPipe}); err == nil {
		t.Fatal("Put with failing reader must error")
	}
	if n, _ := s.Clean(ctx, 0); n != 0 {
		t.Fatalf("Clean after failed Put removed %d files, want 0 (temp cleaned)", n)
	}
	if ok, _ := s.Exists(ctx, h); ok {
		t.Fatal("object exists after failed Put")
	}

	a := digestOf([]byte("keep"))
	b := digestOf([]byte("drop"))
	for _, x := range []struct {
		h cas.Digest
		d string
	}{{a, "keep"}, {b, "drop"}} {
		if err := s.Put(ctx, x.h, strings.NewReader(x.d)); err != nil {
			t.Fatal(err)
		}
	}
	doomed, err := s.Prune(ctx, []cas.Digest{a}, 0, true)
	if err != nil || len(doomed) != 1 || !doomed[0].Equal(b) {
		t.Fatalf("prune dry-run = %v, %v; want [b]", doomed, err)
	}
	if _, err := s.Prune(ctx, []cas.Digest{a}, 0, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, b); ok {
		t.Fatal("unreachable object survived prune at minAge 0")
	}
	if ok, _ := s.Exists(ctx, a); !ok {
		t.Fatal("reachable root was pruned")
	}
}

// TestPutIdempotent verifies that Put succeeds the first time, a re-Put of the
// same digest is a no-op success, and reading back returns the exact bytes.
func TestPutIdempotent(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("idempotent content bytes")
	h := digestOf(content)
	for i := 0; i < 2; i++ {
		if err := s.Put(ctx, h, bytes.NewReader(content)); err != nil {
			t.Fatalf("Put pass %d: %v", i, err)
		}
	}
	rc, err := s.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllAndClose(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("read back = %q, want %q", got, content)
	}
}

// TestPutPublishError forces os.Rename to fail by pre-creating the object's
// final path as a directory, exercising Put's publish-error cleanup branch.
func TestPutPublishError(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("rename over a directory must fail")
	h := digestOf(content)
	path := s.digestPath(h)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, h, bytes.NewReader(content)); err == nil {
		t.Fatal("Put over an existing directory at the object path must error")
	}
	if leftovers := tmpFilesIn(s, h); len(leftovers) != 0 {
		t.Fatalf("failed Put left temp files behind: %v", leftovers)
	}
}

// TestGetNotFound verifies that reading a missing object yields ErrNotFound.
func TestGetNotFound(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	h := digestOf([]byte("never stored"))
	rc, err := s.Get(ctx, h)
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get missing = %v, %v; want ErrNotFound", rc, err)
	}
	if rc != nil {
		rc.Close()
	}
}

// TestExistsPresentAndMissing exercises Exists for a stored and a missing digest.
func TestExistsPresentAndMissing(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("exists check")
	h := digestOf(content)
	if ok, err := s.Exists(ctx, h); err != nil || ok {
		t.Fatalf("Exists before Put = %v, %v; want false,nil", ok, err)
	}
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Exists(ctx, h); err != nil || !ok {
		t.Fatalf("Exists after Put = %v, %v; want true,nil", ok, err)
	}
}

// TestDeletePresentAndMissing verifies Delete removes an existing object and
// is a nil no-op for a missing one.
func TestDeletePresentAndMissing(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("delete me")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, h); err != nil {
		t.Fatalf("Delete existing: %v", err)
	}
	if ok, _ := s.Exists(ctx, h); ok {
		t.Fatal("object still exists after Delete")
	}
	// Delete of a now-missing object is a nil no-op.
	if err := s.Delete(ctx, h); err != nil {
		t.Fatalf("Delete missing (no-op): %v", err)
	}
	missing := digestOf([]byte("never put"))
	if err := s.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete never-stored (no-op): %v", err)
	}
}

// TestSize verifies Size returns the byte count for a present object and
// ErrNotFound for a missing one.
func TestSize(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("sized content: 1234567890")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Size(ctx, h); err != nil || n != int64(len(content)) {
		t.Fatalf("Size = %d, %v; want %d,nil", n, err, len(content))
	}
	missing := digestOf([]byte("missing"))
	if _, err := s.Size(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Size missing = %v; want ErrNotFound", err)
	}
}

// TestCleanTmpRemoval covers Clean's tmp-file removal semantics: recent tmp
// files survive an olderThan sweep; old tmp files are removed and counted.
func TestCleanTmpRemoval(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	base := s.base

	// A stray tmp file directly under the base root.
	stray := filepath.Join(base, "orphan.tmp")
	if err := os.WriteFile(stray, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Another stray tmp file aged into the past.
	old := filepath.Join(base, "old.tmp")
	if err := os.WriteFile(old, []byte("old junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// A real stored object (not a .tmp file) must survive any sweep.
	content := []byte("keep me")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}

	// Sweep older than 24h: removes only old.tmp.
	n, err := s.Clean(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Clean(24h) removed %d files, want 1", n)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("recent stray tmp was removed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old tmp survived sweep: %v", err)
	}
	// Object still present.
	if ok, _ := s.Exists(ctx, h); !ok {
		t.Fatal("stored object removed by Clean")
	}

	// Clean(0) removes every remaining *.tmp regardless of age.
	n, err = s.Clean(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Clean(0) removed %d files, want 1 (the recent stray)", n)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("stray tmp survived Clean(0): %v", err)
	}
}

// TestCleanRemovesTempFallbacks covers the collision-fallback temp names
// createTempExcl produces ("<hex>.tmp.<n>"): matching only the exact ".tmp"
// suffix left crash leftovers with fallback names unreclaimable.
func TestCleanRemovesTempFallbacks(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	h := digestOf([]byte("payload"))
	objPath := s.digestPath(h)
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		t.Fatal(err)
	}
	fallback := objPath + ".tmp.1"
	if err := os.WriteFile(fallback, []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file that merely contains ".tmp" is not a temp file and must survive.
	decoy := filepath.Join(filepath.Dir(objPath), "notes.tmpdata")
	if err := os.WriteFile(decoy, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := s.Clean(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Clean removed %d files, want 1 (the .tmp.1 fallback)", n)
	}
	if _, err := os.Stat(fallback); !os.IsNotExist(err) {
		t.Fatalf("fallback temp survived Clean: %v", err)
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Fatalf("non-temp file removed by Clean: %v", err)
	}
}

// TestDigestPathRejectsAbsentDigest pins the guard that replaced the old
// algorithm-name sanitizer: cas.Digest is a raw byte slice now, so the only
// invalid address left is the absent one, and every backend entry point
// rejects it with ErrInvalidDigest.
func TestDigestPathRejectsAbsentDigest(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	var absent cas.Digest
	if err := s.Put(ctx, absent, strings.NewReader("x")); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Put(absent) = %v, want ErrInvalidDigest", err)
	}
	if _, err := s.Get(ctx, absent); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Get(absent) = %v, want ErrInvalidDigest", err)
	}
	if _, err := s.Exists(ctx, absent); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Exists(absent) = %v, want ErrInvalidDigest", err)
	}
	if err := s.Delete(ctx, absent); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Delete(absent) = %v, want ErrInvalidDigest", err)
	}
	if _, err := s.Size(ctx, absent); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Size(absent) = %v, want ErrInvalidDigest", err)
	}
	if err := s.Verify(ctx, absent, sha256.New()); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Verify(absent) = %v, want ErrInvalidDigest", err)
	}
}

// TestOpenWithRetry covers the transient-open retry path deterministically:
// a sharing-style failure while the file exists is retried, a missing file is
// not, and an unrelenting failure is reported.
func TestOpenWithRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "object")
	if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fails twice with a permissions-style error, then succeeds.
	calls := 0
	f, err := openWithRetry(func(p string) (*os.File, error) {
		calls++
		if calls <= 2 {
			return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrPermission}
		}
		return os.Open(p)
	}, path)
	if err != nil {
		t.Fatalf("openWithRetry after transient failures = %v", err)
	}
	f.Close()
	if calls != 3 {
		t.Fatalf("open attempts = %d, want 3", calls)
	}

	// A missing file is reported immediately (no retry).
	calls = 0
	if _, err := openWithRetry(func(p string) (*os.File, error) {
		calls++
		return nil, &os.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}, path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file = %v, want fs.ErrNotExist", err)
	}
	if calls != 1 {
		t.Fatalf("missing-file open attempts = %d, want 1", calls)
	}

	// A file that exists but never opens exhausts the retries and errors.
	calls = 0
	if _, err := openWithRetry(func(p string) (*os.File, error) {
		calls++
		return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrPermission}
	}, path); err == nil {
		t.Fatal("persistent open failure must error")
	}
	if calls != 20 {
		t.Fatalf("persistent-failure open attempts = %d, want 20", calls)
	}
}

// cancelAfterReader cancels a context once the first chunk has been served.
type cancelAfterReader struct {
	cancel context.CancelFunc
	data   []byte
	served bool
}

func (c *cancelAfterReader) Read(p []byte) (int, error) {
	n := copy(p, c.data)
	c.data = c.data[n:]
	if !c.served {
		c.served = true
		c.cancel()
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// TestPutCanceledMidStream pins the streaming cancellation contract: a Put
// whose context is canceled mid-write publishes nothing and leaves no temp.
func TestPutCanceledMidStream(t *testing.T) {
	s := mustFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	content := bytes.Repeat([]byte("x"), 3<<20) // several io.Copy buffers
	h := digestOf(content)
	r := &cancelAfterReader{cancel: cancel, data: content}
	if err := s.Put(ctx, h, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put canceled mid-stream = %v, want context.Canceled", err)
	}
	if ok, _ := s.Exists(context.Background(), h); ok {
		t.Fatal("canceled Put published the object")
	}
	if leftovers := tmpFilesIn(s, h); len(leftovers) != 0 {
		t.Fatalf("canceled Put left temp files behind: %v", leftovers)
	}
}

// TestConcurrentPutGetDelete exercises the backend's concurrency contract
// (testing-strategy §4, performance §6): concurrent same-digest Puts, reads
// during writes/deletes, and parallel List/Stats must never corrupt or lose
// intact objects. Run under -race in CI.
func TestConcurrentPutGetDelete(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	shared := []byte("shared object")
	sharedDigest := digestOf(shared)

	const workers, perWorker = 8, 25
	errs := make(chan error, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				// Concurrent writers of the same content (idempotent Put).
				if err := s.Put(ctx, sharedDigest, bytes.NewReader(shared)); err != nil {
					errs <- err
					return
				}
				content := []byte(fmt.Sprintf("obj-%d-%d", w, i))
				h := digestOf(content)
				if err := s.Put(ctx, h, bytes.NewReader(content)); err != nil {
					errs <- err
					return
				}
				rc, err := s.Get(ctx, h)
				if err != nil {
					errs <- err
					return
				}
				got, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(got, content) {
					errs <- fmt.Errorf("read %q, want %q", got, content)
					return
				}
				if i%5 == 0 {
					if err := s.Delete(ctx, h); err != nil {
						errs <- err
						return
					}
				}
				if i%7 == 0 {
					if _, err := s.List(ctx); err != nil {
						errs <- err
						return
					}
					if _, err := s.Stats(ctx); err != nil {
						errs <- err
						return
					}
					if err := s.Verify(ctx, sharedDigest, sha256.New()); err != nil {
						errs <- err
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestStats exercises Stats object count + total size and String (there is no
// per-algorithm breakdown any more: the core does not know the algorithm).
func TestStats(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	a := []byte("stats-content-a")
	b := []byte("stats-content-b-longer")
	ha := digestOf(a)
	hb := digestOf(b)
	for _, x := range []struct {
		h cas.Digest
		d []byte
	}{{ha, a}, {hb, b}} {
		if err := s.Put(ctx, x.h, bytes.NewReader(x.d)); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 2 {
		t.Fatalf("ObjectCount = %d, want 2", st.ObjectCount)
	}
	if want := int64(len(a) + len(b)); st.TotalSize != want {
		t.Fatalf("TotalSize = %d, want %d", st.TotalSize, want)
	}
	if want := fmt.Sprintf("2 objects, %d bytes", len(a)+len(b)); st.String() != want {
		t.Fatalf("String() = %q; want %q", st.String(), want)
	}
}

// TestWithDirSync builds a backend with WithDirSync and confirms Put succeeds
// (exercising the s.dirSync branch in Put on non-Windows and a nil return on
// Windows where syncParentDir is a no-op).
func TestWithDirSync(t *testing.T) {
	s := mustFS(t, WithDirSync())
	if !s.dirSync {
		t.Fatal("WithDirSync did not set dirSync on the backend")
	}
	ctx := context.Background()
	content := []byte("dir-synced object")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatalf("Put with dirSync: %v", err)
	}
	rc, err := s.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllAndClose(rc)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("read back = %q, %v", got, err)
	}
}

// TestVerifyStreaming exercises the sha256 streaming Verify path: success on
// an intact object, ErrDigestMismatch after corruption, ErrNotFound when missing.
func TestVerifyStreaming(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("verify streaming content")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h, sha256.New()); err != nil {
		t.Fatalf("Verify intact = %v", err)
	}
	// Corrupt the stored bytes.
	if err := os.WriteFile(s.digestPath(h), []byte("tampered!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h, sha256.New()); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("Verify corrupt = %v, want ErrDigestMismatch", err)
	}
	missing := digestOf([]byte("absent"))
	if err := s.Verify(ctx, missing, sha256.New()); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Verify missing = %v, want ErrNotFound", err)
	}
}

// TestGC verifies mark-and-sweep removes unreachable objects and keeps roots.
func TestGC(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	keep := []byte("gc keep")
	drop := []byte("gc drop")
	hk := digestOf(keep)
	hd := digestOf(drop)
	for _, x := range []struct {
		h cas.Digest
		d string
	}{{hk, "gc keep"}, {hd, "gc drop"}} {
		if err := s.Put(ctx, x.h, strings.NewReader(x.d)); err != nil {
			t.Fatal(err)
		}
	}
	reachable := map[string]bool{hk.String(): true}
	if err := s.GC(ctx, reachable); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, hk); !ok {
		t.Fatal("reachable object removed by GC")
	}
	if ok, _ := s.Exists(ctx, hd); ok {
		t.Fatal("unreachable object survived GC")
	}
}

// TestPruneAgeRetention verifies Prune keeps unreachable young objects when
// minAge is large, and that a dry-run reports doomed objects without deleting.
func TestPruneAgeRetention(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("young but unreachable")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	// Large minAge: object is unreachable yet too young to prune.
	doomed, err := s.Prune(ctx, nil, 24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 0 {
		t.Fatalf("Prune(young, minAge=24h) doomed %v, want none", doomed)
	}
	if _, err := s.Prune(ctx, nil, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, h); !ok {
		t.Fatal("young unreachable object was pruned")
	}

	// Dry-run at minAge 0 reports the object doomed but deletes nothing.
	doomed, err = s.Prune(ctx, nil, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 1 || !doomed[0].Equal(h) {
		t.Fatalf("Prune dry-run doomed = %v, want [h]", doomed)
	}
	if ok, _ := s.Exists(ctx, h); !ok {
		t.Fatal("dry-run prune deleted the object")
	}
	// Real prune at minAge 0 deletes it.
	if _, err := s.Prune(ctx, nil, 0, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, h); ok {
		t.Fatal("unreachable object survived prune at minAge 0")
	}
}

// TestListReturnsAllDigests pins what survives of the old algorithm-filter
// test: List now takes no algorithm argument (the backend cannot know one), so
// its whole remaining contract is that it returns every stored digest once,
// sorted, and nothing else.
func TestListReturnsAllDigests(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	want := make(map[string]bool, 3)
	for _, content := range []string{"list-a", "list-b", "list-c"} {
		h := digestOf([]byte(content))
		want[h.String()] = true
		if err := s.Put(ctx, h, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("List() = %v, %v; want 3 digests", all, err)
	}
	for _, d := range all {
		if !want[d.String()] {
			t.Fatalf("List() returned unexpected digest %s", d)
		}
		delete(want, d.String())
	}
	if len(want) != 0 {
		t.Fatalf("List() omitted digests: %v", want)
	}
	if !sort.SliceIsSorted(all, func(i, j int) bool { return all[i].String() < all[j].String() }) {
		t.Fatalf("List() = %v; want sorted digests", all)
	}
}

// TestCreateTempExclCollision verifies that when <path>.tmp already exists,
// createTempExcl falls back to <path>.tmp.1.
func TestCreateTempExclCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj")
	base := path + ".tmp"
	if err := os.WriteFile(base, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, tmp, err := createTempExcl(path)
	if err != nil {
		t.Fatalf("createTempExcl after collision: %v", err)
	}
	defer func() {
		f.Close()
		os.Remove(tmp)
	}()
	if tmp != base+".1" {
		t.Fatalf("createTempExcl returned %q, want %q", tmp, base+".1")
	}
}

// TestCreateTempExclNoParent verifies createTempExcl returns an error (not an
// IsExist collision path) when the enclosing directory does not exist.
func TestCreateTempExclNoParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "no-such-dir", "obj")
	if f, tmp, err := createTempExcl(p); err == nil {
		f.Close()
		os.Remove(tmp)
		t.Fatalf("createTempExcl in missing dir succeeded (tmp=%q)", tmp)
	}
}

// TestDigestPathDepthBound pins the layout invariant that makes the chunking in
// digestPath total: FanOut × FanLevels never exceeds the digest width
// (MaxFanDepth), so a configured layout covers the digest exactly and cannot
// run past its end.
func TestDigestPathDepthBound(t *testing.T) {
	if _, err := New(t.TempDir(), WithFanOut(3), WithFanLevels(22)); err == nil {
		t.Fatal("fan-out beyond MaxFanDepth must be rejected")
	}
	h := digestOf([]byte("clamp me"))
	digest := h.String() // 64 hex chars
	for _, opts := range [][]backend.Option{
		{WithFanOut(2), WithFanLevels(1)},
		{WithFanOut(4), WithFanLevels(16)}, // 4 × 16 == MaxFanDepth exactly
		{WithFanOut(64), WithFanLevels(1)},
		{WithFanOut(0), WithFanLevels(0)}, // flat
	} {
		b := mustFS(t, opts...)
		if err := b.Put(context.Background(), h, strings.NewReader("x")); err != nil {
			t.Fatalf("opts %v: Put: %v", opts, err)
		}
		if p := b.digestPath(h); filepath.Base(p) != digest {
			t.Errorf("opts %v: basename = %q, want %q", opts, filepath.Base(p), digest)
		}
	}
}

// TestDeleteDirectoryError exercises Delete's non-IsNotExist error branch by
// pointing it at a non-empty directory (os.Remove cannot remove it).
func TestDeleteDirectoryError(t *testing.T) {
	s := mustFS(t)
	h := digestOf([]byte("dir-as-object"))
	dir := s.digestPath(h)
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), h); err == nil {
		t.Fatal("Delete of a non-empty directory must error")
	}
}

// TestStatsRootStray verifies Stats ignores stray files that are not objects
// (a single-segment relative path that cannot parse as a digest) while still
// counting real objects.
func TestStatsRootStray(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("counted object")
	h := digestOf(content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.base, "README.md"), []byte("not an object"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 1 {
		t.Fatalf("Stats ObjectCount = %d, want 1 (stray root file ignored)", st.ObjectCount)
	}
}

// TestWalkOnMissingBase exercises the top-level error returns of List, Clean
// and Stats when the store base directory no longer exists.
func TestWalkOnMissingBase(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	obj := []byte("walk base content")
	h := digestOf(obj)
	if err := s.Put(ctx, h, strings.NewReader(string(obj))); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Error("List over a removed base must error")
	}
	if _, err := s.Stats(ctx); err == nil {
		t.Error("Stats over a removed base must error")
	}
	// Clean tolerates a missing base (there is nothing to sweep) while
	// List/Stats report it.
	if n, err := s.Clean(ctx, 0); err != nil || n != 0 {
		t.Errorf("Clean over a removed base = %d, %v; want 0, nil", n, err)
	}
}

// TestVerifyReadOnDirectory places a directory where the object file would be
// and confirms Verify fails while reading it (a stream read error) rather than
// reporting the directory as a valid object.
func TestVerifyReadOnDirectory(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	h := digestOf([]byte("dir not an object"))
	if err := os.MkdirAll(s.digestPath(h), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h, sha256.New()); err == nil {
		t.Fatal("Verify over a directory-as-object must error")
	}
}

// TestPutCreateTempExhausted fills every temp-file candidate name for an
// object and confirms Put surfaces the createTempExcl exhaustion error instead
// of hanging or looping past the bound.
func TestPutCreateTempExhausted(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("temp namespace exhausted")
	h := digestOf(content)
	dir := filepath.Dir(s.digestPath(h))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create <path>.tmp and <path>.tmp.1 .. <path>.tmp.9999 so every
	// candidate name in createTempExcl's retry loop already exists.
	base := s.digestPath(h) + ".tmp"
	for i := 0; i < 10000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("precreate %q: %v", name, err)
		}
	}
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err == nil {
		t.Fatal("Put must error when the temp-file namespace is exhausted")
	}
}
