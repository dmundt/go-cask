// Package msgpack provides a MessagePack Codec[T] for the cas core.
// Uses github.com/vmihailenco/msgpack/v5 for encode/decode.
package msgpack

import (
	"github.com/vmihailenco/msgpack/v5"
)

// Codec[T] serializes values with MessagePack: compact binary, cross-language.
type Codec[T any] struct{}

// New returns a msgpack codec for type T.
func New[T any]() Codec[T] { return Codec[T]{} }

// Encode marshals v to MessagePack.
func (Codec[T]) Encode(v T) ([]byte, error) {
	return msgpack.Marshal(v)
}

// Decode unmarshals MessagePack into a fresh T.
func (Codec[T]) Decode(b []byte) (T, error) {
	var v T
	err := msgpack.Unmarshal(b, &v)
	return v, err
}
