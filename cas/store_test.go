package cas_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	gzipcodec "github.com/dmundt/go-cask/cas/codec/gzip"
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
	s := cas.New(backmem.New(), jsoncodec.New[untypedObj](), sha256.New())
	if _, err := s.Put(ctx, untypedObj{}); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Put(empty type) = %v, want ErrUnknownType", err)
	}
	if _, _, err := s.PutDedup(ctx, untypedObj{}); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("PutDedup(empty type) = %v, want ErrUnknownType", err)
	}
}

// TestStoreWithJSONCodecStack covers the documented way to build a store: the
// core names no codec, so the client passes one to cas.New — plain JSON, or a
// compression wrapper stacked over JSON (cas-core §4.6, §7.2).
func TestStoreWithJSONCodecStack(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()

	jsonStore := cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
	jsonDigest, err := jsonStore.Put(ctx, test.Note{Title: "json"})
	if err != nil {
		t.Fatalf("Put through the JSON codec = %v", err)
	}

	gzipStore := cas.New(backend, gzipcodec.New(jsoncodec.New[test.Note]()), sha256.New())
	gzipDigest, err := gzipStore.Put(ctx, test.Note{Title: "gzip"})
	if err != nil {
		t.Fatalf("Put through the gzip-over-JSON stack = %v", err)
	}
	if jsonDigest.Equal(gzipDigest) {
		t.Fatal("the same value through a different codec stack must not share an address")
	}

	got, err := gzipStore.Get(ctx, gzipDigest)
	if err != nil {
		t.Fatalf("Get through the gzip stack = %v", err)
	}
	if got.Title != "gzip" {
		t.Fatalf("Get through the gzip stack = %+v, want Title %q", got, "gzip")
	}
}

func TestStoreCloseIsIdempotent(t *testing.T) {
	wantErr := errors.New("close failed")
	base := &closeTrackingBackend{Backend: backmem.New(), closeErr: wantErr}
	st := cas.New(base, jsoncodec.New[test.Note](), sha256.New())

	if err := st.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close() = %v, want %v", err, wantErr)
	}
	if err := st.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close() second call = %v, want %v", err, wantErr)
	}
	if base.closeCalls != 1 {
		t.Fatalf("closeCalls = %d, want 1", base.closeCalls)
	}
}

func TestStoreGetRawReportsBackendCloseFailure(t *testing.T) {
	digest := sha256.Of([]byte("payload"))
	want := errors.New("close failed")
	s := cas.New(
		closeErrorBackend{reader: failingCloseReader{Reader: bytes.NewReader([]byte("payload")), err: want}},
		jsoncodec.New[untypedObj](),
		sha256.New(),
	)
	if _, err := s.GetRaw(context.Background(), digest); !errors.Is(err, want) {
		t.Fatalf("GetRaw(close error) = %v, want %v", err, want)
	}
}

func fsFactory(t *testing.T) cas.Backend {
	s, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memFactory(t *testing.T) cas.Backend { return backmem.New() }

// testBackendContract exercises the Backend contract (Put/Get round-trip)
// against any backend implementation.
func testBackendContract(t *testing.T, backend cas.Backend) {
	ctx := context.Background()
	h := test.DigestData([]byte("contract"))
	if err := backend.Put(ctx, h, strings.NewReader("contract")); err != nil {
		t.Fatal(err)
	}
	rc, err := backend.Get(ctx, h)
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

func newTestStore(t *testing.T, backend cas.Backend) *cas.Store[test.Note] {
	t.Helper()
	return cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
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
			backend := bf.fn(t)
			testBackendContract(t, backend)
		})
	}
}

// TestStoreFramesThroughEncodeEnvelope pins that what Store.Put stores is
// exactly what the exported writer frames, so a tool that has to produce
// stored bytes without a Store — `cask seed-preview`, which derives each
// preview object's digest from its own frame — writes the same bytes and lands
// on the same digest (go-cask#187).
func TestStoreFramesThroughEncodeEnvelope(t *testing.T) {
	backend := backmem.New()
	s := newTestStore(t, backend)
	ctx := context.Background()
	note := test.Note{Title: "t", Body: "b"}

	h, err := s.Put(ctx, note)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := jsoncodec.New[test.Note]().Encode(note)
	if err != nil {
		t.Fatal(err)
	}
	want, err := cas.EncodeEnvelope("json", note.Type(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, want) {
		t.Fatalf("Store.Put stored %x, want the exported writer's %x", stored, want)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	backend := backmem.New()
	s := newTestStore(t, backend)
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

	// GetRaw returns the stored bytes in the self-describing TLV envelope form:
	// [version][uvarint codecLen][codec][uvarint typeLen][type][codec payload].
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
	if env.Codec != "json" {
		t.Fatalf("stored codec = %q, want json", env.Codec)
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

func TestStoreDedup(t *testing.T) {
	backend := backmem.New()
	s := newTestStore(t, backend)
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
	list, err := backend.List(ctx)
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
	s := newTestStore(t, backmem.New())
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

func TestStoreRecoveryAfterCorruption(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	s := newTestStore(t, backend)
	obj := test.Note{Title: "before", Body: "payload"}

	d, err := s.Put(ctx, obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, d, strings.NewReader("corrupted envelope")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(corrupted envelope) = %v, want ErrCorrupt", err)
	}

	if _, err := s.Put(ctx, obj); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get(rewritten valid object) = %v, want nil", err)
	}
	if got.Title != obj.Title || got.Body != obj.Body {
		t.Fatalf("Get(rewritten valid object) = %+v, want %+v", got, obj)
	}
}

func TestStoreTypeSafety(t *testing.T) {
	// A node store must NOT decode a note object as a node: wrong-type
	// payloads fail loudly rather than producing garbage.
	backend := backmem.New()
	ctx := context.Background()
	notes := newTestStore(t, backend)
	h, err := notes.Put(ctx, test.Note{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	nodes := cas.New(backend, jsoncodec.New[test.Node](), sha256.New())
	if _, err := nodes.Get(ctx, h); err == nil {
		t.Fatal("decoding a note as a node must fail")
	}
}

func TestStoreCancelledContext(t *testing.T) {
	s := newTestStore(t, backmem.New())
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
	// [version][uvarint codecLen][codec][uvarint typeLen][type][uvarint payloadLen][payload],
	// built by Store.Put from the codec payload (the codec is the serialization
	// authority — objects no longer serialize themselves). The codec identity
	// tag in it is the version 2 addition this test pins.
	ctx := context.Background()
	s := newTestStore(t, backmem.New())
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
	if env.Codec != "json" {
		t.Fatalf("codec = %q, want json", env.Codec)
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
	s := cas.New(backmem.New(), jsoncodec.New[test.Note](), sha256.New())
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
	backend := backmem.New()
	st := cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
	payload, err := (jsoncodec.New[test.Note]()).Encode(test.Note{Title: "legacy"})
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
	if err := backend.Put(ctx, h, bytes.NewReader(env)); err != nil {
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
// contexts on Put, PutDedup, GetRaw, Get, Exists, Delete and Type.
func TestStoreCanceledOps(t *testing.T) {
	st := cas.New(backmem.New(), jsoncodec.New[test.Note](), sha256.New())
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
		{"Type", func() error { _, err := st.Type(ctx, h); return err }},
		{"Version", func() error { _, err := st.Version(ctx, h); return err }},
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

// countingBackend wraps a Backend and counts the bytes served through Get, so a
// test can prove that a header peek never reads the payload.
type countingBackend struct {
	cas.Backend
	read int
}

func (b *countingBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	rc, err := b.Backend.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	return &countingReadCloser{rc: rc, n: &b.read}, nil
}

// countingReadCloser adds the bytes it serves to the counter.
type countingReadCloser struct {
	rc io.ReadCloser
	n  *int
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	*c.n += n
	return n, err
}

func (c *countingReadCloser) Close() error { return c.rc.Close() }

// TestStoreTypePeekDoesNotReadThePayload pins the point of the peek: Type reads
// the envelope header and stops, so a large object costs a header rather than a
// payload. That is what makes List plus Type the cheap way to enumerate a store
// by type, where Get would decode every object.
func TestStoreTypePeekDoesNotReadThePayload(t *testing.T) {
	ctx := context.Background()
	counted := &countingBackend{Backend: backmem.New()}
	s := newTestStore(t, counted)

	d, err := s.Put(ctx, test.Note{Title: "large", Body: strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	afterPut := counted.read

	typ, err := s.Type(ctx, d)
	if err != nil {
		t.Fatalf("Type = %v", err)
	}
	if typ != "note@1" {
		t.Fatalf("Type = %q, want note@1", typ)
	}
	peeked := counted.read - afterPut
	if peeked > 64 {
		t.Fatalf("Type read %d bytes, want only the header (the payload is %d bytes)", peeked, 1<<20)
	}

	// The counter is live: a Get over the same backend does read the payload.
	if _, err := s.Get(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := counted.read - afterPut - peeked; got < 1<<20 {
		t.Fatalf("Get read %d bytes, want the whole payload", got)
	}
}

// TestStoreTypeDoesNotAllocateThePayload states the same property in bytes
// allocated: the peek's cost must not grow with the object.
func TestStoreTypeDoesNotAllocateThePayload(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, backmem.New())
	d, err := s.Put(ctx, test.Note{Title: "large", Body: strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := s.Type(ctx, d); err != nil {
		t.Fatalf("Type = %v", err)
	}
	runtime.ReadMemStats(&after)

	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<16 {
		t.Fatalf("Type allocated %d bytes, want a header-sized cost (the payload is %d bytes)", grew, 1<<20)
	}
}

// TestStoreTypeAcrossBackends runs the peek over both shipped backends, so the
// contract holds where a reader is a file as well as where it is a buffer.
func TestStoreTypeAcrossBackends(t *testing.T) {
	for _, bf := range []struct {
		name string
		fn   backendFactory
	}{
		{"fs", fsFactory},
		{"memory", memFactory},
	} {
		t.Run(bf.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t, bf.fn(t))
			d, err := s.Put(ctx, test.Note{Title: "t", Body: "b"})
			if err != nil {
				t.Fatal(err)
			}
			typ, err := s.Type(ctx, d)
			if err != nil {
				t.Fatalf("Type = %v", err)
			}
			if typ != "note@1" {
				t.Fatalf("Type = %q, want note@1", typ)
			}
		})
	}
}

// TestStoreTypeReportsTheStoredType shows the peek is honest about what is on
// disk: it reports the stored type, including one this store cannot decode, and
// leaves the rejection to Get.
func TestStoreTypeReportsTheStoredType(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	notes := newTestStore(t, backend)
	d, err := notes.Put(ctx, test.Note{Title: "n"})
	if err != nil {
		t.Fatal(err)
	}

	others := cas.New(backend, jsoncodec.New[test.Node](), sha256.New())
	typ, err := others.Type(ctx, d)
	if err != nil {
		t.Fatalf("Type through another store = %v", err)
	}
	if typ != "note@1" {
		t.Fatalf("Type = %q, want the stored type note@1", typ)
	}
	if _, err := others.Get(ctx, d); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Get(a foreign type) = %v, want ErrUnknownType", err)
	}
}

// TestStoreTypeRejectsMissingAndCorruptKeys covers the peek's failure modes: an
// absent object is ErrNotFound, an absent digest is rejected before any read,
// and a damaged header is ErrCorrupt rather than a payload decode failure.
func TestStoreTypeRejectsMissingAndCorruptKeys(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	s := newTestStore(t, backend)

	if _, err := s.Type(ctx, sha256.Of([]byte("absent"))); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Type(missing) = %v, want ErrNotFound", err)
	}
	if _, err := s.Type(ctx, cas.Digest{}); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Type(absent digest) = %v, want ErrInvalidDigest", err)
	}

	// A stored object whose header is not a usable envelope: version 1 with an
	// empty type name.
	garbage := []byte{0x01, 0x00}
	d := sha256.Of(garbage)
	if err := backend.Put(ctx, d, bytes.NewReader(garbage)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Type(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Type(damaged header) = %v, want ErrCorrupt", err)
	}
}

// TestStoreVersionDoesNotAllocateThePayload states the version peek's cost in
// bytes allocated: reading the frame's leading byte must not grow with the
// object, exactly as Store.Type's header read does not.
func TestStoreVersionDoesNotAllocateThePayload(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, backmem.New())
	d, err := s.Put(ctx, test.Note{Title: "large", Body: strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := s.Version(ctx, d); err != nil {
		t.Fatalf("Version = %v", err)
	}
	runtime.ReadMemStats(&after)

	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<16 {
		t.Fatalf("Version allocated %d bytes, want a one-byte cost (the payload is %d bytes)", grew, 1<<20)
	}
}

// TestStoreVersionReadsExactlyOneByte pins the same property in bytes read: the
// version peek stops after the leading byte, so a store can choose a header
// layout before paying for the header.
func TestStoreVersionReadsExactlyOneByte(t *testing.T) {
	ctx := context.Background()
	counted := &countingBackend{Backend: backmem.New()}
	s := newTestStore(t, counted)

	d, err := s.Put(ctx, test.Note{Title: "large", Body: strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	afterPut := counted.read

	version, err := s.Version(ctx, d)
	if err != nil {
		t.Fatalf("Version = %v", err)
	}
	if version != cas.EnvelopeVersion {
		t.Fatalf("Version = %d, want %d", version, cas.EnvelopeVersion)
	}
	if peeked := counted.read - afterPut; peeked != 1 {
		t.Fatalf("Version read %d bytes, want exactly one (the payload is %d bytes)", peeked, 1<<20)
	}

	// The counter is live: a Get over the same backend does read the payload.
	if _, err := s.Get(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := counted.read - afterPut - 1; got < 1<<20 {
		t.Fatalf("Get read %d bytes, want the whole payload", got)
	}
}

// TestStoreVersionReportsTheStoredVersion shows the peek is honest about what is
// on disk: it reports the stored byte, including a version this build cannot
// read, and leaves the rejection to Get.
func TestStoreVersionReportsTheStoredVersion(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	s := newTestStore(t, backend)

	// A v1 object stays readable, and its frame says so.
	v1 := readV1Fixture(t)
	v1Digest := test.DigestData(v1)
	if err := backend.Put(ctx, v1Digest, bytes.NewReader(v1)); err != nil {
		t.Fatal(err)
	}
	if version, err := s.Version(ctx, v1Digest); err != nil || version != 1 {
		t.Fatalf("Version(v1 object) = %d, %v, want 1", version, err)
	}

	// A frame written by a newer format is reported as itself, not as damage.
	future := append([]byte{0xff}, v1[1:]...)
	futureDigest := test.DigestData(future)
	if err := backend.Put(ctx, futureDigest, bytes.NewReader(future)); err != nil {
		t.Fatal(err)
	}
	version, err := s.Version(ctx, futureDigest)
	if err != nil {
		t.Fatalf("Version(unknown version) = %v, want the byte reported verbatim", err)
	}
	if version != 0xff {
		t.Fatalf("Version(unknown version) = %d, want 0xff", version)
	}
	// Version reports the unfamiliar byte as data, and Get — which has to parse
	// the frame to answer — reports the header it cannot read as damage. The
	// "newer format or damaged?" question is answered by the Version byte, not
	// by string-matching Get's error.
	if _, err := s.Get(ctx, futureDigest); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(unknown version) = %v, want ErrCorrupt", err)
	}
}

// TestStoreVersionRejectsMissingAndCorruptKeys covers the version peek's failure
// modes: an absent object is ErrNotFound, an absent digest is rejected before
// any read, and a frame with no version byte at all is ErrCorrupt.
func TestStoreVersionRejectsMissingAndCorruptKeys(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	s := newTestStore(t, backend)

	if _, err := s.Version(ctx, sha256.Of([]byte("absent"))); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Version(missing) = %v, want ErrNotFound", err)
	}
	if _, err := s.Version(ctx, cas.Digest{}); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Version(absent digest) = %v, want ErrInvalidDigest", err)
	}

	// An empty object: there is no version byte to report.
	empty := sha256.Of(nil)
	if err := backend.Put(ctx, empty, bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Version(ctx, empty); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Version(empty object) = %v, want ErrCorrupt", err)
	}
}

// taglessJSONCodec is a Codec[T] that produces exactly the JSON wire format but
// declares no identity: it does not implement cas.CodecNamer, so a store built
// on it writes an empty codec tag.
type taglessJSONCodec[T any] struct{}

// Encode marshals v with encoding/json, exactly like cas/codec/json.
func (taglessJSONCodec[T]) Encode(v T) ([]byte, error) { return json.Marshal(v) }

// Decode unmarshals data with encoding/json.
func (taglessJSONCodec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}

// v1EnvelopeBytes hand-builds a version 1 envelope, the layout that predates
// the codec identity field:
//
//	[version u8 = 1][uvarint typeLen][type][uvarint payloadLen][payload]
//
// It is the documented recipe for testdata/envelope-v1.bin: the fixture is
// checked in rather than generated at test time, so a rewrite of the reader
// cannot silently rewrite the bytes it is supposed to keep readable.
func v1EnvelopeBytes(typ string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(1) // envelope version 1: no codec field exists
	var lenBuf [binary.MaxVarintLen64]byte
	buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], uint64(len(typ)))])
	buf.WriteString(typ)
	buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], uint64(len(payload)))])
	buf.Write(payload)
	return buf.Bytes()
}

// v1FixturePayload is the JSON body stored in testdata/envelope-v1.bin.
const v1FixturePayload = `{"title":"v1 fixture","body":"written before codec identity"}`

// readV1Fixture loads the checked-in version 1 envelope and checks it against
// the documented recipe, so the fixture cannot drift into something the test's
// own builder would not produce.
func readV1Fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/envelope-v1.bin")
	if err != nil {
		t.Fatalf("read testdata/envelope-v1.bin: %v", err)
	}
	if want := v1EnvelopeBytes("note@1", []byte(v1FixturePayload)); !bytes.Equal(data, want) {
		t.Fatalf("fixture = %q, want the documented recipe %q", data, want)
	}
	return data
}

// TestStoreReadsV1Fixture is the backwards-compatibility evidence: an object
// written before the codec identity existed (version 1, no codec field) still
// loads and round-trips through the current reader, and reports no mismatch —
// an absent tag means "unspecified", never a difference.
func TestStoreReadsV1Fixture(t *testing.T) {
	ctx := context.Background()
	data := readV1Fixture(t)

	backend := backmem.New()
	h := test.DigestData(data)
	if err := backend.Put(ctx, h, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	s := cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
	got, err := s.Get(ctx, h)
	if err != nil {
		t.Fatalf("Get(v1 fixture) = %v, want nil (no codec mismatch for an untagged object)", err)
	}
	if got.Title != "v1 fixture" || got.Body != "written before codec identity" {
		t.Fatalf("Get(v1 fixture) = %+v", got)
	}

	// The tag is read back empty, which is what "no check" is based on.
	raw, err := s.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	env, err := cas.EnvelopeFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.Codec != "" {
		t.Fatalf("v1 fixture codec = %q, want the empty (unspecified) tag", env.Codec)
	}
}

// TestStoreV1ObjectThroughADifferentCodecIsCorrupt pins the honest gap the
// design names: a v1 object carries no codec identity, so a reader with another
// codec cannot prove a mismatch — the bytes are indistinguishable from damage.
// ErrCorrupt stays the answer, and never ErrCodecMismatch, but the message
// names the absent identity so the diagnosis is one step from the sentinel.
func TestStoreV1ObjectThroughADifferentCodecIsCorrupt(t *testing.T) {
	ctx := context.Background()
	data := readV1Fixture(t)

	backend := backmem.New()
	h := test.DigestData(data)
	if err := backend.Put(ctx, h, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	s := cas.New(backend, gzipcodec.New(jsoncodec.New[test.Note]()), sha256.New())
	_, err := s.Get(ctx, h)
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(v1 fixture through gzip) = %v, want ErrCorrupt", err)
	}
	if errors.Is(err, cas.ErrCodecMismatch) {
		t.Fatalf("Get(v1 fixture through gzip) = %v, must not claim a codec mismatch", err)
	}
	if !strings.Contains(err.Error(), "no codec identity") {
		t.Fatalf("error %q does not name the absent codec identity", err)
	}
}

// TestStoreReportsCodecMismatch covers the acceptance criterion in both
// directions: an object written with one codec and read through a store built
// on another reports ErrCodecMismatch — never ErrCorrupt (the bytes are intact)
// and never ErrUnknownType (the type is known). Both objects carry the same
// versioned type name, so a codec change needs no major bump.
func TestStoreReportsCodecMismatch(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	jsonStore := cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
	gzipStore := cas.New(backend, gzipcodec.New(jsoncodec.New[test.Note]()), sha256.New())

	fromJSON, err := jsonStore.Put(ctx, test.Note{Title: "json", Body: "written as json"})
	if err != nil {
		t.Fatal(err)
	}
	fromGzip, err := gzipStore.Put(ctx, test.Note{Title: "gzip", Body: "written as gzip"})
	if err != nil {
		t.Fatal(err)
	}

	// Both objects are note@1: the codec tag is what differs, not the major.
	for _, d := range []cas.Digest{fromJSON, fromGzip} {
		raw, err := jsonStore.GetRaw(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		env, err := cas.EnvelopeFromBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
		if env.Type != "note@1" {
			t.Fatalf("stored type = %q, want note@1 (a codec change must not need a major bump)", env.Type)
		}
	}

	for _, tc := range []struct {
		name    string
		store   *cas.Store[test.Note]
		digest  cas.Digest
		wantTag string
	}{
		{"json-written read through a gzip store", gzipStore, fromJSON, "json"},
		{"gzip-written read through a json store", jsonStore, fromGzip, "gzip+json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.store.Get(ctx, tc.digest)
			if !errors.Is(err, cas.ErrCodecMismatch) {
				t.Fatalf("Get = %v, want ErrCodecMismatch", err)
			}
			if errors.Is(err, cas.ErrCorrupt) {
				t.Fatalf("Get = %v, must not report ErrCorrupt for a codec change", err)
			}
			if errors.Is(err, cas.ErrUnknownType) {
				t.Fatalf("Get = %v, must not report ErrUnknownType for a codec change", err)
			}
			if !strings.Contains(err.Error(), tc.wantTag) {
				t.Fatalf("error %q does not name the stored codec %q", err, tc.wantTag)
			}
		})
	}
}

// TestStoreForeignTypeStaysUnknownType keeps the sentinels distinct: an object
// of another type, read by a store using the same codec, is ErrUnknownType and
// never ErrCodecMismatch. The codec check must not fire for a type difference.
func TestStoreForeignTypeStaysUnknownType(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	notes := newTestStore(t, backend)
	d, err := notes.Put(ctx, test.Note{Title: "n"})
	if err != nil {
		t.Fatal(err)
	}

	nodes := cas.New(backend, jsoncodec.New[test.Node](), sha256.New())
	_, err = nodes.Get(ctx, d)
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Get(foreign type) = %v, want ErrUnknownType", err)
	}
	if errors.Is(err, cas.ErrCodecMismatch) {
		t.Fatalf("Get(foreign type) = %v, must not report a codec mismatch", err)
	}
}

// TestStoreCodecTags pins the identity tags: a stack composes its inner codec's
// tag, and a codec that does not implement cas.CodecNamer writes no tag at all
// — and then reads any tag without complaint, in both directions.
func TestStoreCodecTags(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()

	// A stack reports "<outer>+<inner>", both through the interface and in the
	// envelope it writes.
	gzipCodec := gzipcodec.New(jsoncodec.New[test.Note]())
	var namer cas.CodecNamer = gzipCodec
	if got := namer.CodecName(); got != "gzip+json" {
		t.Fatalf("gzip.New(json.New[T]()).CodecName() = %q, want gzip+json", got)
	}
	gzipStore := cas.New(backend, gzipCodec, sha256.New())
	gzipDigest, err := gzipStore.Put(ctx, test.Note{Title: "tagged"})
	if err != nil {
		t.Fatal(err)
	}
	assertStoredCodec(t, ctx, gzipStore, gzipDigest, "gzip+json")

	// A codec without CodecNamer declares no tag: the envelope's codec field is
	// empty ("unspecified").
	taglessStore := cas.New(backend, taglessJSONCodec[test.Note]{}, sha256.New())
	taglessDigest, err := taglessStore.Put(ctx, test.Note{Title: "tagless"})
	if err != nil {
		t.Fatal(err)
	}
	assertStoredCodec(t, ctx, taglessStore, taglessDigest, "")

	// It reads a tagged object without complaint...
	jsonStore := cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
	jsonDigest, err := jsonStore.Put(ctx, test.Note{Title: "json"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taglessStore.Get(ctx, jsonDigest); err != nil {
		t.Fatalf("tagless codec reading a tagged object = %v, want nil", err)
	}
	// ...and a tagged codec reads a tagless object without complaint, because
	// the comparison applies only when both sides declare a tag.
	if _, err := jsonStore.Get(ctx, taglessDigest); err != nil {
		t.Fatalf("tagged codec reading a tagless object = %v, want nil", err)
	}
}

// assertStoredCodec checks the codec identity tag recorded in the stored
// envelope at d.
func assertStoredCodec[T cas.Object[T]](t *testing.T, ctx context.Context, s *cas.Store[T], d cas.Digest, want string) {
	t.Helper()
	raw, err := s.GetRaw(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	env, err := cas.EnvelopeFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.Codec != want {
		t.Fatalf("stored codec = %q, want %q", env.Codec, want)
	}
}

// --- Store guards and delegated failures ----------------------------------
//
// Every write and read path funnels through the same two pieces: encoded's
// hash-then-key-check, and a delegation to the Backend. The fakes below make
// each of those fail deterministically, so the store's own error arms are
// asserted rather than assumed.

// rejectingHasher returns a well-formed digest from Digest and then rejects it
// in Validate. CheckDigest accepts the value, so this is the only way to reach
// the arm where the client's algorithm — not the store — refuses a key.
type rejectingHasher struct{ d cas.Digest }

func (h rejectingHasher) Digest(io.Reader) (cas.Digest, error) { return h.d, nil }
func (h rejectingHasher) Validate(cas.Digest) error            { return errors.New("rejected by algorithm") }

// putErrorBackend fails every write and reports every probe as "absent", so a
// store can fail at the backend after a successful encode. It embeds the
// Backend interface rather than a concrete backend, so it deliberately does NOT
// satisfy io.Closer even when the value it wraps does.
type putErrorBackend struct {
	cas.Backend
	err error
}

func (b putErrorBackend) Put(context.Context, cas.Digest, io.Reader) error { return b.err }
func (b putErrorBackend) Exists(context.Context, cas.Digest) (bool, error) { return false, nil }

// existsErrorBackend fails the existence probe PutDedup uses to decide whether
// the content is already stored.
type existsErrorBackend struct {
	cas.Backend
	err error
}

func (b existsErrorBackend) Exists(context.Context, cas.Digest) (bool, error) { return false, b.err }

// rawBackend hands out one caller-supplied reader for every digest, so a store
// can be given a chosen frame, a failing read or a failing close.
type rawBackend struct {
	cas.Backend
	reader io.ReadCloser
}

func (b rawBackend) Get(context.Context, cas.Digest) (io.ReadCloser, error) { return b.reader, nil }

// readErrorReader fails every Read; Close succeeds, so the read arm is the one
// the store reports.
type readErrorReader struct{ err error }

func (r readErrorReader) Read([]byte) (int, error) { return 0, r.err }
func (r readErrorReader) Close() error             { return nil }

// rawStore builds a store whose reads are served by reader.
func rawStore(t *testing.T, reader io.ReadCloser) *cas.Store[test.Note] {
	t.Helper()
	return cas.New(rawBackend{Backend: backmem.New(), reader: reader}, jsoncodec.New[test.Note](), sha256.New())
}

// TestStoreCloseWithoutCloserBackend pins Close's no-op arm: the minimal
// Backend contract has no Close, so a store over it needs no cleanup and
// defer store.Close() is safe.
func TestStoreCloseWithoutCloserBackend(t *testing.T) {
	s := cas.New(backmem.New(), jsoncodec.New[test.Note](), sha256.New())
	if err := s.Close(); err != nil {
		t.Fatalf("Close() over a backend without Close = %v, want nil", err)
	}
}

// TestStoreCloseNilReceiver pins the other no-op arm: a nil *Store owns nothing,
// so Close reports success instead of panicking.
func TestStoreCloseNilReceiver(t *testing.T) {
	var s *cas.Store[test.Note]
	if err := s.Close(); err != nil {
		t.Fatalf("(*Store[test.Note])(nil).Close() = %v, want nil", err)
	}
}

// TestStoreRejectsDigestRefusedByHasher walks the shared key guard: Put hashes
// the frame and then fails the check, and GetRaw/Exists/Delete apply the same
// guard before touching the backend.
func TestStoreRejectsDigestRefusedByHasher(t *testing.T) {
	ctx := context.Background()
	valid := sha256.Of([]byte("seed"))
	s := cas.New(backmem.New(), jsoncodec.New[test.Note](), rejectingHasher{d: valid})

	// The guard names the operation it was applied for and wraps the client
	// algorithm's own refusal, so both halves are asserted.
	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"Put", func() error { _, err := s.Put(ctx, test.Note{Title: "x"}); return err }, "store: put"},
		{"GetRaw", func() error { _, err := s.GetRaw(ctx, valid); return err }, "store: get"},
		{"Exists", func() error { _, err := s.Exists(ctx, valid); return err }, "store: exists"},
		{"Delete", func() error { return s.Delete(ctx, valid) }, "store: delete"},
	} {
		err := tc.call()
		if err == nil {
			t.Fatalf("%s(digest the hasher refuses) = nil, want an error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "rejected by algorithm") {
			t.Fatalf("%s(refused digest) = %v, want the %q guard wrapping the hasher's refusal", tc.name, err, tc.want)
		}
	}
}

// TestStorePutReportsHasherDigestFailure pins the arm where the client's
// algorithm cannot hash the frame at all.
func TestStorePutReportsHasherDigestFailure(t *testing.T) {
	s := cas.New(backmem.New(), jsoncodec.New[test.Note](), digestErrorHasher{})
	if _, err := s.Put(context.Background(), test.Note{Title: "x"}); err == nil || !strings.Contains(err.Error(), "digest failed") {
		t.Fatalf("Put(digest error) = %v, want the hasher's failure", err)
	}
}

// TestStorePutReportsBackendFailure pins Put's write delegation.
func TestStorePutReportsBackendFailure(t *testing.T) {
	want := errors.New("put failed")
	s := cas.New(putErrorBackend{Backend: backmem.New(), err: want}, jsoncodec.New[test.Note](), sha256.New())
	if _, err := s.Put(context.Background(), test.Note{Title: "x"}); !errors.Is(err, want) {
		t.Fatalf("Put(backend error) = %v, want %v", err, want)
	}
}

// TestStorePutDedupReportsProbeFailure pins the existence probe PutDedup makes
// before it decides to write.
func TestStorePutDedupReportsProbeFailure(t *testing.T) {
	want := errors.New("exists failed")
	s := cas.New(existsErrorBackend{Backend: backmem.New(), err: want}, jsoncodec.New[test.Note](), sha256.New())
	if _, _, err := s.PutDedup(context.Background(), test.Note{Title: "x"}); !errors.Is(err, want) {
		t.Fatalf("PutDedup(Exists error) = %v, want %v", err, want)
	}
}

// TestStorePutDedupReportsWriteFailure pins the write PutDedup performs once the
// probe reports the object is absent.
func TestStorePutDedupReportsWriteFailure(t *testing.T) {
	want := errors.New("put failed")
	s := cas.New(putErrorBackend{Backend: backmem.New(), err: want}, jsoncodec.New[test.Note](), sha256.New())
	if _, _, err := s.PutDedup(context.Background(), test.Note{Title: "x"}); !errors.Is(err, want) {
		t.Fatalf("PutDedup(Put error) = %v, want %v", err, want)
	}
}

// TestStoreGetReportsPayloadDecodeFailure pins the ErrCorrupt arm Get takes when
// a frame names the store's own codec — so the mismatch check passes — and the
// payload then fails to decode. It must not be reported as a codec mismatch.
func TestStoreGetReportsPayloadDecodeFailure(t *testing.T) {
	ctx := context.Background()
	raw, err := cas.EncodeEnvelope("json", "note@1", []byte("{"))
	if err != nil {
		t.Fatal(err)
	}
	s := rawStore(t, io.NopCloser(bytes.NewReader(raw)))
	_, err = s.Get(ctx, sha256.Of(raw))
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(bad payload) = %v, want ErrCorrupt", err)
	}
	if errors.Is(err, cas.ErrCodecMismatch) {
		t.Fatal("Get(bad payload) reported ErrCodecMismatch although the frame names this store's own codec")
	}
}

// TestStoreGetRawReportsReadFailure pins the arm that closes the reader and
// reports the read failure rather than a second close error.
func TestStoreGetRawReportsReadFailure(t *testing.T) {
	want := errors.New("read failed")
	s := rawStore(t, readErrorReader{err: want})
	if _, err := s.GetRaw(context.Background(), sha256.Of([]byte("x"))); !errors.Is(err, want) {
		t.Fatalf("GetRaw(read error) = %v, want %v", err, want)
	}
}

// TestStoreTypeReportsCloseFailure pins Type's close delegation: the header
// parsed, so a failing Close is the only error left to report.
func TestStoreTypeReportsCloseFailure(t *testing.T) {
	raw, err := cas.EncodeEnvelope("json", "note@1", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("close failed")
	s := rawStore(t, failingCloseReader{Reader: bytes.NewReader(raw), err: want})
	if _, err := s.Type(context.Background(), sha256.Of(raw)); !errors.Is(err, want) {
		t.Fatalf("Type(close error) = %v, want %v", err, want)
	}
}

// TestStoreVersionReportsCloseFailure pins Version's close delegation, the
// one-byte counterpart of Type's.
func TestStoreVersionReportsCloseFailure(t *testing.T) {
	raw, err := cas.EncodeEnvelope("json", "note@1", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("close failed")
	s := rawStore(t, failingCloseReader{Reader: bytes.NewReader(raw), err: want})
	if _, err := s.Version(context.Background(), sha256.Of(raw)); !errors.Is(err, want) {
		t.Fatalf("Version(close error) = %v, want %v", err, want)
	}
}
