package cas

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Envelope is a decoded, self-describing object.
//
// Stored objects use the TLV envelope wire format (cas-core §8 decision 1):
//
//	+--------+-----------+------------+-----------+---------+
//	| Version| TypeLen   | Type       | PayloadLen| Payload |
//	+--------+-----------+------------+-----------+---------+
//	| 1 byte | uvarint   | N bytes    | uvarint   | M bytes |
//	+--------+-----------+------------+-----------+---------+
//
// where:
//   - Version is the envelope format version (currently envelopeVersion).
//   - TypeLen is the length of the versioned type name (e.g. "commit@1"),
//     encoded as a uvarint.
//   - Type is the versioned type name bytes.
//   - PayloadLen is the length of the payload, encoded as a uvarint.
//   - Payload is exactly PayloadLen bytes — the codec output, arbitrary bytes.
//
// The PayloadLen field makes the frame self-delimiting: a reader can locate
// the exact payload extent without scanning to EOF, which is useful for
// streaming and range reads. Version makes a future format bump detectable
// from the leading byte.
type Envelope struct {
	// Type identifies the encoded object type and major version.
	Type string
	// Data contains the encoded object payload.
	Data []byte
}

// envelopeVersion is the current envelope format version.
const envelopeVersion byte = 1

// encodeEnvelope writes Type and payload as
// [version u8][uvarint typeLen][type][uvarint payloadLen][payload]. The
// encoded length is known up front, so the whole envelope is written into a
// single pre-sized allocation (no growing buffer, no final copy).
func encodeEnvelope(typ string, payload []byte) []byte {
	var lenBuf [binary.MaxVarintLen64]byte
	nType := binary.PutUvarint(lenBuf[:], uint64(len(typ)))
	nPayload := binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	total := 1 + nType + len(typ) + nPayload + len(payload)
	out := make([]byte, total)
	out[0] = envelopeVersion
	off := 1
	off += binary.PutUvarint(out[off:], uint64(len(typ)))
	copy(out[off:], typ)
	off += len(typ)
	off += binary.PutUvarint(out[off:], uint64(len(payload)))
	copy(out[off:], payload)
	return out
}

// decodeEnvelopeType decodes only the leading header of an envelope —
// [version u8][uvarint typeLen][type] — and returns the versioned type name
// together with the offset where the payload-length field starts.
//
// It reads no byte beyond the type, so a truncated object prefix (a caller that
// read a bounded number of bytes rather than the whole object) still yields the
// type. Both decodeEnvelope and the exported EnvelopeType are built on it, so
// there is exactly one implementation of the header layout.
func decodeEnvelopeType(data []byte) (string, int, error) {
	if len(data) < 1 {
		return "", 0, fmt.Errorf("%w: truncated envelope version", ErrUnknownType)
	}
	if data[0] != envelopeVersion {
		return "", 0, fmt.Errorf("%w: unsupported envelope version %d", ErrUnknownType, data[0])
	}
	off := 1
	typeLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return "", 0, fmt.Errorf("%w: truncated type length", ErrUnknownType)
	}
	off += n
	if typeLen == 0 || typeLen > uint64(len(data)-off) {
		return "", 0, fmt.Errorf("%w: object missing or oversized type", ErrUnknownType)
	}
	typeName := string(data[off : off+int(typeLen)])
	off += int(typeLen)
	if !strings.Contains(typeName, "@") {
		typeName += "@1" // legacy unversioned type name
	}
	return typeName, off, nil
}

// decodeEnvelope decodes a TLV envelope from an in-memory buffer, returning the
// versioned type name (an absent major version reads as "@1",
// object-versioning §2) and the codec payload. The payload is returned as a
// zero-copy sub-slice of data — the caller must not retain it past data's
// lifetime (Store.Get, the hot path, decodes it and discards it immediately).
// It returns ErrUnknownType for a malformed envelope or an unknown envelope
// version.
//
// Bytes after the declared payload are ignored (the field is self-delimiting):
// readers tolerate a frame extension that appends fields without breaking
// existing objects, while the writer never emits a trailer.
func decodeEnvelope(data []byte) (string, []byte, error) {
	typeName, off, err := decodeEnvelopeType(data)
	if err != nil {
		return "", nil, err
	}
	payloadLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return "", nil, fmt.Errorf("%w: truncated payload length", ErrUnknownType)
	}
	off += n
	if payloadLen > uint64(len(data)-off) {
		return "", nil, fmt.Errorf("%w: payload length exceeds envelope size", ErrUnknownType)
	}
	return typeName, data[off : off+int(payloadLen)], nil
}

// EnvelopeType returns the versioned type name of the envelope at the start of
// data without reading its payload. It is the header-only counterpart of
// EnvelopeFromBytes, for callers that only need to know what an object is: a
// bounded prefix of the object is enough, so the payload is never buffered and
// a truncated prefix still yields its type. An absent major version reads back
// as "@1" (object-versioning §2).
//
// It returns ErrUnknownType when data does not begin with a usable envelope
// header.
func EnvelopeType(data []byte) (string, error) {
	typeName, _, err := decodeEnvelopeType(data)
	if err != nil {
		return "", err
	}
	return typeName, nil
}

// EnvelopeFromBytes decodes a TLV envelope, returning the versioned type name
// and the codec payload. It is the public accessor to the on-disk format for
// tooling and example layers that inspect raw stored bytes (e.g. ResolveAny).
// The returned Envelope.Data is an independent copy of the payload, so callers
// may retain it beyond the input buffer's lifetime.
func EnvelopeFromBytes(data []byte) (Envelope, error) {
	typ, payload, err := decodeEnvelope(data)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: typ, Data: bytes.Clone(payload)}, nil
}

// maxPeekTypeLen bounds the type name PeekType will read. A stored header is
// untrusted bytes, and the versioned type name is a short string
// (object-versioning §2), so a declared length beyond this is treated as a
// corrupt header rather than allocated. It is deliberately far above any real
// type name: "<type>@<major>" for a name of a few dozen characters.
const maxPeekTypeLen = 1 << 12

// PeekType reads only the envelope header from r —
// [version u8][uvarint typeLen][type] — and returns the versioned type name.
// The payload is never read: the stream is consumed exactly as far as the type
// field, so the cost is independent of the object's size. An absent major
// version reads back as "@1" (object-versioning §2).
//
// It is the streaming counterpart of EnvelopeType, for callers that enumerate a
// store and want each object's type without paying for its bytes: a store can
// report what it holds with List followed by PeekType (or Store.Type) per
// digest, where Store.Get would decode every payload.
//
// It returns ErrCorrupt when the stream does not begin with a usable header,
// naming the offending field — an unsupported envelope version, a truncated
// type length, an empty type, a declared type longer than maxPeekTypeLen, or a
// truncated type. ErrCorrupt is the header-level counterpart of the
// payload-level decode failure Store.Get reports; PeekType resolves no type, so
// ErrUnknownType does not apply to it. A read failure other than end of stream
// is wrapped with its cause.
func PeekType(r io.Reader) (string, error) {
	rd := r
	br, ok := r.(io.ByteReader)
	if !ok {
		adapter := byteReader{r: r}
		br, rd = adapter, adapter
	}
	version, err := br.ReadByte()
	if err != nil {
		return "", peekError("envelope version", err)
	}
	if version != envelopeVersion {
		return "", fmt.Errorf("%w: peek type: unsupported envelope version %d", ErrCorrupt, version)
	}
	typeLen, err := binary.ReadUvarint(br)
	if err != nil {
		return "", peekError("type length", err)
	}
	if typeLen == 0 {
		return "", fmt.Errorf("%w: peek type: empty type name", ErrCorrupt)
	}
	if typeLen > maxPeekTypeLen {
		return "", fmt.Errorf("%w: peek type: type length %d exceeds %d", ErrCorrupt, typeLen, maxPeekTypeLen)
	}
	name := make([]byte, typeLen)
	if _, err := io.ReadFull(rd, name); err != nil {
		return "", peekError("type", err)
	}
	typeName := string(name)
	if !strings.Contains(typeName, "@") {
		typeName += "@1" // legacy unversioned type name
	}
	return typeName, nil
}

// peekError turns a header read failure into an ErrCorrupt that names the field
// it happened in. End of stream needs no cause (there is nothing more to say),
// while any other read error keeps its cause on the chain.
func peekError(field string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: peek type: truncated %s", ErrCorrupt, field)
	}
	return fmt.Errorf("%w: peek type: read %s: %w", ErrCorrupt, field, err)
}

// byteReader adapts an io.Reader to io.ByteReader one byte at a time. PeekType
// uses it when the caller's reader cannot read single bytes itself: a buffered
// reader would read ahead into the payload, which is exactly the cost PeekType
// exists to avoid. It keeps a Read method so the type field can still be filled
// in one bounded read from the same position.
type byteReader struct {
	r io.Reader
}

// Read reads from the underlying reader.
func (b byteReader) Read(p []byte) (int, error) { return b.r.Read(p) }

// ReadByte reads exactly one byte from the underlying reader.
func (b byteReader) ReadByte() (byte, error) {
	var one [1]byte
	if _, err := io.ReadFull(b.r, one[:]); err != nil {
		return 0, err
	}
	return one[0], nil
}
