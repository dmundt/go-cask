package cas

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	payload := []byte("hello world")
	typ := "note@1"
	data := marshalEnvelope(typ, payload)
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
	// Legacy form: type without @major — unmarshalEnvelope appends @1.
	data := marshalEnvelope("mystery", payload)
	out, err := EnvelopeFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "mystery@1" {
		t.Fatalf("legacy type = %q, want mystery@1", out.Type)
	}
}

func TestEnvelopeEmptyPayload(t *testing.T) {
	data := marshalEnvelope("blob@1", nil)
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

func TestEnvelopeVersionAppliedToLegacy(t *testing.T) {
	if !strings.Contains("blob@1", "@") {
		t.Fatal("versioned name lacks @")
	}
	if strings.Contains("blob", "@") {
		t.Fatal("unversioned name has @")
	}
}

func TestEnvelopeMarshalDeterministic(t *testing.T) {
	a := marshalEnvelope("t", []byte("x"))
	b := marshalEnvelope("t", []byte("x"))
	if !bytes.Equal(a, b) {
		t.Fatalf("deterministic marshal: %x != %x", a, b)
	}
}

func TestEnvelopeFromBytesExported(t *testing.T) {
	raw := marshalEnvelope("exported@1", []byte("data"))
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
	data := marshalEnvelope(typ, payload)
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
