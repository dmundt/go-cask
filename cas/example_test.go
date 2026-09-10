package cas_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend/fs"
	memory "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// note is a minimal Object[T] used to show the generic Store.
type note struct{ Body string }

func (note) Type() string             { return "note@1" }
func (note) References() []cas.Digest { return nil }

// Example shows the typed Store (cas-core §4.8): build one over a backend with
// the JSON codec and the client's hasher, Put a value, and Get it back as the
// concrete type.
func Example() {
	ctx := context.Background()
	s := cas.New(memory.New(), jsoncodec.New[note](), sha256.New())
	h, err := s.Put(ctx, note{Body: "hi"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	n, err := s.Get(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(n.Body)
	// Output:
	// hi
}

// ExampleCodec shows the two seams of a Store (cas-core §4.6, §7.2, §8): the
// Codec[T] a client injects and the Backend it runs on. A codec is a wrapper,
// so compression (or encryption) is added without the core or the Backend
// knowing about it; and because an address is the digest of exactly the bytes
// the codec produced, the same value through the same codec keeps the same
// address on any backend.
func ExampleCodec() {
	ctx := context.Background()
	codec := gzipCodec[note]{inner: jsoncodec.New[note]()}

	inMemory := cas.New(memory.New(), codec, sha256.New())

	dir, err := os.MkdirTemp("", "cas-example")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(dir)
	raw, err := fs.New(dir)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	onDisk := cas.New(raw, codec, sha256.New())

	memDigest, err := inMemory.Put(ctx, note{Body: "hi"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	diskDigest, err := onDisk.Put(ctx, note{Body: "hi"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	back, err := onDisk.Get(ctx, diskDigest)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("read back:", back.Body)
	fmt.Println("same address on memory and disk:", memDigest.Equal(diskDigest))
	// Output:
	// read back: hi
	// same address on memory and disk: true
}

// gzipCodec[T] wraps another Codec[T] with gzip — the compression recipe of
// cas-core §7.2. Its output is deterministic (the gzip header's mtime is
// pinned), so identical values keep identical bytes and therefore identical
// addresses; an unpinned header would give the same value a new address on
// every write and silently defeat dedup.
type gzipCodec[T any] struct{ inner cas.Codec[T] }

func (c gzipCodec[T]) Marshal(v T) ([]byte, error) {
	plain, err := c.inner.Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Header.ModTime = time.Unix(0, 0)
	if _, err := zw.Write(plain); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c gzipCodec[T]) Unmarshal(data []byte) (T, error) {
	var zero T
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return zero, err
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		return zero, err
	}
	return c.inner.Unmarshal(plain)
}
