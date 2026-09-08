package cas

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"encoding/json"
	"io"
)

// Codec[T] serializes typed values to and from bytes. The contract:
// Decode(Encode(v)) == v for all storable values (round-trip). The default
// is JSONCodec[T]; gob, compressed, encryption or protobuf codecs are
// additional Codec[T] implementations and never change the byte layer.
type Codec[T any] interface {
	Encode(v T) ([]byte, error)
	Decode(data []byte) (T, error)
}

// JSONCodec[T] is the default Codec[T]: encoding/json Marshal/Unmarshal.
type JSONCodec[T any] struct{}

// Encode marshals v to JSON.
func (JSONCodec[T]) Encode(v T) ([]byte, error) { return json.Marshal(v) }

// Decode unmarshals JSON into a fresh T.
func (JSONCodec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}

// GobCodec[T] serializes with encoding/gob: compact binary output, no field
// names. Both producer and consumer must be Go programs using the same type.
// Prefer it over JSONCodec when payload size and encode/decode speed matter
// more than human-readability or cross-language interop.
type GobCodec[T any] struct{}

// Encode gob-encodes v.
func (GobCodec[T]) Encode(v T) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode gob-decodes data into a fresh T.
func (GobCodec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}

// CompressedCodec[T] wraps any Codec[T] and gzip-compresses the encoded
// payload before it is stored. Decode transparently decompresses. Use it for
// compressible large objects; the envelope type@major is unaffected, so the
// same store may hold compressed and uncompressed objects.
type CompressedCodec[T any] struct {
	inner Codec[T]
}

// NewCompressedCodec wraps inner with gzip compression.
func NewCompressedCodec[T any](inner Codec[T]) CompressedCodec[T] {
	return CompressedCodec[T]{inner: inner}
}

// Encode compresses inner.Encode(v) with gzip.
func (c CompressedCodec[T]) Encode(v T) ([]byte, error) {
	plain, err := c.inner.Encode(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(plain) // bytes.Buffer never fails
	zw.Close()      // flush + checksum; buffer never fails
	return buf.Bytes(), nil
}

// Decode gunzips data and delegates to inner.Decode.
func (c CompressedCodec[T]) Decode(data []byte) (T, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		var zero T
		return zero, err
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		var zero T
		return zero, err
	}
	return c.inner.Decode(plain)
}
