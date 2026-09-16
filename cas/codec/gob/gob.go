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
// contract, Decode(Encode(v)) == v.
package gob

import (
	"bytes"
	"encoding/gob"

	"github.com/dmundt/go-cask/cas"
)

// Codec[T] serializes values with encoding/gob. If a wrapped codec is supplied,
// the value is serialized through that codec first and then gob-encoded as a
// transport layer. This keeps the shape of the object graph unchanged while
// allowing codec stacks to be composed transparently.
type Codec[T any] struct {
	next cas.Codec[T]
}

// New returns a gob codec for type T. When a wrapped codec is supplied, gob
// becomes the outermost layer in a cascade.
func New[T any](next ...cas.Codec[T]) Codec[T] {
	var wrapped cas.Codec[T]
	if len(next) > 0 {
		wrapped = next[0]
	}
	return Codec[T]{next: wrapped}
}

// Encode gob-encodes v directly or, when wrapped, the bytes produced by the
// inner codec.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	if c.next != nil {
		payload, err := c.next.Encode(v)
		if err != nil {
			return nil, err
		}
		buf := bytes.NewBuffer(make([]byte, 0, len(payload)+64))
		if err := gob.NewEncoder(buf).Encode(payload); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	buf := bytes.NewBuffer(make([]byte, 0, 64))
	if err := gob.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode gob-decodes data into a fresh T, or into the wrapped payload bytes
// when the codec is composed with an inner layer.
func (c Codec[T]) Decode(data []byte) (T, error) {
	var zero T
	if c.next != nil {
		var payload []byte
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&payload); err != nil {
			return zero, err
		}
		return c.next.Decode(payload)
	}

	var v T
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}
