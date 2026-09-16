package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func FuzzParseDigestRoundTrip(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})
	f.Add([]byte("CASK"))

	f.Fuzz(func(t *testing.T, in []byte) {
		h := sha256.Sum256(in)
		d := cas.NewDigest(h[:])
		want := "sha256:" + hex.EncodeToString(h[:])
		got := FormatDigest("sha256", d)
		if got != want {
			t.Fatalf("FormatDigest(%q) = %q, want %q", d, got, want)
		}

		parsed, err := ParseDigest("sha256", got, len(h))
		if err != nil {
			t.Fatalf("ParseDigest(%q) returned error: %v", got, err)
		}
		if !parsed.Equal(d) {
			t.Fatalf("ParseDigest(%q) = %q, want %q", got, parsed, d)
		}
	})
}
