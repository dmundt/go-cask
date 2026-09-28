// Package pack provides small helper functions for payload chunking and
// manifest encoding. It is a helper layer, not a storage backend and not a
// codec; the content-addressed byte model remains in the cas core.
//
// The codec is always the caller's, and this package imports none: every entry
// point takes one (EncodeWith, DecodeWith, LoadWith, SaveWith, New) and reports
// ErrNilCodec rather than substituting a format of its own. A caller that wants
// JSON passes json.New[T]() at the call site, where the format is visible.
package pack

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/internal/atomicfile"
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
// renamed over the destination, with the destination directory fsynced too —
// so a crash or a full disk mid-write leaves the previous manifest intact
// instead of a truncated file that no longer decodes, and the rename itself
// survives a crash. The context is checked before the directory is created,
// before the temp file is written and before the rename.
//
// The sequence is atomicfile.Publish (go-cask#339), the one publish
// cas/backend/fs, cas/refs and this package share. This package used to
// hand-roll it and skip the parent-directory fsync, which made a manifest
// rename the one publish a crash could lose; taking the shared publish takes
// its durability rule. The error names the manifest and the phase that failed
// ("pack: write manifest /x/meta.json: create temp file: open …: is a
// directory"), so no per-phase mapping is kept here.
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
	if err := atomicfile.Publish(ctx, path, bytes.NewReader(b), atomicfile.Options{
		Mode:    0o644,
		SyncDir: true,
	}); err != nil {
		return fmt.Errorf("pack: write manifest %s: %w", path, err)
	}
	return nil
}
