package fs

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// FuzzPathRoundTrip checks that hashPath then pathToHash round-trips for
// arbitrary digests across several fan-out layouts (flat, Git-like, deep).
// Any bytes decode to valid (even-length, lowercase) hex, so every non-empty
// input is a representable digest.
func FuzzPathRoundTrip(f *testing.F) {
	for _, seed := range [][]byte{{'a'}, []byte("abc"), bytes.Repeat([]byte{0xab}, 32)} {
		f.Add(seed)
	}
	layouts := []struct{ fanOut, fanLevels int }{{0, 0}, {2, 1}, {4, 2}}
	f.Fuzz(func(t *testing.T, digest []byte) {
		if len(digest) == 0 {
			t.Skip("empty digest is not a valid hash")
		}
		h, err := cas.NewHash("sha256", digest)
		if err != nil {
			t.Fatalf("NewHash: %v", err)
		}
		for _, lay := range layouts {
			s := &Backend{fanOut: lay.fanOut, fanLevels: lay.fanLevels}
			rel := s.hashPath(h)
			got, err := pathToHash(rel)
			if err != nil {
				t.Fatalf("layout %d/%d pathToHash(%q): %v", lay.fanOut, lay.fanLevels, rel, err)
			}
			if !got.Equal(h) {
				t.Fatalf("layout %d/%d round-trip: got %v, want %v", lay.fanOut, lay.fanLevels, got, h)
			}
		}
	})
}

// FuzzVerify checks the integrity contract on the fs backend: an intact
// object verifies, and any bit flip of the stored bytes must make Verify
// fail (ErrHashMismatch).
func FuzzVerify(f *testing.F) {
	for _, seed := range [][]byte{{'x'}, []byte("verify-me"), bytes.Repeat([]byte{0}, 16)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content []byte) {
		ctx := context.Background()
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		h, err := cas.HashBytes("sha256", content)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, h, bytes.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		if err := s.Verify(ctx, h); err != nil {
			t.Fatalf("intact object must verify: %v", err)
		}
		// Simulate bit rot: overwrite the stored file with different bytes.
		corrupt := append([]byte(nil), content...)
		if len(corrupt) == 0 {
			corrupt = []byte{0}
		} else {
			corrupt[0] ^= 0xff
		}
		if bytes.Equal(corrupt, content) {
			t.Skip("no-op corruption")
		}
		if err := os.WriteFile(s.hashPath(h), corrupt, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.Verify(ctx, h); err == nil {
			t.Fatal("Verify of corrupted data must fail")
		}
	})
}
