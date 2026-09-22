package zlib

import (
	"compress/zlib"
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

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (failingReadCloser) Close() error             { return errors.New("close boom") }

func TestHelperErrorBranches(t *testing.T) {
	if _, err := encodeCompressed(jsoncodec.New[payload](), payload{Name: "demo"}, func(io.Writer) (io.WriteCloser, error) {
		return failingWriteCloser{}, nil
	}); err == nil {
		t.Fatal("write failure should propagate")
	}
	if _, err := encodeCompressed(jsoncodec.New[payload](), payload{Name: "demo"}, func(io.Writer) (io.WriteCloser, error) {
		return noOpWriteCloser{}, nil
	}); err != nil {
		t.Fatal("valid helper encode should succeed")
	}
	if _, err := decodeCompressed(jsoncodec.New[payload](), []byte("bad"), MaxDecodedBytes, func(io.Reader) (io.ReadCloser, error) {
		return nil, errors.New("open boom")
	}); err == nil {
		t.Fatal("reader creation failure should propagate")
	}
	if _, err := decodeCompressed(jsoncodec.New[payload](), []byte("bad"), MaxDecodedBytes, func(io.Reader) (io.ReadCloser, error) {
		return failingReadCloser{}, nil
	}); err == nil {
		t.Fatal("read failure should propagate")
	}
}

// TestDecodeRejectsPayloadOverLimit pins the decompression ceiling: the stored
// bytes are untrusted, so a payload that inflates past the limit is rejected
// instead of allocated. The limit is passed explicitly so the test exercises
// the boundary without materializing a gigabyte.
func TestDecodeRejectsPayloadOverLimit(t *testing.T) {
	next := jsoncodec.New[payload]()
	blob, err := encodeCompressed(next, payload{Name: "expand me"}, func(w io.Writer) (io.WriteCloser, error) {
		return zlib.NewWriter(w), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := next.Encode(payload{Name: "expand me"})
	if err != nil {
		t.Fatal(err)
	}
	open := func(r io.Reader) (io.ReadCloser, error) { return zlib.NewReader(r) }

	if _, err := decodeCompressed(next, blob, int64(len(plain)), open); err != nil {
		t.Fatalf("decode exactly at the ceiling = %v, want success", err)
	}
	if _, err := decodeCompressed(next, blob, int64(len(plain))-1, open); !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("decode one byte over the ceiling = %v, want ErrDecodedTooLarge", err)
	}
}

type noOpWriteCloser struct{}

func (noOpWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (noOpWriteCloser) Close() error                { return nil }
