// Package binary provides a Codec[T] for compact, caller-defined binary payloads
// for the cas core. The package is generic and object-agnostic: it does not
// encode any application-specific object model such as Blob or Tree. Instead,
// the caller provides the exact encode and decode functions for the value type
// they want to store in a CAS object.
//
// It is intended for durable, stdlib-only, compact payloads where JSON would be
// too verbose and a general-purpose format such as gob would be too Go-specific
// or too compatibility-limited. The caller remains responsible for choosing a
// stable per-type binary layout and a versioning strategy.
package binary

import (
	"errors"

	"github.com/dmundt/go-cask/cas"
)

// Codec[T] serializes values as a codec stack: the inner codec owns the value
// representation, and the binary layer may wrap those bytes with an explicit
// binary transport transformation. This keeps the stack model uniform across the
// codec family while still allowing an app-defined raw binary format when the
// inner codec is nil.
type Codec[T any] struct {
	next   cas.Codec[T]
	transform func([]byte) ([]byte, error)
	restore   func([]byte) ([]byte, error)
	encode func(T) ([]byte, error)
	decode func([]byte) (T, error)
}

var (
	errNilEncode = errors.New("binarycodec: encode is nil")
	errNilDecode = errors.New("binarycodec: decode is nil")
)

// New builds a binary codec stack around an inner codec. The inner codec
// serializes the value; the binary wrapper can then transform those bytes to a
// custom binary representation. When no inner codec is supplied, New behaves as
// a direct raw custom-binary codec.
func New[T any](next cas.Codec[T], transform func([]byte) ([]byte, error), restore func([]byte) ([]byte, error)) Codec[T] {
	return Codec[T]{next: next, transform: transform, restore: restore}
}

// NewRaw returns a direct custom-binary codec for T without an inner codec.
func NewRaw[T any](encode func(T) ([]byte, error), decode func([]byte) (T, error)) Codec[T] {
	return Codec[T]{encode: encode, decode: decode}
}

// Encode executes the stack: if an inner codec is present, it serializes the
// value through that codec first and then passes the result through the binary
// transform. Otherwise it uses the raw custom binary encoder.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	if c.next != nil {
		if c.transform == nil {
			return nil, errNilEncode
		}
		payload, err := c.next.Encode(v)
		if err != nil {
			return nil, err
		}
		return c.transform(payload)
	}
	if c.encode == nil {
		return nil, errNilEncode
	}
	return c.encode(v)
}

// Decode reverses the stack by restoring the inner payload and then decoding
// through the inner codec when one is present.
func (c Codec[T]) Decode(data []byte) (T, error) {
	var zero T
	if c.next != nil {
		if c.restore == nil {
			return zero, errNilDecode
		}
		payload, err := c.restore(data)
		if err != nil {
			return zero, err
		}
		return c.next.Decode(payload)
	}
	if c.decode == nil {
		return zero, errNilDecode
	}
	return c.decode(data)
}
