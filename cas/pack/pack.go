// Package pack provides small helper functions for payload chunking and
// manifest encoding. It is a helper layer, not a storage backend and not a
// codec; the content-addressed byte model remains in the cas core.
//
// The codec is always the caller's: no function here substitutes one when the
// caller passes nil, because the codec decides what is written on disk. The JSON
// convenience lives behind the names that say so (SaveJSON, LoadJSON,
// EncodeJSON, DecodeJSON).
package pack

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/dmundt/go-cask/cas"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// Codec is the shared codec contract used by the pack layer.
type Codec[T any] = cas.Codec[T]

// Data is a simple sidecar metadata map keyed by string names.
type Data map[string]string

// Split breaks data into fixed-size chunks. The final chunk may be shorter.
func Split(data []byte, size int) [][]byte {
	if size <= 0 {
		return [][]byte{slices.Clone(data)}
	}
	if len(data) == 0 {
		return nil
	}
	count := (len(data) + size - 1) / size
	out := make([][]byte, 0, count)
	for i := 0; i < len(data); i += size {
		end := min(i+size, len(data))
		out = append(out, slices.Clone(data[i:end]))
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

// ErrNilCodec reports a call that did not supply the codec the pack layer
// writes with. The codec decides the on-disk format, so there is no safe
// default to fall back to: a missing one is a caller mistake, not a mode.
var ErrNilCodec = fmt.Errorf("pack: nil codec")

// EncodeJSON serializes a manifest map with the JSON codec, which the function
// name makes explicit.
func EncodeJSON(v Data) ([]byte, error) { return EncodeWith(v, jsoncodec.New[Data]()) }

// DecodeJSON deserializes a manifest map with the JSON codec, which the
// function name makes explicit.
func DecodeJSON(b []byte) (Data, error) { return DecodeWith(b, jsoncodec.New[Data]()) }

// EncodeWith serializes a typed value with the caller's codec. A nil codec is
// ErrNilCodec: encoding with a codec the caller did not choose would make the
// package decide what is stored.
func EncodeWith[T any](v T, c Codec[T]) ([]byte, error) {
	if c == nil {
		return nil, ErrNilCodec
	}
	return c.Encode(v)
}

// DecodeWith deserializes data with the caller's codec. A nil codec is
// ErrNilCodec, for the same reason as EncodeWith.
func DecodeWith[T any](b []byte, c Codec[T]) (T, error) {
	var zero T
	if c == nil {
		return zero, ErrNilCodec
	}
	return c.Decode(b)
}

// Store provides a path-bound manifest with a caller-selected codec.
type Store[T any] struct {
	path  string
	codec Codec[T]
}

// New creates a path-bound manifest store for type T. The codec is required:
// a nil one is ErrNilCodec rather than a silent switch to JSON.
func New[T any](path string, codec Codec[T]) (*Store[T], error) {
	if codec == nil {
		return nil, ErrNilCodec
	}
	return &Store[T]{path: path, codec: codec}, nil
}

// Load reads the store's manifest with the store's codec.
func (s *Store[T]) Load(ctx context.Context) (T, error) {
	return LoadWith(ctx, s.path, s.codec)
}

// Save writes the store's manifest with the store's codec.
func (s *Store[T]) Save(ctx context.Context, v T) error {
	return SaveWith(ctx, s.path, v, s.codec)
}

// LoadJSON reads a manifest with the JSON codec, which the function name makes
// explicit (this is the convenience the old context-less Load provided).
func LoadJSON[T any](ctx context.Context, path string) (T, error) {
	return LoadWith(ctx, path, jsoncodec.New[T]())
}

// SaveJSON writes a manifest with the JSON codec, which the function name makes
// explicit (this is the convenience the old context-less Save provided).
func SaveJSON[T any](ctx context.Context, path string, v T) error {
	return SaveWith(ctx, path, v, jsoncodec.New[T]())
}

// LoadWith reads a manifest file with the supplied codec. The context is
// checked before the read starts; a cancelled context reports context.Canceled
// rather than opening the file.
func LoadWith[T any](ctx context.Context, path string, codec Codec[T]) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if codec == nil {
		return zero, ErrNilCodec
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, fmt.Errorf("pack: read manifest %s: %w", path, err)
	}
	return codec.Decode(b)
}

// SaveWith writes a manifest file with the supplied codec.
//
// The write is atomic — a temp file in the target directory, fsynced, then
// renamed — so a crash or a full disk mid-write leaves the previous manifest
// intact instead of a truncated file that no longer decodes. The context is
// checked before the directory is created, before the temp file is written and
// before the rename.
func SaveWith[T any](ctx context.Context, path string, v T, codec Codec[T]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if codec == nil {
		return ErrNilCodec
	}
	b, err := codec.Encode(v)
	if err != nil {
		return err
	}
	return writeFileAtomic(ctx, path, b, defaultFileOps())
}

// tempFile is the write side of the temp file writeFileAtomic publishes: the
// surface it uses, so a test can make any single step fail. An *os.File
// satisfies it.
type tempFile interface {
	Name() string
	Write(p []byte) (int, error)
	Chmod(mode os.FileMode) error
	Sync() error
	Close() error
}

// fileOps is the filesystem seam of writeFileAtomic. Production always uses
// defaultFileOps; a test injects a failure per branch, the same way
// cas/bloom/persistent injects its mmap driver rather than leaving the error
// paths to a real disk that will not fail on demand.
type fileOps struct {
	createTemp func(dir, pattern string) (tempFile, error)
	remove     func(name string) error
	rename     func(oldpath, newpath string) error
}

// defaultFileOps returns the real filesystem operations.
func defaultFileOps() fileOps {
	return fileOps{
		createTemp: func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) },
		remove:     os.Remove,
		rename:     os.Rename,
	}
}

// writeFileAtomic publishes data at path through a temp file in the same
// directory, so the rename that makes it visible is the only step a reader can
// observe. Every failure removes the temp file: a half-written manifest is never
// published and never left behind as scratch.
func writeFileAtomic(ctx context.Context, path string, data []byte, ops fileOps) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pack: create manifest directory: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := ops.createTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("pack: create manifest temp: %w", err)
	}
	tmp := f.Name()
	discard := func() {
		_ = f.Close()
		_ = ops.remove(tmp)
	}
	if _, err := f.Write(data); err != nil {
		discard()
		return fmt.Errorf("pack: write manifest: %w", err)
	}
	if err := f.Chmod(0o644); err != nil {
		discard()
		return fmt.Errorf("pack: set manifest permissions: %w", err)
	}
	if err := f.Sync(); err != nil {
		discard()
		return fmt.Errorf("pack: sync manifest: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = ops.remove(tmp)
		return fmt.Errorf("pack: close manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = ops.remove(tmp)
		return err
	}
	if err := ops.rename(tmp, path); err != nil {
		_ = ops.remove(tmp)
		return fmt.Errorf("pack: publish manifest: %w", err)
	}
	return nil
}
