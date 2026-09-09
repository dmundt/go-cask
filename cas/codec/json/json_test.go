package json_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas/codec/json"
)

type obj struct {
	Title string
	Body  string
}

func TestRoundTrip(t *testing.T) {
	c := json.New[obj]()
	orig := obj{Title: "json", Body: "test"}
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
	c := json.New[obj]()
	if _, err := c.Unmarshal([]byte("{invalid")); err == nil {
		t.Fatal("invalid JSON must error")
	}
}
