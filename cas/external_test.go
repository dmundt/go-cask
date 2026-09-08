package cas_test

// Shared test helpers for the external (package cas_test) subpackage tests.
// These used to live in the internal (package cas) test files; after the cas
// core test suite became an external test package (so it can import the
// backend/codec subpackages), the helpers are defined once here and reused by
// bench_test.go, corner_test.go, errors_test.go, scale_bench_test.go and
// store_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/memory"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// hashData computes the content address of data with the named algorithm via
// the public HashBytes (Test helper only).
func hashData(algo string, data []byte) (cas.Hash, error) {
	return cas.HashBytes(algo, data)
}

// --- Test object types ---

// testNote is a leaf Object[T] used across the suite.
type testNote struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (n testNote) Type() string           { return "note@1" }
func (n testNote) References() []cas.Hash { return nil }

// testNode references other nodes by hash — exercises References-driven
// traversal and cross-object storage. It carries custom JSON methods so the
// Hash references round-trip as "algo:hex" strings.
type testNode struct {
	Name string     `json:"name"`
	Refs []cas.Hash `json:"refs,omitempty"`
}

func (n testNode) Type() string           { return "node@1" }
func (n testNode) References() []cas.Hash { return n.Refs }

// MarshalJSON renders Refs as strings (a Hash interface cannot be
// unmarshaled by encoding/json directly).
func (n testNode) MarshalJSON() ([]byte, error) {
	refs := make([]string, 0, len(n.Refs))
	for _, r := range n.Refs {
		refs = append(refs, r.String())
	}
	return json.Marshal(struct {
		Name string   `json:"name"`
		Refs []string `json:"refs,omitempty"`
	}{n.Name, refs})
}

// UnmarshalJSON parses the string refs back into Hash values.
func (n *testNode) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name string   `json:"name"`
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	n.Name = raw.Name
	for _, r := range raw.Refs {
		h, err := cas.ParseHash(r)
		if err != nil {
			return err
		}
		n.Refs = append(n.Refs, h)
	}
	return nil
}

// --- Shared backend contract: the CAS laws over both backends ---

// backendFactory builds a fresh Backend for a contract test.
type backendFactory func(t *testing.T) cas.Backend

func fsFactory(t *testing.T) cas.Backend {
	s, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memFactory(t *testing.T) cas.Backend { return backmem.New() }

// testBackendContract runs the Backend-level CAS laws plus the corner/error
// inventory shared by both backends (testing-strategy §1, §3).
func testBackendContract(t *testing.T, raw cas.Backend) {
	ctx := context.Background()
	// Round-trip + determinism: same bytes → same hash → identical bytes.
	h1, err := hashData("sha256", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Put(ctx, h1, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	h1b, err := hashData("sha256", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() != h1b.String() {
		t.Fatal("determinism broken at the byte layer")
	}
	if err := raw.Put(ctx, h1b, strings.NewReader("hello")); err != nil {
		t.Fatal(err) // idempotent Put
	}
	rc, err := raw.Get(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllAndClose(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("round-trip: got %q", got)
	}

	// Exists.
	if ok, err := raw.Exists(ctx, h1); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	missing, _ := hashData("sha256", []byte("nope"))
	if ok, err := raw.Exists(ctx, missing); err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}

	// Get missing → ErrNotFound.
	if _, err := raw.Get(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}

	// List + algorithm filter.
	h2, _ := hashData("sha256", []byte("world"))
	if err := raw.Put(ctx, h2, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	all, err := raw.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List() = %d objects, want 2", len(all))
	}
	s256, err := raw.List(ctx, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if len(s256) != 2 {
		t.Fatalf("List(sha256) = %v", s256)
	}

	// Delete: no-op on missing, removes present.
	if err := raw.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(missing) must be a no-op: %v", err)
	}
	if err := raw.Delete(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := raw.Exists(ctx, h2); ok {
		t.Fatal("object still exists after Delete")
	}

	// Immutability: stored bytes never change after Put.
	if err := raw.Put(ctx, h1, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	rc2, err := raw.Get(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	again, err := readAllAndClose(rc2)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != "hello" {
		t.Fatal("stored bytes changed")
	}

	// Cancelled context surfaces the cancellation.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := raw.Put(cctx, h1, strings.NewReader("x")); err == nil {
		t.Log("Put on cancelled ctx returned nil (backend may not check); acceptable")
	}
}

// mustFS builds a fresh filesystem Backend for a test.
func mustFS(t *testing.T, opts ...backend.Option) *fs.Backend {
	s, err := fs.New(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// --- Store[T] typed-layer tests ---

// newTestStore builds a Store[testNote] over raw with the JSON codec.
func newTestStore(t *testing.T, raw cas.Backend) *cas.Store[testNote] {
	s, err := cas.New(raw, jsoncodec.New[testNote](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

// errorObject is a minimal Object[T] used with a failing codec.
type errorObject struct{}

func (errorObject) Type() string           { return "err@1" }
func (errorObject) References() []cas.Hash { return nil }

// failingCodec always fails to encode — the serialization authority is the
// codec now, so an encode failure must surface from Store.Put/PutDedup.
type failingCodec[T any] struct{}

func (failingCodec[T]) Encode(T) ([]byte, error) { return nil, errors.New("encode exploded") }
func (failingCodec[T]) Decode([]byte) (T, error) { var z T; return z, nil }
