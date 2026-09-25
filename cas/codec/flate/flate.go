// Package flate implements a codec that wraps another codec and applies the
// standard library's compress/flate format to the encoded bytes.
//
// The flate codec is a representation-layer optimization: it compresses
// serialized payloads without changing object identity or store semantics.
package flate

import (
	"compress/flate"
	"io"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/codec/internal/bounded"
)

// Codec[T] wraps a base codec and compresses bytes with flate before storing or
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

// New returns a flate-compressing codec for type T.
func New[T any](next cas.Codec[T]) Codec[T] {
	return Codec[T]{next: next}
}

// Encode serializes v with the wrapped codec and then flate-compresses the
// result.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	return bounded.Encode(c.next, v, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})
}

// Decode inflates the incoming data and then decodes it with the wrapped
// codec. A payload that inflates past MaxDecodedBytes is rejected with
// ErrDecodedTooLarge.
func (c Codec[T]) Decode(data []byte) (T, error) {
	return bounded.Decode(c.next, data, MaxDecodedBytes, func(r io.Reader) (io.ReadCloser, error) {
		return flate.NewReader(r), nil
	})
}

// CodecName reports the codec identity tag written into the envelope:
// "flate+<inner tag>" — "flate+json" for flate.New(json.New[T]()) — because the
// stored bytes are flate-compressed inner bytes, not inner bytes. A wrapped
// codec that declares no tag yields "" (unspecified), so nesting an unnamed
// codec never manufactures a tag that later reads as a mismatch. It satisfies
// cas.CodecNamer.
func (c Codec[T]) CodecName() string { return bounded.ComposeTag("flate", c.next) }
