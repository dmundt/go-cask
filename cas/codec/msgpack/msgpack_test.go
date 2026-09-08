package msgpack_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas/codec/msgpack"
)

type obj struct {
	Title string
	Body  string
}

func TestRoundTrip(t *testing.T) {
	c := msgpack.New[obj]()
	orig := obj{Title: "msgpack", Body: "test"}
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
	c := msgpack.New[obj]()
	if _, err := c.Decode([]byte("not-msgpack")); err == nil {
		t.Fatal("corrupt msgpack must error")
	}
}
