package zlib_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	flatecodec "github.com/dmundt/go-cask/cas/codec/flate"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	zlibcodec "github.com/dmundt/go-cask/cas/codec/zlib"
)

type sample struct {
	ID   int
	Name string
	Data []byte
}

func TestCodecRoundTrip(t *testing.T) {
	codec := zlibcodec.New(jsoncodec.New[sample]())
	want := sample{ID: 7, Name: "demo", Data: []byte("hello world")}

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
	if got.ID != want.ID || got.Name != want.Name || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
}

func TestCodecRejectsNil(t *testing.T) {
	var c zlibcodec.Codec[sample]
	if _, err := c.Encode(sample{}); err == nil {
		t.Fatal("Encode(nil codec) = nil error, want error")
	}
	if _, err := c.Decode(nil); err == nil {
		t.Fatal("Decode(nil codec) = nil error, want error")
	}
}

func TestCodecIsCascadeable(t *testing.T) {
	codec := zlibcodec.New(flatecodec.New(jsoncodec.New[sample]()))
	want := sample{ID: 7, Name: "demo", Data: []byte("hello world")}

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
	if got.ID != want.ID || got.Name != want.Name || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
}

type errCodec[T any] struct{}

func (errCodec[T]) Encode(T) ([]byte, error) { return nil, errors.New("encode boom") }
func (errCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, errors.New("decode boom") }

func TestCodecUsesCasDigestSemantics(t *testing.T) {
	if _, err := cas.ParseDigest("abc"); err == nil {
		t.Fatal("ParseDigest accepted invalid digest")
	}
}

func TestCodecPropagatesWrappedErrorsAndRejectsBadInput(t *testing.T) {
	if _, err := zlibcodec.New(errCodec[sample]{}).Encode(sample{}); err == nil {
		t.Fatal("wrapped encode error should propagate")
	}
	if _, err := zlibcodec.New(errCodec[sample]{}).Decode([]byte("bad")); err == nil {
		t.Fatal("wrapped decode error should propagate")
	}
	if _, err := zlibcodec.New(jsoncodec.New[sample]()).Decode([]byte("bad")); err == nil {
		t.Fatal("invalid zlib payload should error")
	}
	if _, err := zlibcodec.New(jsoncodec.New[sample]()).Decode(nil); err == nil {
		t.Fatal("nil zlib payload should error")
	}
	if _, err := zlibcodec.New(jsoncodec.New[sample]()).Encode(sample{ID: 1}); err != nil {
		t.Fatal("valid zlib encode should succeed")
	}
}
