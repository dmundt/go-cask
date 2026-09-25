package crc32_test

import (
	"bytes"
	"hash/crc32"
	"testing"

	crc32hasher "github.com/dmundt/go-cask/cas/verify/crc32"
)

// TestNewHasherStreamsTheSameChecksumAsOf pins the streaming hasher the CLI
// wires for hash-on-write: writing the bytes in pieces to the returned hash.Hash32
// must produce exactly the checksum Of computes over the whole buffer.
func TestNewHasherStreamsTheSameChecksumAsOf(t *testing.T) {
	data := []byte("hash-on-write payload")

	h := crc32hasher.NewHasher()
	if h.Size() != crc32hasher.Size {
		t.Fatalf("NewHasher().Size() = %d, want %d", h.Size(), crc32hasher.Size)
	}
	if got, want := h.BlockSize(), crc32.NewIEEE().BlockSize(); got != want {
		t.Fatalf("NewHasher().BlockSize() = %d, want %d", got, want)
	}

	// Write in two pieces, so the result proves the hasher streams rather than
	// only accepting one call.
	if _, err := h.Write(data[:6]); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write(data[6:]); err != nil {
		t.Fatal(err)
	}

	want := crc32hasher.Of(data)
	sum := h.Sum(nil)
	if len(sum) != crc32hasher.Size {
		t.Fatalf("Sum(nil) = %d bytes, want %d", len(sum), crc32hasher.Size)
	}
	if !bytes.Equal(sum, want) {
		t.Fatalf("streaming checksum = %x, want %x (Of)", sum, want)
	}
	if got := h.Sum32(); got != crc32.ChecksumIEEE(data) {
		t.Fatalf("Sum32() = %08x, want %08x", got, crc32.ChecksumIEEE(data))
	}
	if got := h.Sum32(); got != uint32(sum[0])<<24|uint32(sum[1])<<16|uint32(sum[2])<<8|uint32(sum[3]) {
		t.Fatalf("Sum32() = %08x, want the big-endian Sum bytes %x", got, sum)
	}
}
