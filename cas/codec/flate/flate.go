// Package flate provides a Codec[T] that wraps another codec and compresses its
// serialized bytes with the standard library's compress/flate package.
package flate

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// Codec[T] wraps a base codec and compresses bytes with flate before storing or
// after reading them back.
type Codec[T any] struct {
	next cas.Codec[T]
}

var errNilCodec = errors.New("flatecodec: next codec is nil")

// New returns a flate-compressing codec for type T.
func New[T any](next cas.Codec[T]) Codec[T] {
	return Codec[T]{next: next}
}

// Encode serializes v with the wrapped codec and then flate-compresses the
// result.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	if c.next == nil {
		return nil, errNilCodec
	}

	payload, err := c.next.Encode(v)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
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

// Decode inflates the incoming data and then decodes it with the wrapped
// codec.
func (c Codec[T]) Decode(data []byte) (T, error) {
	var zero T
	if c.next == nil {
		return zero, errNilCodec
	}

	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()

	payload, err := io.ReadAll(r)
	if err != nil {
		return zero, err
	}
	return c.next.Decode(payload)
}
