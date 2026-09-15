// Package manifest provides a lightweight sidecar metadata layer for CAS
// workflows. It stays codec-agnostic by reusing the core cas.Codec[T]
// contract rather than inventing a duplicate interface.
package manifest

import (
	"os"
	"path/filepath"

	"github.com/dmundt/go-cask/cas"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// Codec[T] is the same codec contract as the core cas package. Callers may pass
// any compatible codec implementation; the package default is the JSON codec.
type Codec[T any] = cas.Codec[T]

// Data is a simple sidecar metadata map keyed by string names.
type Data map[string]string

// Encode returns the default JSON encoding for the metadata map.
func Encode(m Data) ([]byte, error) {
	return EncodeWith(m, jsoncodec.New[Data]())
}

// Decode parses the default JSON encoding for the metadata map.
func Decode(b []byte) (Data, error) {
	return DecodeWith(b, jsoncodec.New[Data]())
}

// EncodeWith serializes a typed value with a caller-supplied codec.
func EncodeWith[T any](v T, c Codec[T]) ([]byte, error) {
	if c == nil {
		c = jsoncodec.New[T]()
	}
	return c.Marshal(v)
}

// DecodeWith deserializes data with a caller-supplied codec.
func DecodeWith[T any](b []byte, c Codec[T]) (T, error) {
	if c == nil {
		c = jsoncodec.New[T]()
	}
	return c.Unmarshal(b)
}

// Store provides a path-bound manifest with a caller-selected codec.
type Store[T any] struct {
	path  string
	codec Codec[T]
}

// New creates a path-bound manifest store for type T. If codec is nil, the
// package default JSON codec is used.
func New[T any](path string, codec Codec[T]) *Store[T] {
	if codec == nil {
		codec = jsoncodec.New[T]()
	}
	return &Store[T]{path: path, codec: codec}
}

// Load reads a manifest from disk using the store's codec.
func (s *Store[T]) Load() (T, error) {
	return LoadWith(s.path, s.codec)
}

// Save writes a manifest to disk using the store's codec.
func (s *Store[T]) Save(v T) error {
	return SaveWith(s.path, v, s.codec)
}

// LoadWith reads a manifest file with the supplied codec.
func LoadWith[T any](path string, codec Codec[T]) (T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		var zero T
		return zero, err
	}
	return DecodeWith(b, codec)
}

// SaveWith writes a manifest file with the supplied codec.
func SaveWith[T any](path string, v T, codec Codec[T]) error {
	b, err := EncodeWith(v, codec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Load reads a metadata map from disk using the default JSON codec.
func Load(path string) (Data, error) {
	return LoadWith(path, jsoncodec.New[Data]())
}

// Save writes a metadata map to disk using the default JSON codec.
func Save(path string, m Data) error {
	return SaveWith(path, m, jsoncodec.New[Data]())
}
