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

import "errors"

// Codec[T] serializes values with caller-supplied binary marshal/unmarshal
// functions. It is intentionally object-agnostic: callers define the actual
// binary layout for each type they store.
type Codec[T any] struct {
	marshal   func(T) ([]byte, error)
	unmarshal func([]byte) (T, error)
}

var (
	errNilMarshal   = errors.New("binarycodec: marshal is nil")
	errNilUnmarshal = errors.New("binarycodec: unmarshal is nil")
)

// New returns a binary codec for type T using the provided marshal and
// unmarshal functions.
func New[T any](marshal func(T) ([]byte, error), unmarshal func([]byte) (T, error)) Codec[T] {
	return Codec[T]{marshal: marshal, unmarshal: unmarshal}
}

// Marshal calls the caller's marshal function.
func (c Codec[T]) Marshal(v T) ([]byte, error) {
	if c.marshal == nil {
		return nil, errNilMarshal
	}
	return c.marshal(v)
}

// Unmarshal calls the caller's unmarshal function.
func (c Codec[T]) Unmarshal(data []byte) (T, error) {
	var zero T
	if c.unmarshal == nil {
		return zero, errNilUnmarshal
	}
	return c.unmarshal(data)
}
