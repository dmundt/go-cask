package cas

// Corner-case and explicitness tests for the cas core (testing-strategy
// §1.1): envelope parsing branches, context cancellation across every
// backend/store operation, memory-store semantics, custom one-shot hash
// paths, and filesystem error paths that are portable to test.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnmarshalEnvelopeCases pins every unmarshalEnvelope branch, including
// the legacy unversioned type name (reads as "@1", object-versioning §2).
func TestUnmarshalEnvelopeCases(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantTyp string
		wantPld string
		wantErr error
	}{
		{"versioned type + payload", `{"type":"note@2","data":"aGVsbG8="}`, "note@2", "hello", nil},
		{"legacy unversioned type reads as @1", `{"type":"note","data":"aGVsbG8="}`, "note@1", "hello", nil},
		{"empty payload decodes to empty", `{"type":"note@1","data":""}`, "note@1", "", nil},
		{"missing type", `{"data":"aGVsbG8="}`, "", "", ErrUnknownType},
		{"empty type", `{"type":"","data":"aGVsbG8="}`, "", "", ErrUnknownType},
		{"data not base64", `{"type":"note@1","data":"%%%"}`, "", "", ErrUnknownType},
		{"not json", `garbage`, "", "", ErrUnknownType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ, pld, err := unmarshalEnvelope([]byte(tc.in))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || typ != tc.wantTyp || string(pld) != tc.wantPld {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, nil)", typ, pld, err, tc.wantTyp, tc.wantPld)
			}
		})
	}
}

// TestContextCancellationFS verifies every FSRawStore operation honors a
// canceled context (no filesystem side effects happen).
func TestContextCancellationFS(t *testing.T) {
	s, err := NewFSRawStore(t.TempDir())
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
		{"Prune", func() error { _, err := s.Prune(ctx, []Hash{h}, 0, true); return err }},
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

// TestMemoryRawStoreSuite covers the in-memory backend contract directly:
// round-trip, idempotence, filtering, error paths, and canceled contexts.
func TestMemoryRawStoreSuite(t *testing.T) {
	m := NewMemoryRawStore()
	ctx := context.Background()
	h1, _ := hashData("sha256", []byte("alpha"))
	h2, _ := hashData("sha256", []byte("beta"))

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
	missing, _ := hashData("sha256", []byte("missing"))
	if _, err := m.Get(ctx, missing); !errors.Is(err, ErrNotFound) {
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
	// A stored key that is not a parseable hash string is skipped by List
	// (can only happen via direct map access; Put keys are always valid).
	m.objects["not-a-hash"] = []byte("stray")
	if got, err := m.List(ctx, ""); err != nil || len(got) != 1 {
		t.Fatalf("List with stray key = %v, %v; want 1", got, err)
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

// TestFSRawStoreErrorPaths covers portable FSRawStore failures: constructor
// over a file, Put with a failing reader (temp cleaned up), and Prune at
// minAge 0.
func TestFSRawStoreErrorPaths(t *testing.T) {
	// NewFSRawStore over an existing file must fail (MkdirAll error).
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFSRawStore(file); err == nil {
		t.Fatal("NewFSRawStore over an existing file must error")
	}

	ctx := context.Background()
	s, err := NewFSRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, _ := hashData("sha256", []byte("data"))
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
	a, _ := hashData("sha256", []byte("keep"))
	b, _ := hashData("sha256", []byte("drop"))
	for _, x := range []struct {
		h Hash
		d string
	}{{a, "keep"}, {b, "drop"}} {
		if err := s.Put(ctx, x.h, strings.NewReader(x.d)); err != nil {
			t.Fatal(err)
		}
	}
	doomed, err := s.Prune(ctx, []Hash{a}, 0, true)
	if err != nil || len(doomed) != 1 || !doomed[0].Equal(b) {
		t.Fatalf("prune dry-run = %v, %v; want [b]", doomed, err)
	}
	if _, err := s.Prune(ctx, []Hash{a}, 0, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, b); ok {
		t.Fatal("unreachable object survived prune at minAge 0")
	}
	if ok, _ := s.Exists(ctx, a); !ok {
		t.Fatal("reachable root was pruned")
	}
}

// TestVerifyCustomOneShot exercises Verify's buffered fallback for hash
// algorithms registered only as one-shot HashFunc, plus the unknown-algo
// error path.
func TestVerifyCustomOneShot(t *testing.T) {
	RegisterHash("obvfy", func(data []byte) Hash {
		sum := sha256.Sum256(data)
		return hash{algo: "obvfy", bytes: sum[:]}
	})
	ctx := context.Background()
	s, err := NewFSRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("verify me with a one-shot algorithm")
	sum := sha256.Sum256(data)
	h := hash{algo: "obvfy", bytes: sum[:]}
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
	if err := s.Verify(ctx, h); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("Verify corrupt = %v, want ErrHashMismatch", err)
	}
	// An algorithm that is not registered at all → ErrUnknownAlgorithm.
	unknown := hash{algo: "noreg1", bytes: sum[:]}
	if err := s.Put(ctx, unknown, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, unknown); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("Verify unknown algo = %v, want ErrUnknownAlgorithm", err)
	}
}

// TestHashOneShotRegistration pins the one-shot-only hash paths: HashBytes
// works through the registry, NewHasher rejects non-streamable algorithms.
func TestHashOneShotRegistration(t *testing.T) {
	RegisterHash("obone", func(data []byte) Hash {
		sum := sha256.Sum256(data)
		return hash{algo: "obone", bytes: sum[:]}
	})
	want := sha256.Sum256([]byte("abc"))
	h, err := HashBytes("obone", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "obone:"+hex.EncodeToString(want[:]) {
		t.Fatalf("HashBytes = %q", h.String())
	}
	if _, err := NewHasher("obone"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(one-shot) err = %v, want ErrUnknownAlgorithm", err)
	}
	// And the streaming built-in still works.
	if hs, err := NewHasher("sha256"); err != nil || hs == nil {
		t.Fatalf("NewHasher(sha256) = %v, %v", hs, err)
	}
}

// TestStoreGetLegacyEnvelope verifies a legacy unversioned envelope
// type name decodes (reads as @1) and round-trips through Get.
func TestStoreGetLegacyEnvelope(t *testing.T) {
	ctx := context.Background()
	st, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := (JSONCodec[testNote]{}).Encode(testNote{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	env := []byte(`{"type":"note","data":"` + base64.StdEncoding.EncodeToString(payload) + `"}`)
	h, err := hashData("sha256", env)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.raw.Put(ctx, h, bytes.NewReader(env)); err != nil {
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

// TestCachedStoreCanceled verifies CachedStore.Get propagates a canceled
// context from the underlying store.
func TestCachedStoreCanceled(t *testing.T) {
	st, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCachedStore(st)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, _ := hashData("sha256", []byte("x"))
	if _, err := c.Get(ctx, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached Get err = %v, want context.Canceled", err)
	}
}

// TestStoreCanceledOps verifies the typed store short-circuits canceled
// contexts on Put, PutDedup, GetRaw, and Get (via GetRaw).
func TestStoreCanceledOps(t *testing.T) {
	st, err := NewStore(NewMemoryRawStore(), JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, _ := hashData("sha256", []byte("x"))
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"Put", func() error { _, err := st.Put(ctx, testNote{Title: "t"}); return err }},
		{"PutDedup", func() error { _, _, err := st.PutDedup(ctx, testNote{Title: "t"}); return err }},
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
	st, err := NewStore(NewMemoryRawStore(), JSONCodec[testNode]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	leafH, err := st.Put(ctx, testNode{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, testNode{Name: "root", Refs: []Hash{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	missingH, _ := hashData("sha256", []byte("missing"))
	brokenH, err := st.Put(ctx, testNode{Name: "broken", Refs: []Hash{missingH}})
	if err != nil {
		t.Fatal(err)
	}

	// A missing reference during recursion → ErrNotFound.
	w := NewWalker(st, func(testNode) error { return nil })
	if err := w.Walk(ctx, brokenH); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Walk over broken ref = %v, want ErrNotFound", err)
	}

	// A visit error from a child propagates (not just from the root).
	seen := 0
	w2 := NewWalker(st, func(o testNode) error {
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
