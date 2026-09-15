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

	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("marshal produced empty payload")
	}

	got, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecIsCascadeable(t *testing.T) {
	codec := New(flatecodec.New(jsoncodec.New[sample]()))
	want := sample{Title: "hello", Body: "world", Data: []byte("compressed payload")}

	data, err := codec.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("marshal produced empty payload")
	}

	got, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func TestCodecErrorsWhenWrappedCodecMissing(t *testing.T) {
	codec := New[sample](nil)
	if _, err := codec.Marshal(sample{Title: "demo"}); err == nil {
		t.Fatal("Marshal with nil codec returned nil error, want non-nil")
	}
	if _, err := codec.Unmarshal([]byte("not gzip")); err == nil {
		t.Fatal("Unmarshal with nil codec returned nil error, want non-nil")
	}
}
