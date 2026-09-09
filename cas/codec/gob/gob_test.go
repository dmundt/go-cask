package gob_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas/codec/gob"
)

type obj struct {
	Title string
	Body  string
}

func TestRoundTrip(t *testing.T) {
	c := gob.New[obj]()
	orig := obj{Title: "gob", Body: "test"}
	data, err := c.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != orig {
		t.Fatalf("round-trip: %+v != %+v", got, orig)
	}
}

func TestDecodeError(t *testing.T) {
	c := gob.New[obj]()
	if _, err := c.Unmarshal([]byte("garbage")); err == nil {
		t.Fatal("invalid gob must error")
	}
}

func TestMarshalError(t *testing.T) {
	// encoding/gob cannot encode a channel, so Marshal must surface the error.
	c := gob.New[chan int]()
	if _, err := c.Marshal(make(chan int)); err == nil {
		t.Fatal("encoding an unsupported type must error")
	}
}
