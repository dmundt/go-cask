package cas

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
