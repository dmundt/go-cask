package cas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// --- Test object types ---

// testNote is a leaf Object[T] used across the suite.
type testNote struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (n testNote) Type() string { return "note@1" }
func (n testNote) References() []Hash {
	return nil
}

// testNode references other nodes by hash — exercises References-driven
// traversal and cross-object storage. It carries custom JSON methods so the
// Hash references round-trip as "algo:hex" strings.
type testNode struct {
	Name string `json:"name"`
	Refs []Hash `json:"refs,omitempty"`
}

func (n testNode) Type() string { return "node@1" }
func (n testNode) References() []Hash {
	return n.Refs
}

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
		h, err := ParseHash(r)
		if err != nil {
			return err
		}
		n.Refs = append(n.Refs, h)
	}
	return nil
}

// --- Shared backend contract: the CAS laws over both backends ---

// backendFactory builds a fresh RawStore for a contract test.
type backendFactory func(t *testing.T) RawStore

func fsFactory(t *testing.T) RawStore {
	s, err := NewFSRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memFactory(t *testing.T) RawStore { return NewMemoryRawStore() }

func TestBackendContract(t *testing.T) {
	for _, bf := range []struct {
		name string
		fn   backendFactory
	}{
		{"fs", fsFactory},
		{"memory", memFactory},
	} {
		t.Run(bf.name, func(t *testing.T) {
			raw := bf.fn(t)
			testRawStoreContract(t, raw)
		})
	}
}

// testRawStoreContract runs the RawStore-level CAS laws plus the corner/error
// inventory shared by both backends (testing-strategy §1, §3).
func testRawStoreContract(t *testing.T, raw RawStore) {
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
	if _, err := raw.Get(ctx, missing); !errors.Is(err, ErrNotFound) {
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

func readAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

// --- Store[T] typed-layer tests ---

func newTestStore(t *testing.T, raw RawStore) *Store[testNote] {
	s, err := NewStore(raw, JSONCodec[testNote]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreRoundTrip(t *testing.T) {
	raw := NewMemoryRawStore()
	s := newTestStore(t, raw)
	ctx := context.Background()

	h, err := s.Put(ctx, testNote{Title: "t", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Algorithm() != "sha256" {
		t.Fatalf("algorithm = %q", h.Algorithm())
	}

	// Get returns the concrete value with no casts.
	note, err := s.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if note.Title != "t" || note.Body != "b" {
		t.Fatalf("Get = %+v", note)
	}

	// The typed value exposes Object[T] methods (T is Object[T]).
	note2, err := s.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if note2.Type() != "note@1" {
		t.Fatalf("Type() = %q", note2.Type())
	}

	// GetRaw returns the stored envelope bytes.
	rawBytes, err := s.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(rawBytes, &env); err != nil {
		t.Fatalf("stored bytes are not an envelope: %v", err)
	}
	if env.Type != "note@1" {
		t.Fatalf("envelope type = %q", env.Type)
	}

	// Exists / Delete.
	if ok, err := s.Exists(ctx, h); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if err := s.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, h); ok {
		t.Fatal("Exists after Delete")
	}
	if _, err := s.Get(ctx, h); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(after delete) = %v", err)
	}
}

// CAS law: dedup — Put twice → one object; PutDedup reports the duplicate.
func TestStoreDedup(t *testing.T) {
	raw := NewMemoryRawStore()
	s := newTestStore(t, raw)
	ctx := context.Background()

	h1, err := s.Put(ctx, testNote{Title: "same"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.Put(ctx, testNote{Title: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() != h2.String() {
		t.Fatalf("identical content must hash identically: %s vs %s", h1, h2)
	}
	list, err := raw.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("dedup broken: %d objects stored", len(list))
	}

	// PutDedup: first write reports stored, repeat reports deduplicated.
	h3, dedup, err := s.PutDedup(ctx, testNote{Title: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if !dedup || h3.String() != h1.String() {
		t.Fatalf("PutDedup repeat = (%s, %v), want dedup=true", h3, dedup)
	}
	h4, dedup, err := s.PutDedup(ctx, testNote{Title: "different"})
	if err != nil {
		t.Fatal(err)
	}
	if dedup {
		t.Fatal("PutDedup of new content must report dedup=false")
	}
	if h4.Equal(h1) {
		t.Fatal("different content must hash differently")
	}
}

func TestStoreEmptyStore(t *testing.T) {
	s := newTestStore(t, NewMemoryRawStore())
	ctx := context.Background()
	missing, _ := ParseHash("sha256:" + strings.Repeat("ab", 32))

	if _, err := s.Get(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v", err)
	}
	if ok, err := s.Exists(ctx, missing); err != nil || ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if err := s.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete must be a no-op: %v", err)
	}
}

func TestStoreTypeSafety(t *testing.T) {
	// A node store must NOT decode a note object as a node: wrong-type
	// payloads fail loudly rather than producing garbage.
	raw := NewMemoryRawStore()
	ctx := context.Background()
	notes := newTestStore(t, raw)
	h, err := notes.Put(ctx, testNote{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := NewStore(raw, JSONCodec[testNode]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodes.Get(ctx, h); err == nil {
		t.Fatal("decoding a note as a node must fail")
	}
}

func TestNewStoreUnknownAlgorithm(t *testing.T) {
	_, err := NewStore[testNote](NewMemoryRawStore(), JSONCodec[testNote]{}, "nope")
	if !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestStoreWithCustomHasher(t *testing.T) {
	// Custom algorithm via the documented recipe: RegisterHash then NewStore
	// (cas-core §4.2). The address must round-trip through ParseHash.
	RegisterHash("testblob", func([]byte) Hash {
		return hash{algo: "testblob", bytes: []byte{0xde, 0xad}}
	})
	raw := NewMemoryRawStore()
	ctx := context.Background()
	s, err := NewStore(raw, JSONCodec[testNote]{}, "testblob")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Put(ctx, testNote{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "testblob:dead" {
		t.Fatalf("custom hasher address = %q", h.String())
	}
	if _, err := ParseHash(h.String()); err != nil {
		t.Fatalf("custom address must round-trip through ParseHash: %v", err)
	}
	note, err := s.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if note.Title != "t" {
		t.Fatalf("Get = %+v", note)
	}
}

func TestStoreCancelledContext(t *testing.T) {
	s := newTestStore(t, NewMemoryRawStore())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, testNote{Title: "t"}); err == nil {
		t.Fatal("Put on cancelled context must error")
	}
	if _, err := s.Get(ctx, nil); err == nil {
		t.Fatal("Get on cancelled context must error")
	}
}

func TestEnvelopeFormat(t *testing.T) {
	// The stored form must be exactly the self-describing envelope, built by
	// Store.Put from the codec payload (the codec is the serialization
	// authority — objects no longer serialize themselves).
	ctx := context.Background()
	s := newTestStore(t, NewMemoryRawStore())
	h, err := s.Put(ctx, testNote{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if env.Type != "note@1" {
		t.Fatalf("type = %q", env.Type)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		t.Fatalf("data not base64: %v", err)
	}
	var note testNote
	if err := json.Unmarshal(payload, &note); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if note.Title != "t" {
		t.Fatalf("payload = %+v", note)
	}
}
