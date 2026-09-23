package main

import (
	"bytes"
	"testing"

	gzipcodec "github.com/dmundt/go-cask/cas/codec/gzip"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// FuzzGzipCodecRoundTrip drives the real codec stack the example stores with —
// the shipped gzip wrapper over the JSON codec — on the real object type, so the
// corpus exercises the production path rather than a copy of it.
//
// The payload is a byte slice on purpose: JSON renders []byte as base64, so
// arbitrary bytes round-trip. A bare string payload would not, because
// encoding/json replaces invalid UTF-8 rather than failing.
func FuzzGzipCodecRoundTrip(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0xff, 0xb2, 0x7f})
	f.Fuzz(func(t *testing.T, data []byte) {
		codec := gzipcodec.New(jsoncodec.New[*Artifact]())
		in := &Artifact{Name: "fuzz", Data: data}

		encoded, err := codec.Encode(in)
		if err != nil {
			t.Fatalf("Encode(%d bytes): %v", len(data), err)
		}
		if len(encoded) == 0 {
			t.Fatal("gzip codec produced an empty payload")
		}
		// Deterministic output is what makes dedup work: the same artifact must
		// always encode to the same bytes, hence the same digest.
		again, err := codec.Encode(in)
		if err != nil {
			t.Fatalf("Encode(%d bytes) a second time: %v", len(data), err)
		}
		if !bytes.Equal(encoded, again) {
			t.Fatal("gzip codec output is not deterministic")
		}

		out, err := codec.Decode(encoded)
		if err != nil {
			t.Fatalf("Decode returned error for %d bytes: %v", len(data), err)
		}
		if out.Name != in.Name || !bytes.Equal(out.Data, in.Data) {
			t.Fatalf("round-trip mismatch: name %q != %q, %d != %d data bytes",
				out.Name, in.Name, len(out.Data), len(in.Data))
		}
	})
}
