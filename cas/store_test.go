package cas_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/internal/test"
)

// Shared test object types (test.Note, test.Node), backend factories
// (backendFactory, fsFactory, memFactory), the backend contract
// (testBackendContract), readAllAndClose and newTestStore are defined in
// external_test.go.

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
			testBackendContract(t, raw)
		})
	}
}

func TestStoreRoundTrip(t *testing.T) {
	raw := mem.New()
	s := newTestStore(t, raw)
	ctx := context.Background()

	h, err := s.Put(ctx, test.Note{Title: "t", Body: "b"})
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

	// GetRaw returns the stored bytes in the self-describing TLV envelope
	// form: [version][uvarint typeLen][type][codec payload].
	rawBytes, err := s.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	env, err := cas.EnvelopeFromBytes(rawBytes)
	if err != nil {
		t.Fatalf("EnvelopeFromBytes(stored) = %v", err)
	}
	if env.Type != "note@1" {
		t.Fatalf("stored type = %q, want note@1", env.Type)
	}
	var payloadNote test.Note
	if err := json.Unmarshal(env.Data, &payloadNote); err != nil {
		t.Fatalf("stored payload is not JSON: %v", err)
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
	if _, err := s.Get(ctx, h); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(after delete) = %v", err)
	}
}

// CAS law: dedup — Put twice → one object; PutDedup reports the duplicate.
func TestStoreDedup(t *testing.T) {
	raw := mem.New()
	s := newTestStore(t, raw)
	ctx := context.Background()

	h1, err := s.Put(ctx, test.Note{Title: "same"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.Put(ctx, test.Note{Title: "same"})
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
	h3, dedup, err := s.PutDedup(ctx, test.Note{Title: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if !dedup || h3.String() != h1.String() {
		t.Fatalf("PutDedup repeat = (%s, %v), want dedup=true", h3, dedup)
	}
	h4, dedup, err := s.PutDedup(ctx, test.Note{Title: "different"})
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
	s := newTestStore(t, mem.New())
	ctx := context.Background()
	missing, _ := cas.ParseHash("sha256:" + strings.Repeat("ab", 32))

	if _, err := s.Get(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
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
	raw := mem.New()
	ctx := context.Background()
	notes := newTestStore(t, raw)
	h, err := notes.Put(ctx, test.Note{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := cas.New(raw, jsoncodec.New[test.Node](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodes.Get(ctx, h); err == nil {
		t.Fatal("decoding a note as a node must fail")
	}
}

func TestNewStoreUnknownAlgorithm(t *testing.T) {
	_, err := cas.New[test.Note](mem.New(), jsoncodec.New[test.Note](), "nope")
	if !errors.Is(err, cas.ErrUnknownAlgorithm) {
		t.Fatalf("err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestStoreWithCustomHasher(t *testing.T) {
	// Custom algorithm via the documented recipe: RegisterHash then NewStore
	// (cas-core §4.2). The address must round-trip through ParseHash.
	cas.RegisterHash("testblob", func([]byte) cas.Hash {
		h, _ := cas.NewHash("testblob", []byte{0xde, 0xad})
		return h
	})
	raw := mem.New()
	ctx := context.Background()
	s, err := cas.New(raw, jsoncodec.New[test.Note](), "testblob")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Put(ctx, test.Note{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "testblob:dead" {
		t.Fatalf("custom hasher address = %q", h.String())
	}
	if _, err := cas.ParseHash(h.String()); err != nil {
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
	s := newTestStore(t, mem.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, test.Note{Title: "t"}); err == nil {
		t.Fatal("Put on cancelled context must error")
	}
	if _, err := s.Get(ctx, nil); err == nil {
		t.Fatal("Get on cancelled context must error")
	}
}

func TestEnvelopeFormat(t *testing.T) {
	// The stored form must be exactly the self-describing TLV envelope
	// [version][uvarint typeLen][type][codec payload], built by Store.Put
	// from the codec payload (the codec is the serialization authority —
	// objects no longer serialize themselves).
	ctx := context.Background()
	s := newTestStore(t, mem.New())
	h, err := s.Put(ctx, test.Note{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	env, err := cas.EnvelopeFromBytes(data)
	if err != nil {
		t.Fatalf("EnvelopeFromBytes = %v", err)
	}
	if env.Type != "note@1" {
		t.Fatalf("type = %q, want note@1", env.Type)
	}
	var note test.Note
	if err := json.Unmarshal(env.Data, &note); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if note.Title != "t" {
		t.Fatalf("payload = %+v", note)
	}
}

func TestStorePutDedup(t *testing.T) {
	ctx := context.Background()
	s, err := cas.New(mem.New(), jsoncodec.New[test.Note](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	h, dedup, err := s.PutDedup(ctx, test.Note{Title: "dedup"})
	if err != nil {
		t.Fatal(err)
	}
	if dedup {
		t.Fatal("first put must not be dedup")
	}
	_, dedup, err = s.PutDedup(ctx, test.Note{Title: "dedup"})
	if err != nil {
		t.Fatal(err)
	}
	if !dedup {
		t.Fatal("second put must be dedup")
	}
	_ = h
}
