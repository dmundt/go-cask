package cas_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// backendFactory builds a backend for the shared contract test.
type backendFactory func(t *testing.T) cas.Backend

// untypedObj is an Object[T] whose Type() is empty: Put must reject it rather
// than write an envelope whose type parseEnvelope rejects on read.
type untypedObj struct{}

func (untypedObj) Type() string             { return "" }
func (untypedObj) References() []cas.Digest { return nil }

func TestStoreRejectsEmptyTypeName(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[untypedObj](), sha256.New())
	if _, err := s.Put(ctx, untypedObj{}); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Put(empty type) = %v, want ErrUnknownType", err)
	}
	if _, _, err := s.PutDedup(ctx, untypedObj{}); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("PutDedup(empty type) = %v, want ErrUnknownType", err)
	}
}

func fsFactory(t *testing.T) cas.Backend {
	s, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memFactory(t *testing.T) cas.Backend { return mem.New() }

// testBackendContract exercises the Backend contract (Put/Get round-trip)
// against any backend implementation.
func testBackendContract(t *testing.T, raw cas.Backend) {
	ctx := context.Background()
	h := test.DigestData([]byte("contract"))
	if err := raw.Put(ctx, h, strings.NewReader("contract")); err != nil {
		t.Fatal(err)
	}
	rc, err := raw.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := test.ReadAllAndClose(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "contract" {
		t.Fatalf("got %q", got)
	}
}

func newTestStore(t *testing.T, raw cas.Backend) *cas.Store[test.Note] {
	t.Helper()
	return cas.New(raw, jsoncodec.New[test.Note](), sha256.New())
}

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
	if h.IsZero() || len(h) != sha256.Size {
		t.Fatalf("digest = %q, want %d bytes", h, sha256.Size)
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
	list, err := raw.List(ctx)
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
	missing := sha256.Of([]byte("never stored"))

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
	nodes := cas.New(raw, jsoncodec.New[test.Node](), sha256.New())
	if _, err := nodes.Get(ctx, h); err == nil {
		t.Fatal("decoding a note as a node must fail")
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
	s := cas.New(mem.New(), jsoncodec.New[test.Note](), sha256.New())
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

// TestStoreGetLegacyEnvelope verifies a legacy unversioned type name (without
// @major) decodes (reads as @1) and round-trips through Get.
func TestStoreGetLegacyEnvelope(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	st := cas.New(raw, jsoncodec.New[test.Note](), sha256.New())
	payload, err := (jsoncodec.New[test.Note]()).Marshal(test.Note{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy form: TLV envelope with type "note" (no @major). unmarshalEnvelope
	// appends @1 when the type has no '@', so "note" -> "note@1".
	var buf bytes.Buffer
	buf.WriteByte(1) // version
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note")
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	env := buf.Bytes()
	h := test.DigestData(env)
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
	st := cas.New(mem.New(), jsoncodec.New[test.Note](), sha256.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := test.DigestData([]byte("x"))
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
