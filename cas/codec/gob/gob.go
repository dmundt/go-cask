// Package gob provides a Codec[T] for the cas core that serializes typed
// values with the standard library's encoding/gob (cas-core §4.6).
//
// This is an opt-in compatibility codec for Go-only workflows. It is not the
// recommended format for durable or cross-language CAS storage: both producer
// and consumer must be Go programs using the same type, and the format is
// neither a long-term archive format nor a stable external wire format.
//
// New[T]() returns a codec for any storable value T, so a store can be built
// directly: cas.New(raw, gob.New[T](), algo). It satisfies the codec round-trip
// contract, Unmarshal(Marshal(v)) == v.
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
