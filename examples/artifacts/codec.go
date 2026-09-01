package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// gzipCodec[T] wraps a Codec[T] (the default JSONCodec[T]) with gzip
// compression — the codec-composition pattern from cas-core §7.2. It is used
// for both artifacts and manifests, so their stored payloads are compressed.
//
// Output is deterministic: the gzip header's mtime is pinned, so identical
// values encode to identical bytes and therefore identical hashes (dedup).
type gzipCodec[T any] struct{ inner cas.Codec[T] }

// newGzipCodec wraps the default JSONCodec[T].
func newGzipCodec[T any]() gzipCodec[T] { return gzipCodec[T]{inner: cas.JSONCodec[T]{}} }

// Encode gzip-compresses the inner codec's output.
func (c gzipCodec[T]) Encode(v T) ([]byte, error) {
	inner, err := c.inner.Encode(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Header.ModTime = time.Unix(0, 0)
	if _, err := zw.Write(inner); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode gunzip-decompresses and decodes with the inner codec.
func (c gzipCodec[T]) Decode(data []byte) (T, error) {
	var zero T
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return zero, fmt.Errorf("gunzip: %w", err)
	}
	defer zr.Close()
	inner, err := io.ReadAll(zr)
	if err != nil {
		return zero, fmt.Errorf("gunzip: %w", err)
	}
	return c.inner.Decode(inner)
}
