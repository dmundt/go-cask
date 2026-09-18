// Package json provides a Codec[T] for the cas core that serializes typed
// values with the standard library's encoding/json (cas-core §4.6). It is the
// default codec.
//
// New[T]() returns a codec for any storable value T, so a store can be built
// directly: cas.New(raw, json.New[T](), hasher). It satisfies the codec
// round-trip contract, Decode(Encode(v)) == v, for all values with valid
// UTF-8 content (encoding/json replaces invalid UTF-8 on encode, which is
// pinned by tests).
//
// References need no code here: a cas.Digest field renders itself as one
// lowercase-hex string through encoding.TextMarshaler, which encoding/json
// honors, and `omitzero` drops an absent optional reference (cas-core §4.2,
// §4.6).
package json

import "encoding/json"

// Codec[T] serializes values with encoding/json Encode/Decode semantics.
type Codec[T any] struct{}

// New returns a JSON codec for type T.
func New[T any]() Codec[T] { return Codec[T]{} }

// Encode encodes v to JSON.
func (Codec[T]) Encode(v T) ([]byte, error) { return json.Marshal(v) }

// Decode decodes JSON into a fresh T.
func (Codec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}
