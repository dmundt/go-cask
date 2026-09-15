package flate_test

import (
	"bytes"
	"testing"

	flatecodec "github.com/dmundt/go-cask/cas/codec/flate"
	gzipcodec "github.com/dmundt/go-cask/cas/codec/gzip"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type sample struct {
	ID   int
	Name string
	Data []byte
}

func TestCodecRoundTrip(t *testing.T) {
	codec := flatecodec.New(jsoncodec.New[sample]())
	want := sample{ID: 9, Name: "demo", Data: []byte("hello world")}

	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Marshal produced empty payload")
	}

	got, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
}

func TestCodecIsCascadeable(t *testing.T) {
	codec := flatecodec.New(gzipcodec.New(jsoncodec.New[sample]()))
	want := sample{ID: 9, Name: "demo", Data: []byte("hello world")}

	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Marshal produced empty payload")
	}

	got, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
}

func TestCodecRejectsNil(t *testing.T) {
	var c flatecodec.Codec[sample]
	if _, err := c.Marshal(sample{}); err == nil {
		t.Fatal("Marshal(nil codec) = nil error, want error")
	}
	if _, err := c.Unmarshal(nil); err == nil {
		t.Fatal("Unmarshal(nil codec) = nil error, want error")
	}
}
