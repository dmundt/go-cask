// Package json provides a Codec[T] for the cas core that serializes typed
// values with the standard library's encoding/json (cas-core §4.6). It is the
// default codec.
//
// New[T]() returns a codec for any storable value T, so a store can be built
// directly: cas.New(raw, json.New[T](), algo). It satisfies the codec
// round-trip contract, Unmarshal(Marshal(v)) == v, for all values with valid
// UTF-8 content (encoding/json replaces invalid UTF-8 on encode, which is
// pinned by tests).
//
// The package also owns the JSON shape of a hash: Hash is the field type an
// object type declares for a reference (NewHash to wrap, Hash to unwrap). The
// core's cas.Hash deliberately carries no serialization, so this codec — the
// one that defines a wire format — is the only place a hash is rendered as text
// and validated on the way back in (cas-core §4.2, §4.6).
package json

import "encoding/json"

// Codec[T] serializes values with encoding/json Marshal/Unmarshal.
type Codec[T any] struct{}

// New returns a JSON codec for type T.
func New[T any]() Codec[T] { return Codec[T]{} }

// Marshal marshals v to JSON.
func (Codec[T]) Marshal(v T) ([]byte, error) { return json.Marshal(v) }

// Unmarshal unmarshals JSON into a fresh T.
func (Codec[T]) Unmarshal(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}
