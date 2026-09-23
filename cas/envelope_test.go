package cas

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	payload := []byte("hello world")
	typ := "note@1"
	data := encodeEnvelope(typ, payload)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != typ {
		t.Fatalf("type = %q, want %q", out.Type, typ)
	}
	if string(out.Data) != string(payload) {
		t.Fatalf("data = %q, want %q", string(out.Data), string(payload))
	}
}

func TestEnvelopeLegacyUnversioned(t *testing.T) {
	payload := []byte("{}")
	// Legacy form: type without @major — decodeEnvelope appends @1.
	data := encodeEnvelope("mystery", payload)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "mystery@1" {
		t.Fatalf("legacy type = %q, want mystery@1", out.Type)
	}
}

func TestEnvelopeEmptyPayload(t *testing.T) {
	data := encodeEnvelope("blob@1", nil)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "blob@1" || len(out.Data) != 0 {
		t.Fatalf("empty payload: type=%q data=%v", out.Type, out.Data)
	}
}

func TestEnvelopeTruncatedVersion(t *testing.T) {
	if _, err := EnvelopeFromBytes(nil); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("nil data = %v, want ErrUnknownType", err)
	}
}

func TestEnvelopeUnknownVersion(t *testing.T) {
	data := []byte{0xff, 0x01, 0x61} // version 255, typeLen 1, type "a"
	if _, err := EnvelopeFromBytes(data); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown version = %v", err)
	}
}

func TestEnvelopeTruncatedTypeLen(t *testing.T) {
	data := []byte{0x01} // version only, no typeLen
	if _, err := EnvelopeFromBytes(data); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("truncated typeLen = %v", err)
	}
}

func TestEnvelopeTruncatedType(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x01)
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], 100)
	buf.Write(lenBuf[:n])
	// typeLen = 100 but no type bytes follow
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("truncated type = %v", err)
	}
}

func TestEnvelopeEmptyType(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x01)
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], 0)
	buf.Write(lenBuf[:n])
	// typeLen = 0 → empty type name
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("empty type = %v", err)
	}
}

// TestEnvelopeVersionAppliedToLegacy pins the legacy path end to end: a type
// name without "@major" reads back with "@1" appended.
func TestEnvelopeVersionAppliedToLegacy(t *testing.T) {
	env, err := EnvelopeFromBytes(encodeEnvelope("blob", []byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "blob@1" {
		t.Fatalf("unversioned type name = %q, want blob@1", env.Type)
	}
	env, err = EnvelopeFromBytes(encodeEnvelope("blob@2", []byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != "blob@2" {
		t.Fatalf("versioned type name = %q, want blob@2", env.Type)
	}
}

func TestEnvelopeMarshalDeterministic(t *testing.T) {
	a := encodeEnvelope("t", []byte("x"))
	b := encodeEnvelope("t", []byte("x"))
	if !bytes.Equal(a, b) {
		t.Fatalf("deterministic marshal: %x != %x", a, b)
	}
}

func TestEnvelopeFromBytesExported(t *testing.T) {
	raw := encodeEnvelope("exported@1", []byte("data"))
	env, err := EnvelopeFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(env.Type, "exported") {
		t.Fatalf("type = %q", env.Type)
	}
	if string(env.Data) != "data" {
		t.Fatalf("data = %q", env.Data)
	}
}

// TestEnvelopeFormatWithPayloadLen pins the byte layout: a payload-length
// field precedes the payload, so the frame is self-delimiting.
func TestEnvelopeFormatWithPayloadLen(t *testing.T) {
	typ := "note@1"
	payload := []byte("abc")
	data := encodeEnvelope(typ, payload)
	r := bytes.NewReader(data)
	// [version u8]
	if v, _ := r.ReadByte(); v != envelopeVersion {
		t.Fatalf("version = %d", v)
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

func TestEnvelopeTruncatedPayloadLen(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x01)
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note@1")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note@1")
	// No payloadLen follows -> truncated payload length error.
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("truncated payloadLen = %v", err)
	}
}

func TestEnvelopePayloadLenExceeds(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x01)
	var lenBuf [10]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note@1")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note@1")
	// payloadLen = 100 but only a few bytes follow.
	n = binary.PutUvarint(lenBuf[:], 100)
	buf.Write(lenBuf[:n])
	buf.WriteString("xy")
	if _, err := EnvelopeFromBytes(buf.Bytes()); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("oversized payloadLen = %v", err)
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

// headerSize is the exact length of the [version][uvarint typeLen][type] header
// encodeEnvelope writes for typ.
func headerSize(typ string) int {
	var buf [binary.MaxVarintLen64]byte
	return 1 + binary.PutUvarint(buf[:], uint64(len(typ))) + len(typ)
}

// headerTail is what follows that header — the payload-length field and the
// payload — which a peek deliberately leaves unread.
func headerTail(typ string, payload []byte) []byte {
	return encodeEnvelope(typ, payload)[headerSize(typ):]
}

// TestPeekTypeReadsOnlyTheHeader pins the point of the peek: the bytes consumed
// are the header whatever the payload size, and the stream is left positioned
// exactly after it.
func TestPeekTypeReadsOnlyTheHeader(t *testing.T) {
	const typ = "blob@1"
	for _, size := range []int{0, 7, 1 << 20} {
		payload := bytes.Repeat([]byte("x"), size)
		cr := &countingReader{r: bytes.NewReader(encodeEnvelope(typ, payload))}

		got, err := PeekType(cr)
		if err != nil {
			t.Fatalf("payload %d: PeekType = %v", size, err)
		}
		if got != typ {
			t.Fatalf("payload %d: type = %q, want %q", size, got, typ)
		}
		if want := headerSize(typ); cr.n != want {
			t.Fatalf("payload %d: read %d bytes, want exactly the %d-byte header", size, cr.n, want)
		}
		// Nothing was consumed past the header: the rest of the same stream is
		// still the framed tail — the payload-length field and the payload —
		// byte for byte.
		rest, err := io.ReadAll(cr)
		if err != nil {
			t.Fatalf("payload %d: read rest = %v", size, err)
		}
		if want := headerTail(typ, payload); !bytes.Equal(rest, want) {
			t.Fatalf("payload %d: %d bytes left, want the %d-byte framed tail", size, len(rest), len(want))
		}
	}
}

// TestPeekTypeAcceptsAByteReader covers the other branch: a reader that can read
// single bytes itself is used directly, and still stops after the header.
func TestPeekTypeAcceptsAByteReader(t *testing.T) {
	payload := []byte("payload")
	r := bytes.NewReader(encodeEnvelope("tree@2", payload)) // *bytes.Reader is an io.ByteReader

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
	if want := headerTail("tree@2", payload); !bytes.Equal(rest, want) {
		t.Fatalf("%d bytes left, want the %d-byte framed tail", len(rest), len(want))
	}
}

// TestPeekTypeLegacyUnversionedName keeps the "@1" default of the byte-slice
// reader and the streaming one identical.
func TestPeekTypeLegacyUnversionedName(t *testing.T) {
	got, err := PeekType(bytes.NewReader(encodeEnvelope("mystery", []byte("{}"))))
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
	oversized := append([]byte{envelopeVersion}, lenBuf[:binary.PutUvarint(lenBuf[:], maxPeekTypeLen+1)]...)

	for _, tc := range []struct {
		name  string
		data  []byte
		field string
	}{
		{"empty stream", nil, "envelope version"},
		{"unsupported version", []byte{0xff}, "version"},
		{"truncated type length", []byte{envelopeVersion}, "type length"},
		{"empty type", []byte{envelopeVersion, 0x00}, "empty type name"},
		{"oversized type length", oversized, "exceeds"},
		{"truncated type", []byte{envelopeVersion, 0x03, 'a'}, "truncated type"},
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
