// Package gzip implements a codec that wraps another codec and applies the
// standard library's compress/gzip format to the encoded bytes.
//
// The gzip codec is a representation-layer optimization: it compresses the
// serialized payload without changing object identity or store semantics.
package gzip

import (
	"bytes"
	stdgzip "compress/gzip"
	"errors"
	"fmt"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Codec[T] wraps a base codec and compresses bytes with gzip before storing or
// after reading them back. The wrapped codec remains the serialization authority;
// the gzip layer is purely a transport/storage optimization.
type Codec[T any] struct {
	next cas.Codec[T]
}

var errNilCodec = errors.New("gzipcodec: next codec is nil")

// MaxDecodedBytes bounds how many bytes a single Decode call will decompress.
// The compressed payload is stored data, and a small one can expand without
// limit (a compression bomb), so the read stops at this ceiling instead of
// allocating until the machine gives up. A caller whose legitimate payload
// exceeds it needs a codec that streams rather than this decompress-to-memory
// layer.
const MaxDecodedBytes = 1 << 30

// ErrDecodedTooLarge reports a payload whose decompressed form exceeds
// MaxDecodedBytes. The condition belongs to the encoded bytes, not to the
// wrapped codec, so the wrapped codec is never asked to decode them.
var ErrDecodedTooLarge = fmt.Errorf("gzipcodec: decoded payload exceeds %d bytes", MaxDecodedBytes)

// New returns a gzip-compressing codec for type T.
func New[T any](next cas.Codec[T]) Codec[T] {
	return Codec[T]{next: next}
}

func encodeCompressed[T any](next cas.Codec[T], v T, newWriter func(io.Writer) (io.WriteCloser, error)) ([]byte, error) {
	if next == nil {
		return nil, errNilCodec
	}

	payload, err := next.Encode(v)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Grow(len(payload) + len(payload)/8 + 64)
	w, err := newWriter(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(payload); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeCompressed gunzips data and hands the bytes to next. maxDecoded bounds
// the inflated payload: the stored bytes are untrusted, and a small compressed
// payload can expand without limit, so the read is capped rather than trusting
// the stream. It is a parameter rather than the constant directly so the
// internal tests can exercise the ceiling without inflating a gigabyte.
func decodeCompressed[T any](next cas.Codec[T], data []byte, maxDecoded int64, newReader func(io.Reader) (io.ReadCloser, error)) (T, error) {
	var zero T
	if next == nil {
		return zero, errNilCodec
	}

	r, err := newReader(bytes.NewReader(data))
	if err != nil {
		return zero, err
	}
	defer r.Close()

	// The ceiling applies to the decompressed stream, not to data: a small
	// compressed payload can expand far beyond its own size. LimitReader stops
	// the decompressor one byte past the ceiling, so the check below can tell
	// "exactly at the limit" from "over it".
	payload, err := io.ReadAll(io.LimitReader(r, maxDecoded+1))
	if err != nil {
		return zero, err
	}
	if int64(len(payload)) > maxDecoded {
		return zero, ErrDecodedTooLarge
	}
	return next.Decode(payload)
}

// Encode serializes v with the wrapped codec and then gzip-compresses the
// result.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	return encodeCompressed(c.next, v, func(w io.Writer) (io.WriteCloser, error) {
		return stdgzip.NewWriter(w), nil
	})
}

// Decode gunzips the incoming data and then decodes it with the wrapped
// codec. A payload that inflates past MaxDecodedBytes is rejected with
// ErrDecodedTooLarge.
func (c Codec[T]) Decode(data []byte) (T, error) {
	return decodeCompressed(c.next, data, MaxDecodedBytes, func(r io.Reader) (io.ReadCloser, error) {
		return stdgzip.NewReader(r)
	})
}

// CodecName reports the codec identity tag written into the envelope:
// "gzip+<inner tag>" — "gzip+json" for gzip.New(json.New[T]()) — because the
// stored bytes are gzip-compressed inner bytes, not inner bytes. A wrapped
// codec that declares no tag yields "" (unspecified), so nesting an unnamed
// codec never manufactures a tag that later reads as a mismatch. It satisfies
// cas.CodecNamer.
func (c Codec[T]) CodecName() string { return composeTag("gzip", c.next) }

// composeTag builds the identity tag of a codec stacked over next:
// "<name>+<inner tag>". It reports "" when next declares no tag, so an unnamed
// inner codec leaves the stack unspecified rather than manufacturing a tag that
// would later read as a mismatch.
func composeTag[T any](name string, next cas.Codec[T]) string {
	namer, ok := next.(cas.CodecNamer)
	if !ok {
		return ""
	}
	inner := namer.CodecName()
	if inner == "" {
		return ""
	}
	return name + "+" + inner
}
