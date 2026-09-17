package pack

import (
	"bytes"
	"os"
	"path/filepath"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas"
)

// Codec is the shared codec contract used by the pack layer.
type Codec[T any] = cas.Codec[T]

// Data is a simple sidecar metadata map keyed by string names.
type Data map[string]string

// Split breaks data into fixed-size chunks. The final chunk may be shorter.
func Split(data []byte, size int) [][]byte {
	if size <= 0 {
		return [][]byte{append([]byte(nil), data...)}
	}
	if len(data) == 0 {
		return nil
	}
	count := (len(data) + size - 1) / size
	out := make([][]byte, 0, count)
	for i := 0; i < len(data); i += size {
		end := i + size
		if end > len(data) {
			end = len(data)
		}
		out = append(out, append([]byte(nil), data[i:end]...))
	}
	return out
}

// Join reassembles chunk data back into one byte slice.
func Join(chunks [][]byte) []byte {
	if len(chunks) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(c)
	}
	return buf.Bytes()
}

// Count returns the number of chunks produced for a payload of size bytes.
func Count(size, chunkSize int) int {
	if chunkSize <= 0 {
		return 1
	}
	if size <= 0 {
		return 0
	}
	return (size + chunkSize - 1) / chunkSize
}

func defaultCodec[T any]() Codec[T] { return jsoncodec.New[T]() }

// Encode serializes a manifest map with the default JSON codec.
func Encode(v Data) ([]byte, error) { return EncodeWith(v, defaultCodec[Data]()) }

// Decode deserializes a manifest map with the default JSON codec.
func Decode(b []byte) (Data, error) { return DecodeWith(b, defaultCodec[Data]()) }

// EncodeWith serializes a typed value with a caller-supplied codec.
func EncodeWith[T any](v T, c Codec[T]) ([]byte, error) {
	if c == nil {
		c = defaultCodec[T]()
	}
	return c.Encode(v)
}

// DecodeWith deserializes data with a caller-supplied codec.
func DecodeWith[T any](b []byte, c Codec[T]) (T, error) {
	if c == nil {
		c = defaultCodec[T]()
	}
	return c.Decode(b)
}

// Store provides a path-bound manifest with a caller-selected codec.
type Store[T any] struct {
	path  string
	codec Codec[T]
}

// New creates a path-bound manifest store for type T. If codec is nil, the
// default JSON codec is used.
func New[T any](path string, codec Codec[T]) *Store[T] {
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

// Load reads a manifest from disk using the default JSON codec.
func Load(path string) (Data, error) { return LoadWith(path, defaultCodec[Data]()) }

// Save writes a manifest to disk using the default JSON codec.
func Save(path string, m Data) error { return SaveWith(path, m, defaultCodec[Data]()) }

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
