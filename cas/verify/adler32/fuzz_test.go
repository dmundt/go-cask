package adler32_test

import (
	"testing"

	adler32 "github.com/dmundt/go-cask/cas/verify/adler32"
)

func FuzzFormatParse(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte(""), []byte("hello"), []byte("hello world"), []byte{0, 1, 2, 3, 4}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		d := adler32.Of(payload)
		formatted := adler32.Format(d)
		parsed, err := adler32.Parse(formatted)
		if err != nil {
			t.Fatalf("Parse(Format(d)) = %v", err)
		}
		if !parsed.Equal(d) {
			t.Fatalf("Parse(Format(d)) = %x, want %x", parsed, d)
		}
	})
}
