// Package codec provides concrete Codec[T] implementations for the cas core:
// JSONCodec (human-readable, default) and GobCodec (compact binary). The
// Codec[T] interface itself lives in package cas (cas.Core).
package codec

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
)

// JSONCodec[T] is the default Codec[T]: encoding/json Marshal/Unmarshal.
type JSONCodec[T any] struct{}

// Encode marshals v to JSON.
func (JSONCodec[T]) Encode(v T) ([]byte, error) { return json.Marshal(v) }

// Decode unmarshals JSON into a fresh T.
func (JSONCodec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}

// GobCodec[T] serializes with encoding/gob: compact binary output, no field
// names. Both producer and consumer must be Go programs using the same type.
type GobCodec[T any] struct{}

// Encode gob-encodes v.
func (GobCodec[T]) Encode(v T) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode gob-decodes data into a fresh T.
func (GobCodec[T]) Decode(data []byte) (T, error) {
	var v T
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return v, err
	}
	return v, nil
}
