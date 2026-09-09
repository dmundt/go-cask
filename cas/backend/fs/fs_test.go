package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
