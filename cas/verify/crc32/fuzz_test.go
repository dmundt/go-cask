package crc32_test

import (
	"testing"

	crc32 "github.com/dmundt/go-cask/cas/verify/crc32"
)

// FuzzFormatParse ensures the printable form round-trips through the parser for
// arbitrary payload bytes.
func FuzzFormatParse(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte(""), []byte("hello"), []byte("hello world"), []byte{0, 1, 2, 3, 4}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		d := crc32.Of(payload)
		formatted := crc32.Format(d)
		parsed, err := crc32.Parse(formatted)
		if err != nil {
			t.Fatalf("Parse(Format(d)) = %v", err)
		}
		if !parsed.Equal(d) {
			t.Fatalf("Parse(Format(d)) = %x, want %x", parsed, d)
		}
	})
}
