package pack_test

import (
	"bytes"
	"testing"

	"github.com/dmundt/go-cask/cas/pack"
)

func FuzzSplitJoinRoundTrip(f *testing.F) {
	f.Add([]byte("hello"), 3)
	f.Add([]byte("abcdefghijklmnopqrstuvwxyz"), 7)
	f.Add([]byte{}, 1)
	f.Add([]byte("abc123xyz"), -4)

	f.Fuzz(func(t *testing.T, in []byte, size int) {
		if size < 0 {
			size = -size
		}
		got := pack.Join(pack.Split(in, size))
		if !bytes.Equal(got, in) {
			t.Fatalf("Join(Split(%q, %d)) = %q, want %q", in, size, got, in)
		}
	})
}

func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	f.Add("alpha", "one")
	f.Add("beta", "two")
	f.Add("", "")
	f.Add("multi", "value\nwith\nnewlines")

	f.Fuzz(func(t *testing.T, key, value string) {
		in := pack.Data{"key": key, "value": value}
		b, err := pack.Encode(in)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		out, err := pack.Decode(b)
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if out["key"] != key || out["value"] != value {
			t.Fatalf("Decode(Encode(data)) mismatch: got %#v, want %#v", out, in)
		}
	})
}
