// Package flate implements a codec that wraps another codec and applies the
// standard library's compress/flate format to the encoded bytes.
//
// The flate codec is a representation-layer optimization: it compresses
// serialized payloads without changing object identity or store semantics.
//
// A Codec[T] is safe for concurrent use, and so is a stack of them: the
// compressor and the decompressor are process-wide pool scratch rather than
// fields of the codec, taken per call and Reset onto that call's destination
// (go-cask#378). Pooling changes no bytes — a reset writer is a fresh writer —
// so a pooled codec writes exactly what a newly built one writes and reads
// every stream the unpooled code wrote.
package flate

import (
	"compress/flate"
	"io"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/codec/internal/bounded"
)

// writers and readers pool the stdlib compressor and decompressor: this is the
// shipped default compression wrapper, and a flate writer carries roughly a
// megabyte of match tables, so building one per call spent that to store a
// 64-byte object, and building one reader per call spent tens of kilobytes to
// inflate it. A pooled writer is handed back only by a call that closed it
// cleanly, and a pooled reader only by a call that read its stream to the end,
// so neither ever carries a failed stream into the next call.
var (
	writers = bounded.NewWriterPool(func(w io.Writer) (bounded.Compressor, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})
	readers = bounded.NewReaderPool(func(r io.Reader) (bounded.Decompressor, error) {
		fr := flate.NewReader(r)
		// flate.Resetter is the contract of the reader flate.NewReader returns,
		// so the assertion cannot fail; TestStdlibReadersAreResetters fails long
		// before a standard-library change could turn it into a panic here.
		return decompressor{ReadCloser: fr, resetter: fr.(flate.Resetter)}, nil
	})
)

// decompressor adapts a compress/flate reader to the pool's Decompressor
// contract: compress/flate's Reset takes a preset dictionary, and this codec
// never writes one, so the adapter supplies nil — the same call flate.NewReader
// itself makes.
type decompressor struct {
	io.ReadCloser
	resetter flate.Resetter
}

func (d decompressor) Reset(r io.Reader) error { return d.resetter.Reset(r, nil) }

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
// result with a pooled writer, Reset onto this call's buffer.
func (c Codec[T]) Encode(v T) ([]byte, error) {
	return bounded.Encode(c.next, v, writers)
}

// Decode inflates the incoming data with a pooled reader and then decodes it
// with the wrapped codec. A payload that inflates past MaxDecodedBytes is
// rejected with ErrDecodedTooLarge.
func (c Codec[T]) Decode(data []byte) (T, error) {
	return bounded.Decode(c.next, data, MaxDecodedBytes, readers)
}

// CodecName reports the codec identity tag written into the envelope:
// "flate+<inner tag>" — "flate+json" for flate.New(json.New[T]()) — because the
// stored bytes are flate-compressed inner bytes, not inner bytes. A wrapped
// codec that declares no tag yields "" (unspecified), so nesting an unnamed
// codec never manufactures a tag that later reads as a mismatch. It satisfies
// cas.CodecNamer.
func (c Codec[T]) CodecName() string { return bounded.ComposeTag("flate", c.next) }
