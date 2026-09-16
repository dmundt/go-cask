package cbor_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas/codec/cbor"
)

func FuzzRoundTrip(f *testing.F) {
	f.Add("")
	f.Add("hello")
	f.Add("[]")
	f.Add("{\"a\":1,\"b\":true}")
	f.Add("[1,2,3]")
	f.Add("[\"x\",null,\"y\"]")
	f.Add("[\"a\",\"b\",\"c\"]")
	f.Add("[0,1,2,3,4,5]")
	f.Add("[true,false,null]")

	f.Fuzz(func(t *testing.T, s string) {
		codec := cbor.NewMap()
		data, err := codec.Encode(map[string]any{
			"raw":     s,
			"len":     int64(len(s)),
			"empty":   "",
			"active":  len(s)%2 == 0,
			"values":  []any{s, int64(len(s)), len(s)%2 == 0, nil},
			"nested":  map[string]any{"k": s, "n": int64(len(s))},
			"bytes":   []byte(s),
			"numbers": []any{int64(len(s)), uint64(len(s) + 1)},
		})
		if err != nil {
			t.Skip()
		}
		got, err := codec.Decode(data)
		if err != nil {
			t.Skip()
		}
		if got["raw"] != s {
			t.Fatalf("raw mismatch: %v != %q", got["raw"], s)
		}
		if normalizeMap(got["len"]) != normalizeMap(int64(len(s))) {
			t.Fatalf("len mismatch: %v != %d", got["len"], len(s))
		}
	})
}
