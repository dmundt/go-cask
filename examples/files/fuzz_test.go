package main

import (
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func FuzzPrintableDigest(f *testing.F) {
	for _, seed := range [][]byte{{0x00}, []byte("hello"), []byte{0xab, 0xcd, 0xef}, nil} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		d := cas.NewDigest(raw)
		got := printable(d)
		if d.IsZero() {
			if got != "<absent>" {
				t.Fatalf("printable(absent) = %q, want <absent>", got)
			}
			return
		}
		if got == "" {
			t.Fatal("printable(non-absent) must not be empty")
		}
		if got == "<absent>" {
			t.Fatal("printable(non-absent) must not render as absent")
		}
	})
}
