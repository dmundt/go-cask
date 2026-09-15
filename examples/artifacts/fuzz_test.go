package main

import (
	"testing"
)

func FuzzGzipJSONRoundTrip(f *testing.F) {
	f.Add("hello world")
	f.Add("[]byte{1,2,3}")
	f.Add("")
	f.Add("unicode: 你好")
	f.Fuzz(func(t *testing.T, input string) {
		encoded := gzipJSON(input)
		if len(encoded) == 0 {
			t.Fatal("gzipJSON produced empty payload")
		}
		out, err := gunzipJSON[string](encoded)
		if err != nil {
			t.Fatalf("gunzipJSON returned error for %q: %v", input, err)
		}
		if *out != input {
			t.Fatalf("round-trip mismatch: %q != %q", *out, input)
		}
	})
}
