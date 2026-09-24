package pack_test

import (
	"bytes"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
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
		codec := jsoncodec.New[pack.Data]()
		b, err := pack.EncodeWith(in, codec)
		if err != nil {
			t.Fatalf("EncodeWith() error = %v", err)
		}
		out, err := pack.DecodeWith(b, codec)
		if err != nil {
			t.Fatalf("DecodeWith() error = %v", err)
		}
		if out["key"] != key || out["value"] != value {
			t.Fatalf("DecodeWith(EncodeWith(data)) mismatch: got %#v, want %#v", out, in)
		}
	})
}
