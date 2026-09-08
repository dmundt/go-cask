package cas

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Envelope is the self-describing wire format for stored objects
// (cas-core §8 decision 1):
//
//	+--------+-----------+------------+---------+
//	| Version| TypeLen   | Type       | Payload |
//	+--------+-----------+------------+---------+
//	| 1 byte | uvarint   | N bytes    | rest    |
//	+--------+-----------+------------+---------+
//
// where:
//   - Version is the envelope format version (currently 1).
//   - TypeLen is the length of the versioned type name (e.g. "commit@1"),
//     encoded as a uvarint.
//   - Type is the versioned type name bytes.
//   - Payload is the rest of the object — the codec output, arbitrary bytes.
//
// Benefits: no JSON or base64 overhead, streamable, codec-agnostic, works for
// arbitrary binary payloads, and versionable (a future format bump can be
// detected from the leading byte). Git's object header ("<type> <size>\0")
// follows a similar philosophy.
const envelopeVersion byte = 1

// Envelope is a decoded self-describing object.
type Envelope struct {
	Type string
	Data []byte
}

// marshalEnvelope encodes Type and Data as
// [version u8][uvarint typeLen][type bytes][payload bytes].
func marshalEnvelope(typ string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(envelopeVersion)
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typ)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typ)
	buf.Write(payload)
	return buf.Bytes()
}

// unmarshalEnvelope decodes a TLV envelope, returning the versioned type name
// (an absent major version reads as "@1", object-versioning §2) and the codec
// payload. It returns ErrUnknownType for a malformed envelope or an unknown
// envelope version.
func unmarshalEnvelope(data []byte) (string, []byte, error) {
	r := bytes.NewReader(data)
	ver, err := r.ReadByte()
	if err != nil {
		return "", nil, fmt.Errorf("%w: truncated envelope version", ErrUnknownType)
	}
	if ver != envelopeVersion {
		return "", nil, fmt.Errorf("%w: unsupported envelope version %d", ErrUnknownType, ver)
	}
	typeLen, err := binary.ReadUvarint(r)
	if err != nil {
		return "", nil, fmt.Errorf("%w: truncated type length", ErrUnknownType)
	}
	if typeLen > uint64(r.Len()) {
		return "", nil, fmt.Errorf("%w: type length %d exceeds envelope size", ErrUnknownType, typeLen)
	}
	typeBytes := make([]byte, typeLen)
	if _, err := r.Read(typeBytes); err != nil {
		return "", nil, fmt.Errorf("%w: truncated type", ErrUnknownType)
	}
	typeName := string(typeBytes)
	if typeName == "" {
		return "", nil, fmt.Errorf("%w: object missing type", ErrUnknownType)
	}
	if !containsAt(typeName) {
		typeName += "@1" // legacy unversioned type name
	}
	payload := make([]byte, r.Len())
	if _, err := r.Read(payload); err != nil && err != io.EOF {
		return "", nil, fmt.Errorf("%w: truncated payload", ErrUnknownType)
	}
	return typeName, payload, nil
}

// EnvelopeFromBytes decodes a TLV envelope, returning the versioned type name
// and the codec payload. It is the public accessor to the on-disk format for
// tooling and example layers that inspect raw stored bytes (e.g. ResolveAny).
func EnvelopeFromBytes(data []byte) (Envelope, error) {
	typ, payload, err := unmarshalEnvelope(data)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: typ, Data: payload}, nil
}

func containsAt(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return true
		}
	}
	return false
}
