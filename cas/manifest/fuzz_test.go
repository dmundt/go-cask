package manifest_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas/manifest"
)

func FuzzEncodeDecodeRoundTrip(f *testing.F) {
	f.Add("alpha", "one")
	f.Add("beta", "two")
	f.Add("", "")
	f.Add("multi", "value\nwith\nnewlines")

	f.Fuzz(func(t *testing.T, key, value string) {
		in := manifest.Data{"key": key, "value": value}
		b, err := manifest.Encode(in)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		out, err := manifest.Decode(b)
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if out["key"] != key || out["value"] != value {
			t.Fatalf("Decode(Encode(data)) mismatch: got %#v, want %#v", out, in)
		}
	})
}
