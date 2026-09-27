package bounded

import (
	"compress/flate"
	"errors"
	"io"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type payload struct {
	Name string
}

type failingWriteCloser struct{}

func (failingWriteCloser) Write([]byte) (int, error) { return 0, errors.New("write boom") }
func (failingWriteCloser) Close() error              { return errors.New("close boom") }
func (failingWriteCloser) Reset(io.Writer)           {}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (failingReadCloser) Close() error             { return errors.New("close boom") }
func (failingReadCloser) Reset(io.Reader) error    { return nil }

type noOpWriteCloser struct{}

func (noOpWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (noOpWriteCloser) Close() error                { return nil }
func (noOpWriteCloser) Reset(io.Writer)             {}

// closeFailingWriteCloser accepts every write and fails on Close, the branch a
// writer that fails earlier can never reach.
type closeFailingWriteCloser struct{}

func (closeFailingWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (closeFailingWriteCloser) Close() error                { return errors.New("close boom") }
func (closeFailingWriteCloser) Reset(io.Writer)             {}

// unnamedCodec declares no identity tag, so ComposeTag reports "" for it.
type unnamedCodec[T any] struct{}

func (unnamedCodec[T]) Encode(T) ([]byte, error) { return nil, nil }
func (unnamedCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, nil }

// untaggedCodec names itself but declares no tag, the other way a stack becomes
// unspecified.
type untaggedCodec[T any] struct{}

func (untaggedCodec[T]) Encode(T) ([]byte, error) { return nil, nil }
func (untaggedCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, nil }
func (untaggedCodec[T]) CodecName() string        { return "" }

// dictReader adapts a decompressor whose Reset takes a preset dictionary — the
// shape compress/flate and compress/zlib expose — to the pool's Decompressor
// contract. The compression wrappers carry the same adapter; this one lets
// bounded's own tests pool a real flate reader instead of a stub.
type dictReader struct{ io.ReadCloser }

func (d dictReader) Reset(r io.Reader) error {
	return d.ReadCloser.(flate.Resetter).Reset(r, nil)
}

// flateWriterPool and flateReaderPool pool the real flate codec, so the pool
// tests exercise Reset on the standard library rather than on a stub.
func flateWriterPool() *WriterPool {
	return NewWriterPool(func(w io.Writer) (Compressor, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})
}

func flateReaderPool() *ReaderPool {
	return NewReaderPool(func(r io.Reader) (Decompressor, error) {
		return dictReader{ReadCloser: flate.NewReader(r)}, nil
	})
}

func TestEncodeErrorBranches(t *testing.T) {
	if _, err := Encode(jsoncodec.New[payload](), payload{Name: "demo"}, NewWriterPool(func(io.Writer) (Compressor, error) {
		return nil, errors.New("writer boom")
	})); err == nil {
		t.Fatal("writer construction failure should propagate")
	}
	if _, err := Encode(jsoncodec.New[payload](), payload{Name: "demo"}, NewWriterPool(func(io.Writer) (Compressor, error) {
		return failingWriteCloser{}, nil
	})); err == nil {
		t.Fatal("write failure should propagate")
	}
	if _, err := Encode(jsoncodec.New[payload](), payload{Name: "demo"}, NewWriterPool(func(io.Writer) (Compressor, error) {
		return closeFailingWriteCloser{}, nil
	})); err == nil {
		t.Fatal("close failure should propagate")
	}
	if _, err := Encode(jsoncodec.New[payload](), payload{Name: "demo"}, NewWriterPool(func(io.Writer) (Compressor, error) {
		return noOpWriteCloser{}, nil
	})); err != nil {
		t.Fatalf("valid helper encode = %v, want success", err)
	}
	if _, err := Encode[payload](nil, payload{}, flateWriterPool()); !errors.Is(err, ErrNilCodec) {
		t.Fatalf("Encode with a nil inner codec = %v, want ErrNilCodec", err)
	}
}

func TestDecodeErrorBranches(t *testing.T) {
	if _, err := Decode(jsoncodec.New[payload](), []byte("bad"), MaxDecodedBytes, NewReaderPool(func(io.Reader) (Decompressor, error) {
		return nil, errors.New("open boom")
	})); err == nil {
		t.Fatal("reader creation failure should propagate")
	}
	if _, err := Decode(jsoncodec.New[payload](), []byte("bad"), MaxDecodedBytes, NewReaderPool(func(io.Reader) (Decompressor, error) {
		return failingReadCloser{}, nil
	})); err == nil {
		t.Fatal("read failure should propagate")
	}
	if _, err := Decode[payload](nil, []byte("bad"), MaxDecodedBytes, flateReaderPool()); !errors.Is(err, ErrNilCodec) {
		t.Fatalf("Decode with a nil inner codec = %v, want ErrNilCodec", err)
	}
}

// TestDecodeRejectsPayloadOverLimit pins the decompression ceiling: the stored
// bytes are untrusted, so a payload that inflates past the limit is rejected
// instead of allocated. The limit is passed explicitly so the test exercises
// the boundary without materializing a gigabyte.
func TestDecodeRejectsPayloadOverLimit(t *testing.T) {
	next := jsoncodec.New[payload]()
	writers, readers := flateWriterPool(), flateReaderPool()
	blob, err := Encode(next, payload{Name: "expand me"}, writers)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := next.Encode(payload{Name: "expand me"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Decode(next, blob, int64(len(plain)), readers); err != nil {
		t.Fatalf("decode exactly at the ceiling = %v, want success", err)
	}
	if _, err := Decode(next, blob, int64(len(plain))-1, readers); !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("decode one byte over the ceiling = %v, want ErrDecodedTooLarge", err)
	}
}

// TestComposeTag covers the branches: a named inner codec contributes its tag;
// an unnamed one, and one that names itself with an empty tag, leave the stack
// unspecified instead of inventing a tag that would later read as a mismatch.
func TestComposeTag(t *testing.T) {
	if got := ComposeTag("flate", jsoncodec.New[payload]()); got != "flate+json" {
		t.Fatalf("ComposeTag over a named codec = %q, want %q", got, "flate+json")
	}
	if got := ComposeTag("flate", unnamedCodec[payload]{}); got != "" {
		t.Fatalf("ComposeTag over an unnamed codec = %q, want \"\"", got)
	}
	if got := ComposeTag("flate", untaggedCodec[payload]{}); got != "" {
		t.Fatalf("ComposeTag over an untagged codec = %q, want \"\"", got)
	}
}
