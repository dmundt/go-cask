// Package gob provides a binary Codec[T] (encoding/gob) for the cas core.
// Both producer and consumer must be Go programs using the same type.
package gob

import (
	"bytes"
	"encoding/gob"
)

// Codec[T] serializes values with encoding/gob: compact binary output.
type Codec[T any] struct{}

// New returns a gob codec for type T.
func New[T any]() Codec[T] { return Codec[T]{} }

// Marshal gob-encodes v.
func (Codec[T]) Marshal(v T) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal gob-decodes data into a fresh T.
func (Codec[T]) Unmarshal(data []byte) (T, error) {
	var v T
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}
