// Package gzip provides a Codec[T] that wraps another codec and compresses its
// serialized bytes with the standard library's compress/gzip (cas-core §4.6).
// This keeps the CAS object model unchanged while making storage cheaper for
// large or repetitive payloads. It is an optional codec-layer optimization, not
// a new object type or a semantic change to the store.
package gzip

import (
	"bytes"
	stdgzip "compress/gzip"
	"errors"
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

func decodeCompressed[T any](next cas.Codec[T], data []byte, newReader func(io.Reader) (io.ReadCloser, error)) (T, error) {
	var zero T
	if next == nil {
		return zero, errNilCodec
	}

	r, err := newReader(bytes.NewReader(data))
	if err != nil {
		return zero, err
	}
	defer r.Close()

	payload, err := io.ReadAll(r)
	if err != nil {
		return zero, err
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
// codec.
func (c Codec[T]) Decode(data []byte) (T, error) {
	return decodeCompressed(c.next, data, func(r io.Reader) (io.ReadCloser, error) {
		return stdgzip.NewReader(r)
	})
}
