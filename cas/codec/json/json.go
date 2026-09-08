// Package json provides a JSON Codec[T] for the cas core.
package json

import "encoding/json"

// Codec[T] serializes values with encoding/json Marshal/Unmarshal.
type Codec[T any] struct{}

// New returns a JSON codec for type T.
func New[T any]() Codec[T] { return Codec[T]{} }

// Encode marshals v to JSON.
func (Codec[T]) Encode(v T) ([]byte, error) { return json.Marshal(v) }

// Decode unmarshals JSON into a fresh T.
func (Codec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}
