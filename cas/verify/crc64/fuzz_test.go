package crc64_test

import (
	"testing"

	crc64 "github.com/dmundt/go-cask/cas/verify/crc64"
)

func FuzzFormatParse(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte(""), []byte("hello"), []byte("hello world"), []byte{0, 1, 2, 3, 4}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		d := crc64.Of(payload)
		formatted := crc64.Format(d)
		parsed, err := crc64.Parse(formatted)
		if err != nil {
			t.Fatalf("Parse(Format(d)) = %v", err)
		}
		if !parsed.Equal(d) {
			t.Fatalf("Parse(Format(d)) = %x, want %x", parsed, d)
		}
	})
}
