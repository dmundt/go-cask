package gzip

import (
	"errors"
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

type errCodec[T any] struct{}

func (errCodec[T]) Encode(T) ([]byte, error) { return nil, errors.New("encode boom") }
func (errCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, errors.New("decode boom") }

func TestCodecErrorsWhenWrappedCodecMissing(t *testing.T) {
	codec := New[sample](nil)
	if _, err := codec.Encode(sample{Title: "demo"}); err == nil {
		t.Fatal("Encode with nil codec returned nil error, want non-nil")
	}
	if _, err := codec.Decode([]byte("not gzip")); err == nil {
		t.Fatal("Decode with nil codec returned nil error, want non-nil")
	}
}

func TestCodecPropagatesWrappedErrorsAndRejectsBadInput(t *testing.T) {
	if _, err := New(errCodec[sample]{}).Encode(sample{Title: "demo"}); err == nil {
		t.Fatal("wrapped encode error should propagate")
	}
	if _, err := New(errCodec[sample]{}).Decode([]byte("bad")); err == nil {
		t.Fatal("wrapped decode error should propagate")
	}
	if _, err := New(jsoncodec.New[sample]()).Decode([]byte("bad")); err == nil {
		t.Fatal("invalid gzip payload should error")
	}
	if _, err := New(jsoncodec.New[sample]()).Decode(nil); err == nil {
		t.Fatal("nil gzip payload should error")
	}
	if _, err := New(jsoncodec.New[sample]()).Encode(sample{}); err != nil {
		t.Fatal("valid gzip encode should succeed")
	}
}

// TestCodecName pins the composed identity tag: compressing changes the stored
// bytes, so the tag names the stack, and an inner codec that declares no tag
// leaves the stack unspecified rather than manufacturing one.
func TestCodecName(t *testing.T) {
	if got := New(jsoncodec.New[sample]()).CodecName(); got != "gzip+json" {
		t.Fatalf("CodecName() = %q, want gzip+json", got)
	}
	if got := New(nameless[sample]{}).CodecName(); got != "" {
		t.Fatalf("CodecName() over an unnamed codec = %q, want the empty (unspecified) tag", got)
	}
}

// nameless is a Codec[T] that declares no identity: it satisfies cas.Codec[T]
// but not cas.CodecNamer.
type nameless[T any] struct{}

// Encode encodes with the JSON codec.
func (nameless[T]) Encode(v T) ([]byte, error) { return jsoncodec.New[T]().Encode(v) }

// Decode decodes with the JSON codec.
func (nameless[T]) Decode(data []byte) (T, error) { return jsoncodec.New[T]().Decode(data) }
