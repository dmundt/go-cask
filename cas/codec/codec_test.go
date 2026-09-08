package codec_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas/codec"
)

type testObj struct {
	Title string
	Body  string
}

func TestJSONCodecRoundTrip(t *testing.T) {
	c := codec.JSONCodec[testObj]{}
	orig := testObj{Title: "json", Body: "test"}
	data, err := c.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	var decoded testObj
	decoded, err = c.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("json round-trip: %+v != %+v", decoded, orig)
	}
}

func TestGobCodecRoundTrip(t *testing.T) {
	c := codec.GobCodec[testObj]{}
	orig := testObj{Title: "gob", Body: "test"}
	data, err := c.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := c.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != orig {
		t.Fatalf("gob round-trip: %+v != %+v", decoded, orig)
	}
}

func TestGobCodecDecodeError(t *testing.T) {
	c := codec.GobCodec[testObj]{}
	if _, err := c.Decode([]byte("garbage")); err == nil {
		t.Fatal("invalid gob must error")
	}
}

func TestJSONCodecDecodeError(t *testing.T) {
	c := codec.JSONCodec[testObj]{}
	if _, err := c.Decode([]byte("{invalid")); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestGobCodecPointer(t *testing.T) {
	c := codec.GobCodec[*testObj]{}
	orig := &testObj{Title: "pt", Body: "r"}
	data, err := c.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := c.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Title != "pt" {
		t.Fatalf("gob pointer: %+v", decoded)
	}
}
