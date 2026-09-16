package gzip

import (
	"reflect"
	"testing"

	flatecodec "github.com/dmundt/go-cask/cas/codec/flate"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type sample struct {
	Title string
	Body  string
	Data  []byte
}

func TestCodecRoundTrip(t *testing.T) {
	codec := New(jsoncodec.New[sample]())
	want := sample{
		Title: "hello",
		Body:  "world",
		Data:  []byte("compressed payload"),
	}

	data, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Encode produced empty payload")
	}

	got, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecIsCascadeable(t *testing.T) {
	codec := New(flatecodec.New(jsoncodec.New[sample]()))
	want := sample{Title: "hello", Body: "world", Data: []byte("compressed payload")}

	data, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Encode produced empty payload")
	}

	got, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecErrorsWhenWrappedCodecMissing(t *testing.T) {
	codec := New[sample](nil)
	if _, err := codec.Encode(sample{Title: "demo"}); err == nil {
		t.Fatal("Encode with nil codec returned nil error, want non-nil")
	}
	if _, err := codec.Decode([]byte("not gzip")); err == nil {
		t.Fatal("Decode with nil codec returned nil error, want non-nil")
	}
}
