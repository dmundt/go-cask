package zlib

import (
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
	if _, err := decodeCompressed(jsoncodec.New[payload](), []byte("bad"), func(io.Reader) (io.ReadCloser, error) {
		return nil, errors.New("open boom")
	}); err == nil {
		t.Fatal("reader creation failure should propagate")
	}
	if _, err := decodeCompressed(jsoncodec.New[payload](), []byte("bad"), func(io.Reader) (io.ReadCloser, error) {
		return failingReadCloser{}, nil
	}); err == nil {
		t.Fatal("read failure should propagate")
	}
}

type noOpWriteCloser struct{}

func (noOpWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (noOpWriteCloser) Close() error                { return nil }
