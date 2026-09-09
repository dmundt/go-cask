package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
)

func hashData(algo string, data []byte) (cas.Hash, error) {
	return cas.HashBytes(algo, data)
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
	h, _ := cas.ParseHash("sha256:" + digest)
	cases := []struct {
		name     string
		opts     []backend.Option
		wantPath string
	}{
		{"flat", []backend.Option{WithFanOut(0), WithFanLevels(0)}, filepath.Join("sha256", digest)},
		{"gitlike-default", nil, filepath.Join("sha256", "a1", digest)},
		{"deep-2-2", []backend.Option{WithFanOut(2), WithFanLevels(2)}, filepath.Join("sha256", "a1", "a1", digest)},
		{"wide-4-1", []backend.Option{WithFanOut(4), WithFanLevels(1)}, filepath.Join("sha256", "a1a1", digest)},
	}
	for _, tc := range cases {
		s := mustFS(t, tc.opts...)
		got := s.hashPath(h)
		want := filepath.Join(s.base, tc.wantPath)
		if got != want {
			t.Errorf("%s: hashPath = %q, want %q", tc.name, got, want)
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
	h, _ := hashData("sha256", content)
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
	for _, algo := range []string{"sha256"} {
		h, _ := hashData(algo, []byte("path round trip"))
		for _, opts := range [][]backend.Option{nil, {WithFanOut(0), WithFanLevels(0)}, {WithFanOut(2), WithFanLevels(2)}, {WithFanOut(4), WithFanLevels(1)}} {
			s := mustFS(t, opts...)
			if err := s.Put(ctx, h, strings.NewReader("path round trip")); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(s.base, s.hashPath(h))
			if err != nil {
				t.Fatal(err)
			}
			back, err := pathToHash(rel)
			if err != nil {
				t.Fatalf("pathToHash(%q): %v", rel, err)
			}
			if !back.Equal(h) {
				t.Fatalf("pathToHash(hashPath(h)) != h: %s vs %s", back, h)
			}
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
func tmpFilesIn(s *Backend, h cas.Hash) []string {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(s.hashPath(h)), "*.tmp"))
	return matches
}

func TestFSPutMkdirError(t *testing.T) {
	// Make the algorithm directory unusable: create a FILE where the
	// algorithm dir would go, so MkdirAll fails.
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("x"))
	blocker := filepath.Join(s.base, "sha256")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), h, strings.NewReader("x")); err == nil {
		t.Fatal("Put must fail when the object dir cannot be created")
	}
}

func TestFSPutReaderError(t *testing.T) {
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("x"))
	err := s.Put(context.Background(), h, &failingReader{data: []byte("partial")})
	if err == nil {
		t.Fatal("Put with failing reader must error")
	}
	// The temp file must be cleaned up and the object must not exist.
	list, err := s.List(context.Background(), "")
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
	list, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("stray root file listed as object: %v", list)
	}
}

func TestFSHashPathDigestClamp(t *testing.T) {
	// Layouts that exceed the digest length must clamp (end > len) or
	// stop (start >= len) rather than overrun. SHA-256 (64 hex) with
	// deep fan-out exercises this.
	h, _ := hashData("sha256", []byte("clamp"))
	cases := []struct {
		opts []backend.Option
	}{
		{[]backend.Option{WithFanOut(16), WithFanLevels(3)}}, // 3rd chunk clamps: 32..64
		{[]backend.Option{WithFanOut(16), WithFanLevels(4)}}, // 4th level breaks: 48 >= 64
		{[]backend.Option{WithFanOut(8), WithFanLevels(8)}},  // many levels, digest exhausted
	}
	for _, tc := range cases {
		s := mustFS(t, tc.opts...)
		p := s.hashPath(h)
		// The file name must still be the full hex digest.
		base := filepath.Base(p)
		if base != h.String()[strings.IndexByte(h.String(), ':')+1:] {
			t.Errorf("opts %v: basename = %q, want full digest", tc.opts, base)
		}
		// And the path must round-trip.
		rel, err := filepath.Rel(s.base, p)
		if err != nil {
			t.Fatal(err)
		}
		back, err := pathToHash(rel)
		if err != nil || !back.Equal(h) {
			t.Errorf("opts %v: pathToHash(%q) = %v, %v", tc.opts, rel, back, err)
		}
	}
}

// TestVerifyCustomOneShot exercises Verify's buffered fallback for hash
// algorithms registered only as one-shot HashFunc, plus the unknown-algo
// error path.
func TestVerifyCustomOneShot(t *testing.T) {
	cas.RegisterHash("obvfy", func(data []byte) cas.Hash {
		sum := sha256.Sum256(data)
		h, _ := cas.NewHash("obvfy", sum[:])
		return h
	})
	ctx := context.Background()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("verify me with a one-shot algorithm")
	sum := sha256.Sum256(data)
	h, _ := cas.NewHash("obvfy", sum[:])
	if err := s.Put(ctx, h, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); err != nil {
		t.Fatalf("Verify (buffered fallback) = %v", err)
	}
	// Corrupt the stored bytes: mismatch via the fallback path.
	if err := os.WriteFile(s.hashPath(h), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); !errors.Is(err, cas.ErrHashMismatch) {
		t.Fatalf("Verify corrupt = %v, want ErrHashMismatch", err)
	}
	// The unregistered-algorithm error path (Verify returning
	// ErrUnknownAlgorithm for a hash of an algorithm not in the registry) is
	// covered by the cas package tests (TestHashDataUnknownAlgorithm,
	// TestNewStoreUnknownAlgorithm): from outside package cas we cannot
	// construct a hash of an unregistered algorithm because the public
	// constructors (ParseHash/NewHash) reject it.
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
	h, err := hashData("sha256", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	ops := []struct {
		name string
		run  func() error
	}{
		{"Put", func() error { return s.Put(ctx, h, strings.NewReader("x")) }},
		{"Get", func() error { _, err := s.Get(ctx, h); return err }},
		{"Exists", func() error { _, err := s.Exists(ctx, h); return err }},
		{"Delete", func() error { return s.Delete(ctx, h) }},
		{"Size", func() error { _, err := s.Size(ctx, h); return err }},
		{"List", func() error { _, err := s.List(ctx, ""); return err }},
		{"Stats", func() error { _, err := s.Stats(ctx); return err }},
		{"Verify", func() error { return s.Verify(ctx, h) }},
		{"GC", func() error { return s.GC(ctx, map[string]bool{}) }},
		{"Prune", func() error { _, err := s.Prune(ctx, []cas.Hash{h}, 0, true); return err }},
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
	h, _ := hashData("sha256", []byte("data"))
	if err := s.Put(ctx, h, errReader{err: io.ErrClosedPipe}); err == nil {
		t.Fatal("Put with failing reader must error")
	}
	if n, _ := s.Clean(ctx, 0); n != 0 {
		t.Fatalf("Clean after failed Put removed %d files, want 0 (temp cleaned)", n)
	}
	if ok, _ := s.Exists(ctx, h); ok {
		t.Fatal("object exists after failed Put")
	}

	a, _ := hashData("sha256", []byte("keep"))
	b, _ := hashData("sha256", []byte("drop"))
	for _, x := range []struct {
		h cas.Hash
		d string
	}{{a, "keep"}, {b, "drop"}} {
		if err := s.Put(ctx, x.h, strings.NewReader(x.d)); err != nil {
			t.Fatal(err)
		}
	}
	doomed, err := s.Prune(ctx, []cas.Hash{a}, 0, true)
	if err != nil || len(doomed) != 1 || !doomed[0].Equal(b) {
		t.Fatalf("prune dry-run = %v, %v; want [b]", doomed, err)
	}
	if _, err := s.Prune(ctx, []cas.Hash{a}, 0, false); err != nil {
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
// same hash is a no-op success, and reading back returns the exact bytes.
func TestPutIdempotent(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("idempotent content bytes")
	h, _ := hashData("sha256", content)
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
	h, _ := hashData("sha256", content)
	path := s.hashPath(h)
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
	h, _ := hashData("sha256", []byte("never stored"))
	rc, err := s.Get(ctx, h)
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get missing = %v, %v; want ErrNotFound", rc, err)
	}
	if rc != nil {
		rc.Close()
	}
}

// TestExistsPresentAndMissing exercises Exists for a stored and a missing hash.
func TestExistsPresentAndMissing(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("exists check")
	h, _ := hashData("sha256", content)
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
	h, _ := hashData("sha256", content)
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
	missing, _ := hashData("sha256", []byte("never put"))
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
	h, _ := hashData("sha256", content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Size(ctx, h); err != nil || n != int64(len(content)) {
		t.Fatalf("Size = %d, %v; want %d,nil", n, err, len(content))
	}
	missing, _ := hashData("sha256", []byte("missing"))
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
	h, _ := hashData("sha256", content)
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

// TestStats exercises Stats per-algorithm counts + total size and String.
func TestStats(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	a := []byte("stats-content-a")
	b := []byte("stats-content-b-longer")
	ha, _ := hashData("sha256", a)
	hb, _ := hashData("sha256", b)
	for _, x := range []struct {
		h cas.Hash
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
	if st.AlgorithmCounts["sha256"] != 2 {
		t.Fatalf("sha256 count = %d, want 2", st.AlgorithmCounts["sha256"])
	}
	if want := int64(len(a) + len(b)); st.TotalSize != want {
		t.Fatalf("TotalSize = %d, want %d", st.TotalSize, want)
	}
	str := st.String()
	if !strings.Contains(str, "2 objects") || !strings.Contains(str, "sha256=2") {
		t.Fatalf("String() = %q; want '2 objects' and 'sha256=2'", str)
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
	h, _ := hashData("sha256", content)
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
// an intact object, ErrHashMismatch after corruption, ErrNotFound when missing.
func TestVerifyStreaming(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("verify streaming content")
	h, _ := hashData("sha256", content)
	if err := s.Put(ctx, h, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); err != nil {
		t.Fatalf("Verify intact = %v", err)
	}
	// Corrupt the stored bytes.
	if err := os.WriteFile(s.hashPath(h), []byte("tampered!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); !errors.Is(err, cas.ErrHashMismatch) {
		t.Fatalf("Verify corrupt = %v, want ErrHashMismatch", err)
	}
	missing, _ := hashData("sha256", []byte("absent"))
	if err := s.Verify(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Verify missing = %v, want ErrNotFound", err)
	}
}

// TestGC verifies mark-and-sweep removes unreachable objects and keeps roots.
func TestGC(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	keep := []byte("gc keep")
	drop := []byte("gc drop")
	hk, _ := hashData("sha256", keep)
	hd, _ := hashData("sha256", drop)
	for _, x := range []struct {
		h cas.Hash
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
	h, _ := hashData("sha256", content)
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

// TestListAlgorithmFilter checks List filters by algorithm.
func TestListAlgorithmFilter(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	for _, content := range []string{"list-a", "list-b", "list-c"} {
		h, _ := hashData("sha256", []byte(content))
		if err := s.Put(ctx, h, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List(ctx, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("List(\"\") = %v, %v; want 3 hashes", all, err)
	}
	sha, err := s.List(ctx, "sha256")
	if err != nil || len(sha) != 3 {
		t.Fatalf("List(\"sha256\") = %v, %v; want 3", sha, err)
	}
	other, err := s.List(ctx, "sha1")
	if err != nil || len(other) != 0 {
		t.Fatalf("List(\"sha1\") = %v, %v; want empty", other, err)
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

// TestHashPathShortDigestClamp exercises hashPath's digest clamping for an
// algorithm whose hex digest is shorter than the configured fan-out depth.
func TestHashPathShortDigestClamp(t *testing.T) {
	cas.RegisterHash("obvhc", func(data []byte) cas.Hash {
		sum := sha256.Sum256(data)
		h, _ := cas.NewHash("obvhc", sum[:3]) // 6 hex chars
		return h
	})
	sum := sha256.Sum256([]byte("clamp me"))
	h, err := cas.NewHash("obvhc", sum[:3])
	if err != nil {
		t.Fatal(err)
	}
	digest := h.String()[strings.IndexByte(h.String(), ':')+1:] // 6 hex chars
	for _, opts := range [][]backend.Option{
		{WithFanOut(4), WithFanLevels(2)}, // 2nd chunk clamps end: 8 > 6
		{WithFanOut(4), WithFanLevels(3)}, // 3rd level breaks: start 8 >= 6
	} {
		s := mustFS(t, opts...)
		if err := s.Put(context.Background(), h, strings.NewReader("x")); err != nil {
			t.Fatalf("opts %v: Put: %v", opts, err)
		}
		if p := s.hashPath(h); filepath.Base(p) != digest {
			t.Errorf("opts %v: basename = %q, want %q", opts, filepath.Base(p), digest)
		}
	}
}

// TestDeleteDirectoryError exercises Delete's non-IsNotExist error branch by
// pointing it at a non-empty directory (os.Remove cannot remove it).
func TestDeleteDirectoryError(t *testing.T) {
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("dir-as-object"))
	dir := s.hashPath(h)
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), h); err == nil {
		t.Fatal("Delete of a non-empty directory must error")
	}
}

// TestStatsRootStray verifies Stats ignores stray files that are not objects
// (a single-segment relative path that cannot parse as a hash) while still
// counting real objects.
func TestStatsRootStray(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("counted object")
	h, _ := hashData("sha256", content)
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
	h, _ := hashData("sha256", obj)
	if err := s.Put(ctx, h, strings.NewReader(string(obj))); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, ""); err == nil {
		t.Error("List over a removed base must error")
	}
	if _, err := s.Stats(ctx); err == nil {
		t.Error("Stats over a removed base must error")
	}
	// Clean's walk callback swallows the root error, so it reports success
	// with nothing removed (exercising that callback error branch).
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
	h, _ := hashData("sha256", []byte("dir not an object"))
	if err := os.MkdirAll(s.hashPath(h), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, h); err == nil {
		t.Fatal("Verify over a directory-as-object must error")
	}
}

// TestVerifyFallbackReadOnDirectory drives the buffered-fallback read path
// (io.ReadAll) of Verify for a one-shot-only algorithm whose object path is a
// directory, so the read must fail.
func TestVerifyFallbackReadOnDirectory(t *testing.T) {
	cas.RegisterHash("obvd", func(data []byte) cas.Hash {
		sum := sha256.Sum256(data)
		h, _ := cas.NewHash("obvd", sum[:])
		return h
	})
	sum := sha256.Sum256([]byte("dir not an object"))
	h, err := cas.NewHash("obvd", sum[:])
	if err != nil {
		t.Fatal(err)
	}
	s := mustFS(t)
	if err := os.MkdirAll(s.hashPath(h), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(context.Background(), h); err == nil {
		t.Fatal("Verify (fallback) over a directory-as-object must error")
	}
}

// TestPutCreateTempExhausted fills every temp-file candidate name for an
// object and confirms Put surfaces the createTempExcl exhaustion error instead
// of hanging or looping past the bound.
func TestPutCreateTempExhausted(t *testing.T) {
	s := mustFS(t)
	ctx := context.Background()
	content := []byte("temp namespace exhausted")
	h, _ := hashData("sha256", content)
	dir := filepath.Dir(s.hashPath(h))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create <path>.tmp and <path>.tmp.1 .. <path>.tmp.9999 so every
	// candidate name in createTempExcl's retry loop already exists.
	base := s.hashPath(h) + ".tmp"
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
