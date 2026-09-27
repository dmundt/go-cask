// Package bounded holds the codec-stack internals the cas/codec wrappers share:
// the bounded decompression body the flate, gzip and zlib codecs use, and the
// identity-tag compositor every stacking codec names itself through.
//
// It is internal to cas/codec: those wrappers are the public surface
// (cas-core §4.6), and each keeps its own `Codec[T]`, `New`, `MaxDecodedBytes`
// and `ErrDecodedTooLarge`. What they must not keep is three copies of the same
// bounded read — the stored bytes are untrusted, and a small compressed payload
// can expand without limit, so the ceiling, the one-byte-over probe and the
// rule that the wrapped codec is never handed bytes this layer refused are
// written once here. ComposeTag is here for the same reason: binary, gob, flate,
// gzip and zlib all derive their tag from the codec they wrap, so that rule has
// one implementation too.
//
// The compressor and the decompressor are as shared as the read is: a
// flate-family writer carries roughly a megabyte of match tables, so `Encode`
// takes a writer from a `WriterPool` and `Decode` a reader from a `ReaderPool`
// rather than building either per call (go-cask#378). Each wrapper declares one
// pool of each kind and passes it in; `pool.go` holds the machinery and the
// concurrency contract.
package bounded

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// MaxDecodedBytes bounds how many bytes a single Decode call will decompress:
// the ceiling each compression wrapper exports as its own constant.
const MaxDecodedBytes = 1 << 30

// ErrNilCodec reports a wrapping codec constructed without an inner codec.
var ErrNilCodec = errors.New("cas/codec: next codec is nil")

// ErrDecodedTooLarge reports a payload whose decompressed form exceeds the
// ceiling. It is one value that flate, gzip and zlib each export under their
// own name, so a caller's errors.Is check holds whichever of the three it read
// the payload through.
var ErrDecodedTooLarge = fmt.Errorf("cas/codec: decoded payload exceeds %d bytes", MaxDecodedBytes)

// Encode serializes v with next and then compresses the result with a writer
// taken from writers.
func Encode[T any](next cas.Codec[T], v T, writers *WriterPool) ([]byte, error) {
	if next == nil {
		return nil, ErrNilCodec
	}

	payload, err := next.Encode(v)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Grow(len(payload) + len(payload)/8 + 64)
	w, err := writers.get(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(payload); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	// Only a writer that closed cleanly goes back to the pool: one that failed
	// mid-stream is dropped instead, so the next call cannot inherit its broken
	// state.
	writers.put(w)
	return buf.Bytes(), nil
}

// Decode decompresses data and hands the bytes to next. maxDecoded bounds the
// inflated payload: the stored bytes are untrusted, and a small compressed
// payload can expand without limit, so the read is capped rather than trusting
// the stream. It is a parameter rather than the constant directly so a test can
// exercise the ceiling without inflating a gigabyte.
func Decode[T any](next cas.Codec[T], data []byte, maxDecoded int64, readers *ReaderPool) (T, error) {
	var zero T
	if next == nil {
		return zero, ErrNilCodec
	}

	r, err := readers.get(bytes.NewReader(data))
	if err != nil {
		return zero, err
	}

	// The ceiling applies to the decompressed stream, not to data: a small
	// compressed payload can expand far beyond its own size. LimitReader stops
	// the decompressor one byte past the ceiling, so the check below can tell
	// "exactly at the limit" from "over it".
	payload, err := io.ReadAll(io.LimitReader(r, maxDecoded+1))
	if err != nil {
		// A reader that failed mid-stream is closed and dropped, never parked.
		_ = r.Close()
		return zero, err
	}
	if int64(len(payload)) > maxDecoded {
		_ = r.Close()
		return zero, ErrDecodedTooLarge
	}
	// Close stays best-effort, as it was before pooling: the read reached the
	// end of the stream, which is where the format's own checksum is validated.
	_ = r.Close()
	readers.put(r)
	return next.Decode(payload)
}

// ComposeTag builds the identity tag of a codec stacked over next:
// "<name>+<inner tag>". It reports "" when next declares no tag, so an unnamed
// inner codec leaves the stack unspecified rather than manufacturing a tag that
// would later read as a mismatch. It is the one implementation the stacking
// codecs (binary, gob, flate, gzip, zlib) name themselves through.
func ComposeTag[T any](name string, next cas.Codec[T]) string {
	namer, ok := next.(cas.CodecNamer)
	if !ok {
		return ""
	}
	inner := namer.CodecName()
	if inner == "" {
		return ""
	}
	return name + "+" + inner
}
