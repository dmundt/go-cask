package gob_test

import (
	"testing"

	flatecodec "github.com/dmundt/go-cask/cas/codec/flate"
	"github.com/dmundt/go-cask/cas/codec/gob"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type obj struct {
	Title string
	Body  string
}

func TestRoundTrip(t *testing.T) {
	c := gob.NewRaw[obj]()
	orig := obj{Title: "gob", Body: "test"}
	data, err := c.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != orig {
		t.Fatalf("round-trip: %+v != %+v", got, orig)
	}
}

func TestCascadeRoundTrip(t *testing.T) {
	c := gob.New(flatecodec.New(jsoncodec.New[obj]()))
	orig := obj{Title: "gob", Body: "cascaded"}
	data, err := c.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != orig {
		t.Fatalf("round-trip: %+v != %+v", got, orig)
	}
}

func TestDecodeError(t *testing.T) {
	c := gob.NewRaw[obj]()
	if _, err := c.Decode([]byte("garbage")); err == nil {
		t.Fatal("invalid gob must error")
	}
}

func TestMarshalError(t *testing.T) {
	// encoding/gob cannot encode a channel, so Marshal must surface the error.
	c := gob.NewRaw[chan int]()
	if _, err := c.Encode(make(chan int)); err == nil {
		t.Fatal("encoding an unsupported type must error")
	}
}

func TestWrappedDecodeAndErrorBranches(t *testing.T) {
	c := gob.New(flatecodec.New(jsoncodec.New[obj]()))
	if _, err := c.Decode([]byte("bad gob")); err == nil {
		t.Fatal("invalid wrapped payload must error")
	}
	wrapped := gob.New[obj](nil)
	if _, err := wrapped.Decode([]byte("bad gob")); err == nil {
		t.Fatal("nil wrapped codec still decodes via gob and should fail on invalid payload")
	}
	if _, err := wrapped.Encode(obj{Title: "x", Body: "y"}); err != nil {
		t.Fatal("plain gob encode should succeed")
	}
}
