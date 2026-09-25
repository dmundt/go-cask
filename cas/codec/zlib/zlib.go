// Package zlib implements a codec that wraps another codec and applies the
// standard library's compress/zlib format to the encoded bytes.
//
// The zlib codec is a representation-layer optimization: it compresses
// serialized payloads without changing object identity or store semantics.
package zlib

import (
	"compress/zlib"
	"io"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/codec/internal/bounded"
)

// Codec[T] wraps a base codec and compresses bytes with zlib before storing or
// after reading them back.
type Codec[T any] struct {
	next cas.Codec[T]
}

// MaxDecodedBytes bounds how many bytes a single Decode call will decompress.
// The compressed payload is stored data, and a small one can expand without
// limit (a compression bomb), so the read stops at this ceiling instead of
// allocating until the machine gives up. A caller whose legitimate payload
// exceeds it needs a codec that streams rather than this decompress-to-memory
// layer.
const MaxDecodedBytes = bounded.MaxDecodedBytes

// ErrDecodedTooLarge reports a payload whose decompressed form exceeds
// MaxDecodedBytes. The condition belongs to the encoded bytes, not to the
// wrapped codec, so the wrapped codec is never asked to decode them. flate,
// gzip and zlib share this one value, so errors.Is holds whichever of the three
// read the payload.
var ErrDecodedTooLarge = bounded.ErrDecodedTooLarge

// New returns a zlib-compressing codec for type T.
func New[T any](next cas.Codec[T]) Codec[T] {
	return Codec[T]{next: next}
}

// Encode serializes v with the wrapped codec and then zlib-compresses the
// result.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	return bounded.Encode(c.next, v, func(w io.Writer) (io.WriteCloser, error) {
		return zlib.NewWriter(w), nil
	})
}

// Decode inflates the incoming data and then decodes it with the wrapped
// codec. A payload that inflates past MaxDecodedBytes is rejected with
// ErrDecodedTooLarge.
func (c Codec[T]) Decode(data []byte) (T, error) {
	return bounded.Decode(c.next, data, MaxDecodedBytes, func(r io.Reader) (io.ReadCloser, error) {
		return zlib.NewReader(r)
	})
}

// CodecName reports the codec identity tag written into the envelope:
// "zlib+<inner tag>" — "zlib+json" for zlib.New(json.New[T]()) — because the
// stored bytes are zlib-compressed inner bytes, not inner bytes. A wrapped
// codec that declares no tag yields "" (unspecified), so nesting an unnamed
// codec never manufactures a tag that later reads as a mismatch. It satisfies
// cas.CodecNamer.
func (c Codec[T]) CodecName() string { return bounded.ComposeTag("zlib", c.next) }
