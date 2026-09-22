package gob_test

import (
	"reflect"
	"testing"

	"github.com/dmundt/go-cask/cas/codec/gob"
)

type fuzzPayload struct {
	Title   string
	Body    string
	Count   int
	Enabled bool
}

func FuzzCodecRoundTrip(f *testing.F) {
	for _, seed := range []struct {
		title   string
		count   int
		enabled bool
	}{
		{"", 0, false},
		{"hello", 7, true},
		{"λ + 日本語", -3, true},
		{"nested { \"json\": true }", 42, false},
	} {
		f.Add(seed.title, seed.count, seed.enabled)
	}

	f.Fuzz(func(t *testing.T, title string, count int, enabled bool) {
		want := fuzzPayload{Title: title, Body: "gob payload", Count: count, Enabled: enabled}
		codec := gob.NewRaw[fuzzPayload]()
		data, err := codec.Encode(want)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		got, err := codec.Decode(data)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round-trip mismatch: got %#v want %#v", got, want)
		}
	})
}
