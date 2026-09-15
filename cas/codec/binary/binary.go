// Package binary provides a Codec[T] for compact, caller-defined binary payloads
// for the cas core. The package is generic and object-agnostic: it does not
// encode any application-specific object model such as Blob or Tree. Instead,
// the caller provides the exact marshal and unmarshal functions for the value
// type they want to store in a CAS object.
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
	next      cas.Codec[T]
	wrap      func([]byte) ([]byte, error)
	unwrap    func([]byte) ([]byte, error)
	marshal   func(T) ([]byte, error)
	unmarshal func([]byte) (T, error)
}

var (
	errNilMarshal   = errors.New("binarycodec: marshal is nil")
	errNilUnmarshal = errors.New("binarycodec: unmarshal is nil")
)

// New builds a binary codec stack around an inner codec. The inner codec
// serializes the value; the binary wrapper can then transform those bytes to a
// custom binary representation. When no inner codec is supplied, New behaves as
// a direct raw custom-binary codec.
func New[T any](next cas.Codec[T], wrap func([]byte) ([]byte, error), unwrap func([]byte) ([]byte, error)) Codec[T] {
	return Codec[T]{next: next, wrap: wrap, unwrap: unwrap}
}

// NewRaw returns a direct custom-binary codec for T without an inner codec.
func NewRaw[T any](marshal func(T) ([]byte, error), unmarshal func([]byte) (T, error)) Codec[T] {
	return Codec[T]{marshal: marshal, unmarshal: unmarshal}
}

// Marshal executes the stack: if an inner codec is present, it serializes the
// value through that codec first and then passes the result through the binary
// transform. Otherwise it uses the raw custom binary marshaller.
func (c Codec[T]) Marshal(v T) ([]byte, error) {
	if c.next != nil {
		if c.wrap == nil {
			return nil, errNilMarshal
		}
		payload, err := c.next.Marshal(v)
		if err != nil {
			return nil, err
		}
		return c.wrap(payload)
	}
	if c.marshal == nil {
		return nil, errNilMarshal
	}
	return c.marshal(v)
}

// Unmarshal reverses the stack by restoring the inner payload and then decoding
// through the inner codec when one is present.
func (c Codec[T]) Unmarshal(data []byte) (T, error) {
	var zero T
	if c.next != nil {
		if c.unwrap == nil {
			return zero, errNilUnmarshal
		}
		payload, err := c.unwrap(data)
		if err != nil {
			return zero, err
		}
		return c.next.Unmarshal(payload)
	}
	if c.unmarshal == nil {
		return zero, errNilUnmarshal
	}
	return c.unmarshal(data)
}
