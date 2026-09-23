package gob_test

import (
	"testing"

	"github.com/dmundt/go-cask/cas"
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

// TestCodecName pins the identity tag: "gob" for a direct codec, and the
// composed "gob+<inner>" for a stack, because a stacked codec stores
// gob-wrapped inner bytes rather than a gob-encoded T — the two must not share
// a tag.
func TestCodecName(t *testing.T) {
	var namer cas.CodecNamer = gob.NewRaw[obj]()
	if got := namer.CodecName(); got != "gob" {
		t.Fatalf("NewRaw CodecName() = %q, want gob", got)
	}
	if got := gob.New[obj](nil).CodecName(); got != "gob" {
		t.Fatalf("New(nil) CodecName() = %q, want gob", got)
	}
	stacked := gob.New(flatecodec.New(jsoncodec.New[obj]()))
	if got := stacked.CodecName(); got != "gob+flate+json" {
		t.Fatalf("stacked CodecName() = %q, want gob+flate+json", got)
	}
	if got := gob.New(nameless[obj]{}).CodecName(); got != "" {
		t.Fatalf("unnamed inner CodecName() = %q, want the empty (unspecified) tag", got)
	}
}

// nameless is a Codec[T] that declares no identity: it satisfies cas.Codec[T]
// but not cas.CodecNamer.
type nameless[T any] struct{}

// Encode encodes with the JSON codec.
func (nameless[T]) Encode(v T) ([]byte, error) { return jsoncodec.New[T]().Encode(v) }

// Decode decodes with the JSON codec.
func (nameless[T]) Decode(data []byte) (T, error) { return jsoncodec.New[T]().Decode(data) }
