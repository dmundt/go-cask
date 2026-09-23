package cas

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CodecNamer is an optional interface for a Codec[T] that can name the wire
// format it produces.
//
// The name is a stable identity tag written into every envelope the store
// produces (Envelope.Codec) and compared on read, so swapping the codec behind
// a type is reported as ErrCodecMismatch instead of surfacing as a decode
// failure. It is an optional interface on purpose: adding a method to Codec[T]
// would break every existing implementation, in this repo and in consumers, for
// a check that only applies when both writer and reader opt in.
//
// A tag is lowercase ASCII with no "@" (it is not a type name) and is declared,
// never derived: no tag comes from the Go type name, reflection or the payload
// bytes, so renaming a type or moving a package is not read as a format change.
// The shipped codecs report "json", "gob", "cbor" and "binary", and a codec
// stacked over another composes the inner tag ("gzip+json" for
// gzip.New(json.New[T]())).
type CodecNamer interface {
	// CodecName returns the codec's identity tag, or "" when it declares no
	// tag. An empty tag means "unspecified": the envelope carries no identity
	// and no comparison is made on read.
	CodecName() string
}

// Envelope is a decoded, self-describing object.
//
// Stored objects use the TLV envelope wire format (cas-core §8 decision 1):
//
//	+--------+----------+---------+----------+---------+------------+---------+
//	| Version| CodecLen | Codec   | TypeLen  | Type    | PayloadLen | Payload |
//	+--------+----------+---------+----------+---------+------------+---------+
//	| 1 byte | uvarint  | N bytes | uvarint  | M bytes | uvarint    | K bytes |
//	+--------+----------+---------+----------+---------+------------+---------+
//
// where:
//   - Version is the envelope format version (currently envelopeVersion).
//   - CodecLen is the length of the codec identity tag, encoded as a uvarint.
//     Zero is legal and means "unspecified".
//   - Codec is the codec identity tag bytes (CodecNamer). Version 1 of the
//     format has no codec field at all, so a v1 envelope reads as unspecified.
//   - TypeLen is the length of the versioned type name (e.g. "commit@1"),
//     encoded as a uvarint.
//   - Type is the versioned type name bytes.
//   - PayloadLen is the length of the payload, encoded as a uvarint.
//   - Payload is exactly PayloadLen bytes — the codec output, arbitrary bytes.
//
// The PayloadLen field is last, which makes the frame self-delimiting: a reader
// can locate the exact payload extent without scanning to EOF, which is useful
// for streaming and range reads. Version makes a future format bump detectable
// from the leading byte.
type Envelope struct {
	// Type identifies the encoded object type and major version.
	Type string
	// Codec is the identity tag of the codec that produced the payload (the
	// CodecNamer tag of the writing codec). It is empty when the envelope
	// carries none: a version 1 envelope, or one written by a codec that
	// declares no tag.
	Codec string
	// Data contains the encoded object payload.
	Data []byte
}

// envelopeVersion is the current envelope format version. Version 2 adds the
// codec identity tag in front of the type name, so a reader can tell "written
// with another codec" from "damaged bytes".
const envelopeVersion byte = 2

// envelopeVersionV1 is the first envelope format version, which has no codec
// field. It stays readable: such an envelope decodes with an empty Codec, i.e.
// "codec unspecified", and is never reported as a codec mismatch.
const envelopeVersionV1 byte = 1

// encodeEnvelope writes codec, typ and payload as
// [version u8][uvarint codecLen][codec][uvarint typeLen][type][uvarint payloadLen][payload].
// An empty codec is legal and means the writing codec declares no identity. The
// encoded length is known up front, so the whole envelope is written into a
// single pre-sized allocation (no growing buffer, no final copy).
func encodeEnvelope(codec, typ string, payload []byte) []byte {
	var lenBuf [binary.MaxVarintLen64]byte
	nCodec := binary.PutUvarint(lenBuf[:], uint64(len(codec)))
	nType := binary.PutUvarint(lenBuf[:], uint64(len(typ)))
	nPayload := binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	total := 1 + nCodec + len(codec) + nType + len(typ) + nPayload + len(payload)
	out := make([]byte, total)
	out[0] = envelopeVersion
	off := 1
	off += binary.PutUvarint(out[off:], uint64(len(codec)))
	copy(out[off:], codec)
	off += len(codec)
	off += binary.PutUvarint(out[off:], uint64(len(typ)))
	copy(out[off:], typ)
	off += len(typ)
	off += binary.PutUvarint(out[off:], uint64(len(payload)))
	copy(out[off:], payload)
	return out
}

// decodeEnvelopeHeader decodes only the leading header of an envelope — the
// version byte, the codec identity tag and the versioned type name — and
// returns the codec tag, the type name, and the offset where the payload-length
// field starts.
//
// A version 1 envelope has no codec field: its tag reads back empty
// ("unspecified") and the type name follows the version byte directly. An empty
// codec tag in a version 2 envelope is legal and means the same thing.
//
// It reads no byte beyond the type, so a truncated object prefix (a caller that
// read a bounded number of bytes rather than the whole object) still yields the
// type. Both decodeEnvelope and the exported EnvelopeType are built on it, so
// there is exactly one implementation of the header layout.
func decodeEnvelopeHeader(data []byte) (codec, typeName string, off int, err error) {
	if len(data) < 1 {
		return "", "", 0, fmt.Errorf("%w: truncated envelope version", ErrUnknownType)
	}
	version := data[0]
	if version != envelopeVersion && version != envelopeVersionV1 {
		return "", "", 0, fmt.Errorf("%w: unsupported envelope version %d", ErrUnknownType, version)
	}
	off = 1
	if version == envelopeVersion {
		codecLen, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return "", "", 0, fmt.Errorf("%w: truncated codec length", ErrUnknownType)
		}
		off += n
		if codecLen > uint64(len(data)-off) {
			return "", "", 0, fmt.Errorf("%w: object missing or oversized codec", ErrUnknownType)
		}
		codec = string(data[off : off+int(codecLen)])
		off += int(codecLen)
	}
	typeLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return "", "", 0, fmt.Errorf("%w: truncated type length", ErrUnknownType)
	}
	off += n
	if typeLen == 0 || typeLen > uint64(len(data)-off) {
		return "", "", 0, fmt.Errorf("%w: object missing or oversized type", ErrUnknownType)
	}
	typeName = string(data[off : off+int(typeLen)])
	off += int(typeLen)
	if !strings.Contains(typeName, "@") {
		typeName += "@1" // legacy unversioned type name
	}
	return codec, typeName, off, nil
}

// decodeEnvelope decodes a TLV envelope from an in-memory buffer: the versioned
// type name (an absent major version reads as "@1", object-versioning §2), the
// codec identity tag (empty when the envelope carries none) and the codec
// payload. The payload is returned as a zero-copy sub-slice of data — the
// caller must not retain it past data's lifetime (Store.Get, the hot path,
// decodes it and discards it immediately; EnvelopeFromBytes clones it). It
// returns ErrUnknownType for a malformed envelope or an unknown envelope
// version.
//
// Bytes after the declared payload are ignored (the field is self-delimiting):
// readers tolerate a frame extension that appends fields without breaking
// existing objects, while the writer never emits a trailer.
func decodeEnvelope(data []byte) (Envelope, error) {
	codec, typeName, off, err := decodeEnvelopeHeader(data)
	if err != nil {
		return Envelope{}, err
	}
	payloadLen, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return Envelope{}, fmt.Errorf("%w: truncated payload length", ErrUnknownType)
	}
	off += n
	if payloadLen > uint64(len(data)-off) {
		return Envelope{}, fmt.Errorf("%w: payload length exceeds envelope size", ErrUnknownType)
	}
	return Envelope{Type: typeName, Codec: codec, Data: data[off : off+int(payloadLen)]}, nil
}

// EnvelopeType returns the versioned type name of the envelope at the start of
// data without reading its payload. It is the header-only counterpart of
// EnvelopeFromBytes, for callers that only need to know what an object is: a
// bounded prefix of the object is enough, so the payload is never buffered and
// a truncated prefix still yields its type. An absent major version reads back
// as "@1" (object-versioning §2).
//
// The codec identity tag is stepped over but not returned; EnvelopeFromBytes
// reports it for callers that need it.
//
// It returns ErrUnknownType when data does not begin with a usable envelope
// header.
func EnvelopeType(data []byte) (string, error) {
	_, typeName, _, err := decodeEnvelopeHeader(data)
	if err != nil {
		return "", err
	}
	return typeName, nil
}

// EnvelopeFromBytes decodes a TLV envelope, returning the versioned type name,
// the codec identity tag and the codec payload. It is the public accessor to
// the on-disk format for tooling and example layers that inspect raw stored
// bytes (e.g. ResolveAny). The returned Envelope.Data is an independent copy of
// the payload, so callers may retain it beyond the input buffer's lifetime.
func EnvelopeFromBytes(data []byte) (Envelope, error) {
	env, err := decodeEnvelope(data)
	if err != nil {
		return Envelope{}, err
	}
	env.Data = bytes.Clone(env.Data)
	return env, nil
}

// maxPeekNameLen bounds a header string field PeekType will read: the codec
// identity tag and the type name. A stored header is untrusted bytes, and both
// fields are short strings (a tag of a few characters, "<type>@<major>" for a
// name of a few dozen), so a declared length beyond this is treated as a
// corrupt header rather than allocated. It is deliberately far above any real
// header field.
const maxPeekNameLen = 1 << 12

// PeekType reads only the envelope header from r —
// [version u8][uvarint codecLen][codec][uvarint typeLen][type] — and returns
// the versioned type name. The codec identity tag is stepped over, not
// reported: EnvelopeFromBytes is the reader that reports it. The payload is
// never read: the stream is consumed exactly as far as the type field, so the
// cost is independent of the object's size. An absent major version reads back
// as "@1" (object-versioning §2).
//
// A version 1 stream has no codec field and is read as such.
//
// It is the streaming counterpart of EnvelopeType, for callers that enumerate a
// store and want each object's type without paying for its bytes: a store can
// report what it holds with List followed by PeekType (or Store.Type) per
// digest, where Store.Get would decode every payload.
//
// It returns ErrCorrupt when the stream does not begin with a usable header,
// naming the offending field — an unsupported envelope version, a truncated or
// oversized codec tag, a truncated type length, an empty type, a declared type
// longer than maxPeekNameLen, or a truncated type.
// ErrCorrupt is the header-level counterpart of the payload-level decode
// failure Store.Get reports; PeekType resolves no type, so ErrUnknownType does
// not apply to it. A read failure other than end of stream is wrapped with its
// cause.
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
	if version != envelopeVersion && version != envelopeVersionV1 {
		return "", fmt.Errorf("%w: peek type: unsupported envelope version %d", ErrCorrupt, version)
	}
	if version == envelopeVersion {
		if err := skipPeekName(br, rd, "codec"); err != nil {
			return "", err
		}
	}
	typeLen, err := binary.ReadUvarint(br)
	if err != nil {
		return "", peekError("type length", err)
	}
	if typeLen == 0 {
		return "", fmt.Errorf("%w: peek type: empty type name", ErrCorrupt)
	}
	if typeLen > maxPeekNameLen {
		return "", fmt.Errorf("%w: peek type: type length %d exceeds %d", ErrCorrupt, typeLen, maxPeekNameLen)
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

// skipPeekName consumes a length-prefixed header string field from a peek
// stream without keeping it. An empty field is legal (an empty codec tag means
// "unspecified"), while a declared length beyond maxPeekNameLen is a corrupt
// header rather than something to skip on trust.
func skipPeekName(br io.ByteReader, rd io.Reader, field string) error {
	fieldLen, err := binary.ReadUvarint(br)
	if err != nil {
		return peekError(field+" length", err)
	}
	if fieldLen > maxPeekNameLen {
		return fmt.Errorf("%w: peek type: %s length %d exceeds %d", ErrCorrupt, field, fieldLen, maxPeekNameLen)
	}
	if fieldLen == 0 {
		return nil
	}
	if _, err := io.CopyN(io.Discard, rd, int64(fieldLen)); err != nil {
		return peekError(field, err)
	}
	return nil
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
