package cas

import (
	"errors"
	"strings"
	"testing"
)

func TestHashBytes(t *testing.T) {
	data := []byte("hash bytes")
	got, err := HashBytes("sha256", data)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hashData("sha256", data)
	if got.String() != want.String() {
		t.Fatalf("HashBytes = %s, want %s", got, want)
	}
	// One-shot custom algorithms (no stream constructor) use the HashFunc
	// fallback path.
	if _, err := HashBytes("nope", data); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("unknown algo = %v, want ErrUnknownAlgorithm", err)
	}
}

// A one-shot registered algorithm (no streaming hasher) exercises the
// HashBytes fallback.
func TestHashBytesOneShotFallback(t *testing.T) {
	RegisterHash("oneshot", func(data []byte) Hash {
		return hash{algo: "oneshot", bytes: []byte{0x01}}
	})
	h, err := HashBytes("oneshot", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != "oneshot:01" {
		t.Fatalf("HashBytes(oneshot) = %q", h.String())
	}
}

func TestNewHasher(t *testing.T) {
	h, err := NewHasher("sha256")
	if err != nil {
		t.Fatal(err)
	}
	h.Write([]byte("abc"))
	sum := h.Sum(nil)
	expected, _ := hashData("sha256", []byte("abc"))
	if string(sum) != string(expected.Bytes()) {
		t.Fatal("streaming hash mismatch")
	}
	// A one-shot algorithm cannot stream.
	if _, err := NewHasher("oneshot"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(oneshot) = %v, want ErrUnknownAlgorithm", err)
	}
	if _, err := NewHasher("nope"); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("NewHasher(nope) = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestHashBytesRoundTrip(t *testing.T) {
	// HashBytes output parses back into the same hash.
	h, _ := HashBytes("sha256", []byte("round trip"))
	back, err := ParseHash(h.String())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Equal(back) {
		t.Fatal("round-trip mismatch")
	}
	if !strings.HasPrefix(h.String(), "sha256:") {
		t.Fatalf("hash = %q", h.String())
	}
}
