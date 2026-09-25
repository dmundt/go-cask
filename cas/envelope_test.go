package cas

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

// v1Envelope hand-builds a version 1 envelope — the layout that predates the
// codec identity field:
//
//	[version u8 = 1][uvarint typeLen][type][uvarint payloadLen][payload]
//
// The reader tolerates these bytes forever (a version 1 object has no codec
// identity and is never rewritten), so they are the back-compat evidence for
// the envelope parser.
func v1Envelope(typ string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersionV1)
	var lenBuf [binary.MaxVarintLen64]byte
	buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], uint64(len(typ)))])
	buf.WriteString(typ)
	buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], uint64(len(payload)))])
	buf.Write(payload)
	return buf.Bytes()
}

func TestEnvelopeRoundTrip(t *testing.T) {
	payload := []byte("hello world")
	typ := "note@1"
	data := encodeEnvelope("json", typ, payload)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != typ {
		t.Fatalf("type = %q, want %q", out.Type, typ)
	}
	if out.Codec != "json" {
		t.Fatalf("codec = %q, want json", out.Codec)
	}
	if string(out.Data) != string(payload) {
		t.Fatalf("data = %q, want %q", string(out.Data), string(payload))
	}
}

// TestEnvelopeCodecUnspecified pins the legal empty codec tag: a codec that
// declares no identity writes an empty field, which means "unspecified" rather
// than a format of its own.
func TestEnvelopeCodecUnspecified(t *testing.T) {
	out, err := EnvelopeFromBytes(encodeEnvelope("", "blob@1", []byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Codec != "" {
		t.Fatalf("codec = %q, want the empty (unspecified) tag", out.Codec)
	}
	if out.Type != "blob@1" {
		t.Fatalf("type = %q, want blob@1", out.Type)
	}
}

// TestEnvelopeVersion1ReadsNoCodec pins the version 1 branch: no codec field
// exists, so the tag reads back empty and the type follows the version byte.
func TestEnvelopeVersion1ReadsNoCodec(t *testing.T) {
	out, err := EnvelopeFromBytes(v1Envelope("note@1", []byte(`{"title":"v1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "note@1" {
		t.Fatalf("type = %q, want note@1", out.Type)
	}
	if out.Codec != "" {
		t.Fatalf("codec = %q, want the empty (unspecified) tag for a v1 envelope", out.Codec)
	}
	if string(out.Data) != `{"title":"v1"}` {
		t.Fatalf("data = %q", out.Data)
	}
}

func TestEnvelopeLegacyUnversioned(t *testing.T) {
	payload := []byte("{}")
	// Legacy form: type without @major — decodeEnvelope appends @1.
	data := encodeEnvelope("json", "mystery", payload)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "mystery@1" {
		t.Fatalf("legacy type = %q, want mystery@1", out.Type)
	}
}

func TestEnvelopeEmptyPayload(t *testing.T) {
	data := encodeEnvelope("json", "blob@1", nil)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "blob@1" || len(out.Data) != 0 {
		t.Fatalf("empty payload: type=%q data=%v", out.Type, out.Data)
	}
}

// TestEnvelopeUnknownTypeIsNotAParseFailure pins the line between parsing and
// dispatch: the envelope readers report a structural failure as ErrCorrupt,
// while a well-formed frame naming a type nothing has a decoder for parses
// without error and simply hands the name back. Deciding what to do with that
// name is the caller's job (cas/repo.UnknownTypeError, gitlike's fixed model),
// not the parser's.
func TestEnvelopeUnknownTypeIsNotAParseFailure(t *testing.T) {
	data := encodeEnvelope("json", "nothing-registered@7", []byte("{}"))

	env, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatalf("EnvelopeFromBytes(unregistered type) = %v, want no error", err)
	}
	if env.Type != "nothing-registered@7" {
		t.Fatalf("type = %q, want nothing-registered@7", env.Type)
	}
	typ, err := EnvelopeType(data)
	if err != nil {
		t.Fatalf("EnvelopeType(unregistered type) = %v, want no error", err)
	}
	if typ != "nothing-registered@7" {
		t.Fatalf("EnvelopeType = %q, want nothing-registered@7", typ)
	}
}

// TestEnvelopeTypeReportsCorruptPrefix pins EnvelopeType's error arm: the
// header-only reader gives the same ErrCorrupt verdict as the full reader for
// bytes that do not begin with a usable header, and reports no type alongside
// it. A caller that peeks a type must never be told a damaged frame is fine.
func TestEnvelopeTypeReportsCorruptPrefix(t *testing.T) {
	full := encodeEnvelope("json", "blob@1", []byte("{}"))
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"version byte only", []byte{EnvelopeVersion}},
		{"truncated before the codec field", full[:2]},
		{"truncated inside the type name", full[:5]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, err := EnvelopeType(tc.data)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("EnvelopeType() = (%q, %v), want ErrCorrupt", typ, err)
			}
			if typ != "" {
				t.Fatalf("EnvelopeType() = %q alongside the error, want the empty string", typ)
			}
			if _, err := EnvelopeFromBytes(tc.data); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("EnvelopeFromBytes() = %v, want the same ErrCorrupt verdict", err)
			}
		})
	}
}

func TestEnvelopeTruncatedVersion(t *testing.T) {
	if _, err := EnvelopeFromBytes(nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("nil data = %v, want ErrCorrupt", err)
	}
}

// stubBackend serves one object's bytes and nothing else, so HeaderType's own
// read paths (a failing reader, a failing Close) can be exercised without a
// backend that can produce them.
type stubBackend struct {
	data    []byte
	getErr  error
	rc      func() io.ReadCloser
	missing bool
}

func (b stubBackend) Get(context.Context, Digest) (io.ReadCloser, error) {
	if b.getErr != nil {
		return nil, b.getErr
	}
	if b.missing {
		return nil, ErrNotFound
	}
	if b.rc != nil {
		return b.rc(), nil
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}

func (stubBackend) Put(context.Context, Digest, io.Reader) error { return nil }
func (stubBackend) Exists(context.Context, Digest) (bool, error) { return false, nil }
func (stubBackend) Delete(context.Context, Digest) error         { return nil }
func (stubBackend) List(context.Context) ([]Digest, error)       { return nil, nil }
func (stubBackend) Stats(context.Context) (*Stats, error)        { return &Stats{}, nil }

// erroringReadCloser fails the read or the close on demand.
type erroringReadCloser struct {
	failRead  bool
	failClose bool
}

func (e erroringReadCloser) Read(p []byte) (int, error) {
	if e.failRead {
		return 0, errors.New("read exploded")
	}
	return bytes.NewReader(nil).Read(p)
}

func (e erroringReadCloser) Close() error {
	if e.failClose {
		return errors.New("close exploded")
	}
	return nil
}

// TestHeaderTypeReadsOnlyTheHeader pins the contract every consumer of
// cas.HeaderType now shares (cas/repo.Registry.Resolve, the gitlike resolver,
// internal/index.HeaderType, the CLI and the viewer): it returns the versioned
// type name from a bounded prefix, it never needs the payload — so a prefix is
// enough and a huge object is not buffered — and it reports an unreadable
// object, a missing one and damaged bytes as three distinguishable answers.
func TestHeaderTypeReadsOnlyTheHeader(t *testing.T) {
	ctx := context.Background()

	data := encodeEnvelope("json", "blob@1", bytes.Repeat([]byte("x"), 1<<20))
	typ, err := HeaderType(ctx, stubBackend{data: data}, Digest{})
	if err != nil {
		t.Fatalf("HeaderType(valid) = %v", err)
	}
	if typ != "blob@1" {
		t.Fatalf("HeaderType = %q, want blob@1", typ)
	}

	// Bytes that do not begin with a usable header are damage, not an absent
	// type: "" with no error is internal/index's best-effort reading, not this.
	if _, err := HeaderType(ctx, stubBackend{data: []byte("not an envelope")}, Digest{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("HeaderType(raw bytes) = %v, want ErrCorrupt", err)
	}
	// A truncated prefix is the same answer: the declared type length is not
	// satisfied by the bytes that arrived.
	if _, err := HeaderType(ctx, stubBackend{data: []byte{envelopeVersion, 0x01, 'a', 0xc8, 'a'}}, Digest{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("HeaderType(truncated) = %v, want ErrCorrupt", err)
	}
	if _, err := HeaderType(ctx, stubBackend{missing: true}, Digest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("HeaderType(missing) = %v, want ErrNotFound", err)
	}
	if _, err := HeaderType(ctx, stubBackend{getErr: errors.New("boom")}, Digest{}); err == nil {
		t.Fatal("HeaderType(Get failure) = nil, want an error")
	}
	// The messages are load-bearing: cas/repo's and gitlike's tests pin them,
	// so the read that moved into the core must keep naming the failure.
	_, err = HeaderType(ctx, stubBackend{rc: func() io.ReadCloser {
		return erroringReadCloser{failRead: true}
	}}, Digest{})
	if err == nil || !strings.Contains(err.Error(), "read object header") {
		t.Fatalf("HeaderType(failing read) = %v, want it to name the header read", err)
	}
	_, err = HeaderType(ctx, stubBackend{rc: func() io.ReadCloser {
		return erroringReadCloser{failClose: true}
	}}, Digest{})
	if err == nil || !strings.Contains(err.Error(), "close object header reader") {
		t.Fatalf("HeaderType(failing close) = %v, want it to name the close", err)
	}
}

func TestEnvelopeUnknownVersion(t *testing.T) {
	for _, version := range []byte{0x00, 0x03, 0xff} {
		// Version, codecLen 1, codec "a": only 1 and 2 are readable formats.
		data := []byte{version, 0x01, 0x61}
		if _, err := EnvelopeFromBytes(data); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("version %d = %v, want ErrCorrupt", version, err)
		}
	}
}

func TestEnvelopeTruncatedCodecLen(t *testing.T) {
	data := []byte{envelopeVersion} // version only, no codecLen
	_, err := EnvelopeFromBytes(data)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated codecLen = %v, want ErrCorrupt", err)
	}
	if !strings.Contains(err.Error(), "codec length") {
		t.Fatalf("error %q does not name the codec length field", err)
	}
}

func TestEnvelopeOversizedCodec(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersion)
	var lenBuf [binary.MaxVarintLen64]byte
	// codecLen = 100 but no codec bytes follow.
	buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], 100)])
	_, err := EnvelopeFromBytes(buf.Bytes())
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversized codec = %v, want ErrCorrupt", err)
	}
	if !strings.Contains(err.Error(), "codec") {
		t.Fatalf("error %q does not name the codec field", err)
	}
}

func TestEnvelopeTruncatedTypeLen(t *testing.T) {
	data := []byte{envelopeVersion, 0x00} // version, empty codec, no typeLen
	if _, err := EnvelopeFromBytes(data); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated typeLen = %v, want ErrCorrupt", err)
	}
}

func TestEnvelopeTruncatedType(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersion)
	buf.WriteByte(0x00) // empty codec
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], 100)
	buf.Write(lenBuf[:n])
	// typeLen = 100 but no type bytes follow
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated type = %v, want ErrCorrupt", err)
	}
}

func TestEnvelopeEmptyType(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersion)
	buf.WriteByte(0x00) // empty codec
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], 0)
	buf.Write(lenBuf[:n])
	// typeLen = 0 → empty type name
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("empty type = %v, want ErrCorrupt", err)
	}
}

// TestEnvelopeVersion1TruncatedHeader pins the v1 branch's own failure paths:
// a version 1 envelope has no codec field, so the type length follows the
// version byte directly. They are ErrCorrupt like every other structural
// failure, so the byte-slice reader and the streaming peek agree.
func TestEnvelopeVersion1TruncatedHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"truncated type length", []byte{envelopeVersionV1}},
		{"empty type", []byte{envelopeVersionV1, 0x00}},
		{"truncated payload length", v1Envelope("note@1", nil)[:8]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EnvelopeFromBytes(tc.data); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("EnvelopeFromBytes(%v) = %v, want ErrCorrupt", tc.data, err)
			}
		})
	}
}

// TestEnvelopeVersionAppliedToLegacy pins the legacy path end to end: a type
// name without "@major" reads back with "@1" appended.
func TestEnvelopeVersionAppliedToLegacy(t *testing.T) {
	env, err := EnvelopeFromBytes(encodeEnvelope("", "blob", []byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "blob@1" {
		t.Fatalf("unversioned type name = %q, want blob@1", env.Type)
	}
	env, err = EnvelopeFromBytes(encodeEnvelope("", "blob@2", []byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "blob@2" {
		t.Fatalf("versioned type name = %q, want blob@2", env.Type)
	}
}

func TestEnvelopeMarshalDeterministic(t *testing.T) {
	a := encodeEnvelope("json", "t", []byte("x"))
	b := encodeEnvelope("json", "t", []byte("x"))
	if !bytes.Equal(a, b) {
		t.Fatalf("deterministic marshal: %x != %x", a, b)
	}
}

// TestEncodeEnvelopeExported pins the writer's contract: the frame it produces
// carries the version this build writes, reads back through EnvelopeFromBytes
// and the peekers, treats an empty codec tag as "unspecified", and enforces the
// versioned type-name rule Store.Put applies (go-cask#187).
func TestEncodeEnvelopeExported(t *testing.T) {
	frame, err := EncodeEnvelope("json", "note@1", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != EnvelopeVersion {
		t.Fatalf("leading byte = %d, want EnvelopeVersion %d", frame[0], EnvelopeVersion)
	}
	env, err := EnvelopeFromBytes(frame)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "note@1" || env.Codec != "json" || string(env.Data) != "payload" {
		t.Fatalf("EnvelopeFromBytes = {type %q, codec %q, data %q}", env.Type, env.Codec, env.Data)
	}
	if version, err := PeekVersion(bytes.NewReader(frame)); err != nil || version != EnvelopeVersion {
		t.Fatalf("PeekVersion = (%d, %v), want %d", version, err, EnvelopeVersion)
	}
	if typ, err := PeekType(bytes.NewReader(frame)); err != nil || typ != "note@1" {
		t.Fatalf("PeekType = (%q, %v), want note@1", typ, err)
	}

	// An empty tag is legal and means "codec unspecified", not an error.
	untagged, err := EncodeEnvelope("", "note@1", nil)
	if err != nil {
		t.Fatal(err)
	}
	env, err = EnvelopeFromBytes(untagged)
	if err != nil {
		t.Fatal(err)
	}
	if env.Codec != "" || len(env.Data) != 0 {
		t.Fatalf("untagged frame = {codec %q, data %q}, want an empty tag and payload", env.Codec, env.Data)
	}

	for _, typ := range []string{"", "note"} {
		if _, err := EncodeEnvelope("json", typ, nil); !errors.Is(err, ErrUnknownType) {
			t.Fatalf("EncodeEnvelope(type %q) = %v, want ErrUnknownType", typ, err)
		}
	}
}

func TestEnvelopeFromBytesExported(t *testing.T) {
	raw := encodeEnvelope("cbor", "exported@1", []byte("data"))
	env, err := EnvelopeFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(env.Type, "exported") {
		t.Fatalf("type = %q", env.Type)
	}
	if env.Codec != "cbor" {
		t.Fatalf("codec = %q, want cbor", env.Codec)
	}
	if string(env.Data) != "data" {
		t.Fatalf("data = %q", env.Data)
	}
}

// TestEnvelopeFormatWithPayloadLen pins the byte layout: the codec identity tag
// precedes the type name, and a payload-length field precedes the payload, so
// the frame is self-delimiting.
func TestEnvelopeFormatWithPayloadLen(t *testing.T) {
	codec := "gzip+json"
	typ := "note@1"
	payload := []byte("abc")
	data := encodeEnvelope(codec, typ, payload)
	r := bytes.NewReader(data)
	// [version u8]
	if v, _ := r.ReadByte(); v != envelopeVersion {
		t.Fatalf("version = %d", v)
	}
	// [codecLen uvarint][codec]
	codecLen, _ := binary.ReadUvarint(r)
	cb := make([]byte, codecLen)
	r.Read(cb)
	if string(cb) != codec {
		t.Fatalf("codec = %q, want %q", cb, codec)
	}
	// [typeLen uvarint][type]
	typeLen, _ := binary.ReadUvarint(r)
	tb := make([]byte, typeLen)
	r.Read(tb)
	if string(tb) != typ {
		t.Fatalf("type = %q", tb)
	}
	// [payloadLen uvarint][payload]
	payloadLen, _ := binary.ReadUvarint(r)
	if int(payloadLen) != len(payload) {
		t.Fatalf("payloadLen = %d, want %d", payloadLen, len(payload))
	}
	pb := make([]byte, payloadLen)
	r.Read(pb)
	if string(pb) != string(payload) {
		t.Fatalf("payload = %q", pb)
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes after framed payload: %d", r.Len())
	}
}

// TestEnvelopeToleratesTrailingBytes keeps the forward-compatibility rule: the
// payload length is authoritative and the writer emits no trailer, so bytes
// appended after the payload are ignored rather than read as payload.
func TestEnvelopeToleratesTrailingBytes(t *testing.T) {
	payload := []byte("abc")
	data := encodeEnvelope("json", "note@1", payload)
	extended := append(slices.Clone(data), []byte("future trailer")...)

	env, err := EnvelopeFromBytes(extended)
	if err != nil {
		t.Fatalf("EnvelopeFromBytes(extended) = %v", err)
	}
	if env.Type != "note@1" || env.Codec != "json" || !bytes.Equal(env.Data, payload) {
		t.Fatalf("extended frame = %+v, want the framed type, codec and payload", env)
	}
}

func TestEnvelopeTruncatedPayloadLen(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersion)
	buf.WriteByte(0x00) // empty codec
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note@1")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note@1")
	// No payloadLen follows -> truncated payload length error.
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated payloadLen = %v, want ErrCorrupt", err)
	}
}

func TestEnvelopePayloadLenExceeds(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersion)
	buf.WriteByte(0x00) // empty codec
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note@1")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note@1")
	// payloadLen = 100 but only a few bytes follow.
	n = binary.PutUvarint(lenBuf[:], 100)
	buf.Write(lenBuf[:n])
	buf.WriteString("xy")
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversized payloadLen = %v, want ErrCorrupt", err)
	}
}

// countingReader counts the bytes read through it. It deliberately does not
// implement io.ByteReader, so PeekType exercises its byte-at-a-time adapter.
type countingReader struct {
	r io.Reader
	n int
}

// Read reads from the underlying reader and adds the count.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// headerSize is the exact length of the
// [version][uvarint codecLen][codec][uvarint typeLen][type] header
// encodeEnvelope writes for codec and typ.
func headerSize(codec, typ string) int {
	var buf [binary.MaxVarintLen64]byte
	return 1 + binary.PutUvarint(buf[:], uint64(len(codec))) + len(codec) +
		binary.PutUvarint(buf[:], uint64(len(typ))) + len(typ)
}

// headerTail is what follows that header — the payload-length field and the
// payload — which a peek deliberately leaves unread.
func headerTail(codec, typ string, payload []byte) []byte {
	return encodeEnvelope(codec, typ, payload)[headerSize(codec, typ):]
}

// TestPeekTypeReadsOnlyTheHeader pins the point of the peek: the bytes consumed
// are the header — including the codec identity tag it steps over — whatever
// the payload size, and the stream is left positioned exactly after it.
func TestPeekTypeReadsOnlyTheHeader(t *testing.T) {
	const typ = "blob@1"
	for _, tc := range []struct {
		name  string
		codec string
	}{
		{"named codec", "gzip+json"},
		{"unspecified codec", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{0, 7, 1 << 20} {
				payload := bytes.Repeat([]byte("x"), size)
				cr := &countingReader{r: bytes.NewReader(encodeEnvelope(tc.codec, typ, payload))}

				got, err := PeekType(cr)
				if err != nil {
					t.Fatalf("payload %d: PeekType = %v", size, err)
				}
				if got != typ {
					t.Fatalf("payload %d: type = %q, want %q", size, got, typ)
				}
				if want := headerSize(tc.codec, typ); cr.n != want {
					t.Fatalf("payload %d: read %d bytes, want exactly the %d-byte header", size, cr.n, want)
				}
				// Nothing was consumed past the header: the rest of the same stream is
				// still the framed tail — the payload-length field and the payload —
				// byte for byte.
				rest, err := io.ReadAll(cr)
				if err != nil {
					t.Fatalf("payload %d: read rest = %v", size, err)
				}
				if want := headerTail(tc.codec, typ, payload); !bytes.Equal(rest, want) {
					t.Fatalf("payload %d: %d bytes left, want the %d-byte framed tail", size, len(rest), len(want))
				}
			}
		})
	}
}

// TestPeekHeaderReadsOnlyTheHeader pins the census read's cost and its one-pass
// shape: version, codec and type come from exactly the header bytes — including
// the codec tag PeekType steps over — whatever the payload size, and the stream
// is left positioned exactly after them.
func TestPeekHeaderReadsOnlyTheHeader(t *testing.T) {
	const typ = "blob@1"
	for _, tc := range []struct {
		name  string
		codec string
	}{
		{"named codec", "gzip+json"},
		{"unspecified codec", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{0, 7, 1 << 20} {
				payload := bytes.Repeat([]byte("x"), size)
				cr := &countingReader{r: bytes.NewReader(encodeEnvelope(tc.codec, typ, payload))}

				version, codec, typeName, err := PeekHeader(cr)
				if err != nil {
					t.Fatalf("payload %d: PeekHeader = %v", size, err)
				}
				if version != EnvelopeVersion {
					t.Fatalf("payload %d: version = %d, want %d", size, version, EnvelopeVersion)
				}
				if codec != tc.codec {
					t.Fatalf("payload %d: codec = %q, want %q", size, codec, tc.codec)
				}
				if typeName != typ {
					t.Fatalf("payload %d: type = %q, want %q", size, typeName, typ)
				}
				if want := headerSize(tc.codec, typ); cr.n != want {
					t.Fatalf("payload %d: read %d bytes, want exactly the %d-byte header", size, cr.n, want)
				}
				rest, err := io.ReadAll(cr)
				if err != nil {
					t.Fatalf("payload %d: read rest = %v", size, err)
				}
				if want := headerTail(tc.codec, typ, payload); !bytes.Equal(rest, want) {
					t.Fatalf("payload %d: %d bytes left, want the %d-byte framed tail", size, len(rest), len(want))
				}
			}
		})
	}
}

// TestPeekHeaderAgreesWithTheSingleFieldPeeks pins that the combined read and
// the two single-field reads resolve one frame identically — they share the walk
// — for every layout this build knows.
func TestPeekHeaderAgreesWithTheSingleFieldPeeks(t *testing.T) {
	for _, frame := range [][]byte{
		encodeEnvelope("cbor", "note@1", []byte("payload")),
		encodeEnvelope("", "note@1", nil),
		v1Envelope("legacy", []byte("payload")),
	} {
		wantVersion, err := PeekVersion(bytes.NewReader(frame))
		if err != nil {
			t.Fatal(err)
		}
		wantType, err := PeekType(bytes.NewReader(frame))
		if err != nil {
			t.Fatal(err)
		}
		version, _, typeName, err := PeekHeader(bytes.NewReader(frame))
		if err != nil {
			t.Fatal(err)
		}
		if version != wantVersion || typeName != wantType {
			t.Fatalf("PeekHeader = (%d, %q), want (%d, %q)", version, typeName, wantVersion, wantType)
		}
	}
}

// TestPeekHeaderVersion1AndUnversioned pins the compatibility reading: a
// version 1 frame reports version 1 and a codec that is explicitly unspecified,
// and a legacy unversioned type name reads back with "@1".
func TestPeekHeaderVersion1AndUnversioned(t *testing.T) {
	version, codec, typeName, err := PeekHeader(bytes.NewReader(v1Envelope("legacy", []byte("payload"))))
	if err != nil {
		t.Fatal(err)
	}
	if version != envelopeVersionV1 {
		t.Fatalf("version = %d, want %d", version, envelopeVersionV1)
	}
	if codec != "" {
		t.Fatalf("codec = %q, want unspecified", codec)
	}
	if typeName != "legacy@1" {
		t.Fatalf("type = %q, want legacy@1", typeName)
	}
}

// TestPeekHeaderRejectsAnUnknownVersion pins the boundary against PeekVersion:
// the census read needs a layout it can walk, so an unknown version is damage to
// it, while PeekVersion reports the byte verbatim for a caller that wants to
// tell "newer format" from "damaged".
func TestPeekHeaderRejectsAnUnknownVersion(t *testing.T) {
	frame := []byte{9, 0, 0}
	if _, _, _, err := PeekHeader(bytes.NewReader(frame)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PeekHeader(version 9) = %v, want ErrCorrupt", err)
	}
	version, err := PeekVersion(bytes.NewReader(frame))
	if err != nil || version != 9 {
		t.Fatalf("PeekVersion(version 9) = (%d, %v), want (9, nil)", version, err)
	}
}

// TestPeekTypeVersion1Stream covers the version 1 stream: no codec field is
// present, so the type follows the version byte directly.
func TestPeekTypeVersion1Stream(t *testing.T) {
	const typ = "note@1"
	payload := []byte("payload")
	cr := &countingReader{r: bytes.NewReader(v1Envelope(typ, payload))}

	got, err := PeekType(cr)
	if err != nil {
		t.Fatalf("PeekType(v1 stream) = %v", err)
	}
	if got != typ {
		t.Fatalf("type = %q, want %q", got, typ)
	}
	// The v1 header is [version][typeLen][type]; there is no codec field.
	if want := 1 + 1 + len(typ); cr.n != want {
		t.Fatalf("read %d bytes, want the %d-byte v1 header", cr.n, want)
	}
}

// TestPeekTypeAcceptsAByteReader covers the other branch: a reader that can read
// single bytes itself is used directly, and still stops after the header.
func TestPeekTypeAcceptsAByteReader(t *testing.T) {
	payload := []byte("payload")
	r := bytes.NewReader(encodeEnvelope("zlib+json", "tree@2", payload)) // *bytes.Reader is an io.ByteReader

	got, err := PeekType(r)
	if err != nil {
		t.Fatalf("PeekType = %v", err)
	}
	if got != "tree@2" {
		t.Fatalf("type = %q, want tree@2", got)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read rest = %v", err)
	}
	if want := headerTail("zlib+json", "tree@2", payload); !bytes.Equal(rest, want) {
		t.Fatalf("%d bytes left, want the %d-byte framed tail", len(rest), len(want))
	}
}

// TestPeekTypeLegacyUnversionedName keeps the "@1" default of the byte-slice
// reader and the streaming one identical.
func TestPeekTypeLegacyUnversionedName(t *testing.T) {
	got, err := PeekType(bytes.NewReader(encodeEnvelope("json", "mystery", []byte("{}"))))
	if err != nil {
		t.Fatalf("PeekType = %v", err)
	}
	if got != "mystery@1" {
		t.Fatalf("legacy type = %q, want mystery@1", got)
	}
}

// TestPeekTypeRejectsMalformedHeader names the offending field for every way a
// header can be unusable, and reports ErrCorrupt rather than a decode failure:
// PeekType resolves no type, so a damaged header is damaged data.
func TestPeekTypeRejectsMalformedHeader(t *testing.T) {
	var lenBuf [binary.MaxVarintLen64]byte
	oversized := func(field string, n uint64) []byte {
		var buf bytes.Buffer
		buf.WriteByte(envelopeVersion)
		switch field {
		case "codec":
			buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], n)])
		case "type":
			buf.WriteByte(0x00) // empty codec
			buf.Write(lenBuf[:binary.PutUvarint(lenBuf[:], n)])
		}
		return buf.Bytes()
	}

	for _, tc := range []struct {
		name  string
		data  []byte
		field string
	}{
		{"empty stream", nil, "envelope version"},
		{"unsupported version", []byte{0xff}, "version"},
		{"truncated codec length", []byte{envelopeVersion}, "codec length"},
		{"oversized codec length", oversized("codec", maxPeekNameLen+1), "exceeds"},
		{"truncated codec", []byte{envelopeVersion, 0x03, 'a'}, "truncated codec"},
		{"truncated type length", []byte{envelopeVersion, 0x00}, "type length"},
		{"empty type", []byte{envelopeVersion, 0x00, 0x00}, "empty type name"},
		{"oversized type length", oversized("type", maxPeekNameLen+1), "exceeds"},
		{"truncated type", []byte{envelopeVersion, 0x00, 0x03, 'a'}, "truncated type"},
		{"truncated v1 type", []byte{envelopeVersionV1, 0x03, 'a'}, "truncated type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PeekType(bytes.NewReader(tc.data))
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("PeekType(%v) = %v, want ErrCorrupt", tc.data, err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error %q does not name the field %q", err, tc.field)
			}
		})
	}
}

// failingReader fails every read with a cause that is not end of stream.
type failingReader struct{ err error }

// Read reports the configured failure.
func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

// TestPeekTypeKeepsReadErrorCause keeps a real read failure on the chain, so a
// caller can tell a broken stream from a truncated one.
func TestPeekTypeKeepsReadErrorCause(t *testing.T) {
	want := errors.New("device gone")
	_, err := PeekType(failingReader{err: want})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PeekType(read failure) = %v, want ErrCorrupt", err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("PeekType(read failure) = %v, want the cause %v on the chain", err, want)
	}
}

// TestPeekVersionReadsExactlyOneByte pins the point of the version peek: one
// byte, whatever the payload size and whichever envelope layout follows it, and
// the same answer for a version this build writes and one it does not know —
// reporting an unknown version is the call's purpose, never an error.
func TestPeekVersionReadsExactlyOneByte(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame func(size int) []byte
		want  byte
	}{
		{"current version", func(size int) []byte {
			return encodeEnvelope("json", "note@1", bytes.Repeat([]byte("x"), size))
		}, EnvelopeVersion},
		{"v1 stream", func(size int) []byte {
			return v1Envelope("note@1", bytes.Repeat([]byte("x"), size))
		}, envelopeVersionV1},
		{"unknown version", func(int) []byte { return []byte{0xff, 0x01, 'a'} }, 0xff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{0, 7, 1 << 20} {
				frame := tc.frame(size)
				cr := &countingReader{r: bytes.NewReader(frame)}

				got, err := PeekVersion(cr)
				if err != nil {
					t.Fatalf("payload %d: PeekVersion = %v", size, err)
				}
				if got != tc.want {
					t.Fatalf("payload %d: version = %d, want %d", size, got, tc.want)
				}
				if cr.n != 1 {
					t.Fatalf("payload %d: read %d bytes, want exactly one", size, cr.n)
				}
				// Nothing was consumed past the version byte: the rest of the same
				// stream is still the frame, byte for byte.
				rest, err := io.ReadAll(cr)
				if err != nil {
					t.Fatalf("payload %d: read rest = %v", size, err)
				}
				if want := frame[1:]; !bytes.Equal(rest, want) {
					t.Fatalf("payload %d: %d bytes left, want the %d-byte rest of the frame", size, len(rest), len(want))
				}
			}
		})
	}
}

// TestEnvelopeVersionMatchesTheWriter keeps the exported constant honest: the
// version byte a caller compares PeekVersion's answer against is the byte the
// writer actually emits, so the constant cannot drift from encodeEnvelope.
func TestEnvelopeVersionMatchesTheWriter(t *testing.T) {
	if got := encodeEnvelope("json", "note@1", []byte("x"))[0]; got != EnvelopeVersion {
		t.Fatalf("encodeEnvelope wrote version %d, EnvelopeVersion is %d", got, EnvelopeVersion)
	}
	if encoded := encodeEnvelope("", "blob@1", nil); encoded[0] != EnvelopeVersion {
		t.Fatalf("encodeEnvelope(empty codec, empty payload) wrote version %d, want %d", encoded[0], EnvelopeVersion)
	}
}

// TestPeekVersionRejectsAnEmptyStream covers the one way the version peek fails:
// there is no byte to report. The error is ErrCorrupt and names the field, at
// the same shape PeekType uses.
func TestPeekVersionRejectsAnEmptyStream(t *testing.T) {
	_, err := PeekVersion(bytes.NewReader(nil))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PeekVersion(empty) = %v, want ErrCorrupt", err)
	}
	if !strings.Contains(err.Error(), "envelope version") {
		t.Fatalf("error %q does not name the envelope version field", err)
	}
}

// TestPeekVersionKeepsReadErrorCause keeps a real read failure on the chain, so
// a caller can tell a broken stream from a truncated one.
func TestPeekVersionKeepsReadErrorCause(t *testing.T) {
	want := errors.New("device gone")
	_, err := PeekVersion(failingReader{err: want})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PeekVersion(read failure) = %v, want ErrCorrupt", err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("PeekVersion(read failure) = %v, want the cause %v on the chain", err, want)
	}
	if !strings.Contains(err.Error(), "envelope version") {
		t.Fatalf("error %q does not name the envelope version field", err)
	}
}

// failAfterReader serves n bytes from data and then fails with err, so a header
// read can break in the middle of a field rather than at end of stream.
type failAfterReader struct {
	data []byte
	n    int
	err  error
}

// Read serves the remaining bytes up to the budget, then reports err.
func (f *failAfterReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n := copy(p, f.data[:f.n])
	f.data = f.data[n:]
	f.n -= n
	return n, nil
}

// TestPeekTypeKeepsCodecReadErrorCause covers the same rule for the codec field:
// a failure while stepping over it keeps its cause and names the field.
func TestPeekTypeKeepsCodecReadErrorCause(t *testing.T) {
	want := errors.New("device gone")
	// version 2, codecLen 4, then the stream breaks after two codec bytes.
	_, err := PeekType(&failAfterReader{data: []byte{envelopeVersion, 0x04, 'j', 's'}, n: 4, err: want})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PeekType(codec read failure) = %v, want ErrCorrupt", err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("PeekType(codec read failure) = %v, want the cause %v on the chain", err, want)
	}
	if !strings.Contains(err.Error(), "codec") {
		t.Fatalf("error %q does not name the codec field", err)
	}
}
