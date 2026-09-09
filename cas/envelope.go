package cas

import (
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
	Type string
	Data []byte
}

// envelopeVersion is the current envelope format version.
const envelopeVersion byte = 1

// marshalEnvelope encodes Type and payload as
// [version u8][uvarint typeLen][type][uvarint payloadLen][payload]. The
// encoded length is known up front, so the whole envelope is written into a
// single pre-sized allocation (no growing buffer, no final copy).
func marshalEnvelope(typ string, payload []byte) []byte {
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

// parseEnvelope decodes a TLV envelope from an in-memory buffer, returning the
// versioned type name (an absent major version reads as "@1",
// object-versioning §2) and the codec payload. The payload is returned as a
// zero-copy sub-slice of data — the caller must not retain it past data's
// lifetime (Store.Get, the hot path, decodes it and discards it immediately).
// It returns ErrUnknownType for a malformed envelope or an unknown envelope
// version.
func parseEnvelope(data []byte) (string, []byte, error) {
	if len(data) < 1 {
		return "", nil, fmt.Errorf("%w: truncated envelope version", ErrUnknownType)
	}
	if data[0] != envelopeVersion {
		return "", nil, fmt.Errorf("%w: unsupported envelope version %d", ErrUnknownType, data[0])
	}
	off := 1
	typeLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return "", nil, fmt.Errorf("%w: truncated type length", ErrUnknownType)
	}
	off += n
	if typeLen == 0 || typeLen > uint64(len(data)-off) {
		return "", nil, fmt.Errorf("%w: object missing or oversized type", ErrUnknownType)
	}
	typeName := string(data[off : off+int(typeLen)])
	off += int(typeLen)
	if !strings.Contains(typeName, "@") {
		typeName += "@1" // legacy unversioned type name
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

// EnvelopeFromBytes decodes a TLV envelope, returning the versioned type name
// and the codec payload. It is the public accessor to the on-disk format for
// tooling and example layers that inspect raw stored bytes (e.g. ResolveAny).
// The returned Envelope.Data is an independent copy of the payload, so callers
// may retain it beyond the input buffer's lifetime.
func EnvelopeFromBytes(data []byte) (Envelope, error) {
	typ, payload, err := parseEnvelope(data)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: typ, Data: append([]byte(nil), payload...)}, nil
}
