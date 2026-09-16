package json_test

import (
	"testing"
	"unicode/utf8"

	"github.com/dmundt/go-cask/cas/codec/json"
)

// FuzzCodecRoundTrip checks the JSON codec round-trip property
// (Decode(Encode(v)) == v) over arbitrary string content. Input is
// restricted to valid UTF-8 because encoding/json deliberately replaces
// invalid UTF-8 on encode — that documented lossiness is pinned by an
// explicit test, not by this fuzz target.
func FuzzCodecRoundTrip(f *testing.F) {
	for _, seed := range []string{"", "hello", "λ + 日本語", "{\"nested\":\"json\"}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			t.Skip("encoding/json is lossy for invalid UTF-8 (pinned by an explicit test)")
		}
		c := json.New[fuzzNote]()
		v := fuzzNote{Title: s, Body: "fixed"}
		data, err := c.Encode(v)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		got, err := c.Decode(data)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got != v {
			t.Fatalf("round-trip = %+v, want %+v", got, v)
		}
	})
}

type fuzzNote struct {
	Title string
	Body  string
}
