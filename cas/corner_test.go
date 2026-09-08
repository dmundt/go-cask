package cas_test

// Corner-case and explicitness tests for the cas core (testing-strategy
// §1.1): envelope parsing branches, context cancellation across every
// backend/store operation, memory-store semantics, custom one-shot hash
// paths, and filesystem error paths that are portable to test.
//
// Envelope-parsing internals are pinned in cas/envelope_test.go (internal,
// package cas). The filesystem-only layout/error paths that need unexported
// fields live in cas/backend/fs/fs_test.go.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/internal/test"
)

// TestContextCancellationFS verifies every FSBackend operation honors a
// canceled context (no filesystem side effects happen).
func TestContextCancellationFS(t *testing.T) {
	s, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, err := test.HashData("sha256", []byte("x"))
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

// TestMemoryBackendSuite covers the in-memory backend contract directly:
// round-trip, idempotence, filtering, error paths, and canceled contexts.
func TestMemoryBackendSuite(t *testing.T) {
	m := mem.New()
	ctx := context.Background()
	h1, _ := test.HashData("sha256", []byte("alpha"))
	h2, _ := test.HashData("sha256", []byte("beta"))

	if err := m.Put(ctx, h1, strings.NewReader("alpha")); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-Put keeps one entry.
	if err := m.Put(ctx, h1, strings.NewReader("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(ctx, h2, strings.NewReader("beta")); err != nil {
		t.Fatal(err)
	}
	rc, err := m.Get(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(data) != "alpha" {
		t.Fatalf("Get = %q, %v", data, err)
	}
	missing, _ := test.HashData("sha256", []byte("missing"))
	if _, err := m.Get(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}
	if ok, _ := m.Exists(ctx, missing); ok {
		t.Fatal("Exists(missing) = true")
	}
	if err := m.Delete(ctx, missing); err != nil { // missing is a no-op
		t.Fatal(err)
	}
	if err := m.Delete(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.Exists(ctx, h2); ok {
		t.Fatal("deleted object still present")
	}
	// List is sorted and filters by algorithm.
	all, err := m.List(ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("List = %v, %v", all, err)
	}
	if all[0].Algorithm() != "sha256" {
		t.Fatalf("List[0] algorithm = %q", all[0].Algorithm())
	}
	if got, _ := m.List(ctx, "sha1"); len(got) != 0 {
		t.Fatalf("List(sha1) = %v, want empty", got)
	}

	// Canceled context: every op errors without touching state.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"Put", func() error { return m.Put(cctx, h1, strings.NewReader("x")) }},
		{"Get", func() error { _, err := m.Get(cctx, h1); return err }},
		{"Exists", func() error { _, err := m.Exists(cctx, h1); return err }},
		{"Delete", func() error { return m.Delete(cctx, h1) }},
		{"List", func() error { _, err := m.List(cctx, ""); return err }},
	} {
		t.Run("canceled/"+tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}

	// Put with a failing reader leaves no entry behind.
	errReader := errReader{err: io.ErrUnexpectedEOF}
	if err := m.Put(context.Background(), h1, errReader); err == nil {
		t.Fatal("Put with failing reader must error")
	}
	if ok, _ := m.Exists(ctx, h1); !ok {
		t.Fatal("previous entry must survive the failed Put")
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestFSBackendErrorPaths covers portable FSBackend failures: constructor
// over a file, Put with a failing reader (temp cleaned up), and Prune at
// minAge 0.
func TestFSBackendErrorPaths(t *testing.T) {
	// NewFSBackend over an existing file must fail (MkdirAll error).
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.New(file); err == nil {
		t.Fatal("NewFSBackend over an existing file must error")
	}

	ctx := context.Background()
	s, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, _ := test.HashData("sha256", []byte("data"))
	// Failing reader: Put errors and the temp file is removed (no object).
	if err := s.Put(ctx, h, errReader{err: io.ErrClosedPipe}); err == nil {
		t.Fatal("Put with failing reader must error")
	}
	if n, _ := s.Clean(ctx, 0); n != 0 {
		t.Fatalf("Clean after failed Put removed %d files, want 0 (temp cleaned)", n)
	}
	if ok, _ := s.Exists(ctx, h); ok {
		t.Fatal("object exists after failed Put")
	}

	// Prune with minAge 0: every unreachable object is doomed.
	a, _ := test.HashData("sha256", []byte("keep"))
	b, _ := test.HashData("sha256", []byte("drop"))
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

// TestHashOneShotRegistration pins the one-shot-only hash paths: HashBytes
// works through the registry, NewHasher rejects non-streamable algorithms.
func TestHashOneShotRegistration(t *testing.T) {
	cas.RegisterHash("obone", func(data []byte) cas.Hash {
		sum := sha256.Sum256(data)
		h, _ := cas.NewHash("obone", sum[:])
		return h
	})
	want := sha256.Sum256([]byte("abc"))
	h, err := cas.HashBytes("obone", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "obone:"+hex.EncodeToString(want[:]) {
		t.Fatalf("HashBytes = %q", h.String())
	}
	if _, err := cas.NewHasher("obone"); !errors.Is(err, cas.ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(one-shot) err = %v, want ErrUnknownAlgorithm", err)
	}
	// And the streaming built-in still works.
	if hs, err := cas.NewHasher("sha256"); err != nil || hs == nil {
		t.Fatalf("NewHasher(sha256) = %v, %v", hs, err)
	}
}

// TestStoreGetLegacyEnvelope verifies a legacy unversioned type name
// (without @major) decodes (reads as @1) and round-trips through Get.
func TestStoreGetLegacyEnvelope(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	st, err := cas.New(raw, jsoncodec.New[test.Note](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := (jsoncodec.New[test.Note]()).Encode(test.Note{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy form: TLV envelope with type "note" (no @major).
	// unmarshalEnvelope appends @1 when the type has no '@', so
	// "note" → "note@1".
	var buf bytes.Buffer
	buf.WriteByte(1) // version
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note")
	buf.Write(payload)
	env := buf.Bytes()
	h, err := test.HashData("sha256", env)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Put(ctx, h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, h)
	if err != nil {
		t.Fatalf("Get(legacy envelope) = %v", err)
	}
	if got.Title != "legacy" {
		t.Fatalf("Get = %+v", got)
	}
}

// TestStoreCanceledOps verifies the typed store short-circuits canceled
// contexts on Put, PutDedup, GetRaw, and Get (via GetRaw).
func TestStoreCanceledOps(t *testing.T) {
	st, err := cas.New(mem.New(), jsoncodec.New[test.Note](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, _ := test.HashData("sha256", []byte("x"))
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"Put", func() error { _, err := st.Put(ctx, test.Note{Title: "t"}); return err }},
		{"PutDedup", func() error { _, _, err := st.PutDedup(ctx, test.Note{Title: "t"}); return err }},
		{"GetRaw", func() error { _, err := st.GetRaw(ctx, h); return err }},
		{"Get", func() error { _, err := st.Get(ctx, h); return err }},
		{"Exists", func() error { _, err := st.Exists(ctx, h); return err }},
		{"Delete", func() error { return st.Delete(ctx, h) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		})
	}
}

// TestWalkerRecursionErrors covers walker behavior below the root: a
// missing reference mid-graph surfaces ErrNotFound, and a visit error from a
// child propagates.
func TestWalkerRecursionErrors(t *testing.T) {
	ctx := context.Background()
	st, err := cas.New(mem.New(), jsoncodec.New[test.Node](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []cas.Hash{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	missingH, _ := test.HashData("sha256", []byte("missing"))
	brokenH, err := st.Put(ctx, test.Node{Name: "broken", Refs: []cas.Hash{missingH}})
	if err != nil {
		t.Fatal(err)
	}

	// A missing reference during recursion → ErrNotFound.
	w := cas.NewWalker(st, func(test.Node) error { return nil })
	if err := w.Walk(ctx, brokenH); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk over broken ref = %v, want ErrNotFound", err)
	}

	// A visit error from a child propagates (not just from the root).
	seen := 0
	w2 := cas.NewWalker(st, func(o test.Node) error {
		seen++
		if o.References() == nil { // the leaf
			return errors.New("stop at leaf")
		}
		return nil
	})
	if err := w2.Walk(ctx, rootH); err == nil || err.Error() != "stop at leaf" {
		t.Fatalf("Walk child error = %v", err)
	}
	if seen != 2 {
		t.Fatalf("visited %d objects, want root+leaf = 2", seen)
	}
}
