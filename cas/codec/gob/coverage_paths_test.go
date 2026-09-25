package gob_test

import (
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	gobcodec "github.com/dmundt/go-cask/cas/codec/gob"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// failingCodec is a cas.Codec[T] whose Encode always fails, so the gob stack's
// inner-failure branches are reached without a format that fails on demand.
type failingCodec[T any] struct{ err error }

func (c failingCodec[T]) Encode(T) ([]byte, error) { return nil, c.err }
func (c failingCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, c.err }

// unnamedCodec declares no identity tag, so a stack over it is unspecified.
type unnamedCodec[T any] struct{}

func (unnamedCodec[T]) Encode(v T) ([]byte, error) { return []byte(`"x"`), nil }
func (unnamedCodec[T]) Decode([]byte) (T, error)   { var zero T; return zero, nil }

// untaggedCodec names itself with an empty tag, the other way a stack becomes
// unspecified.
type untaggedCodec[T any] struct{}

func (untaggedCodec[T]) Encode(v T) ([]byte, error) { return []byte(`"x"`), nil }
func (untaggedCodec[T]) Decode([]byte) (T, error)   { var zero T; return zero, nil }
func (untaggedCodec[T]) CodecName() string          { return "" }

// TestStackedEncodePropagatesInnerFailure pins the encode branch of a stacked
// gob codec: when the inner codec cannot serialize the value, its error is
// reported and no gob frame is produced.
func TestStackedEncodePropagatesInnerFailure(t *testing.T) {
	boom := errors.New("inner encode exploded")
	codec := gobcodec.New[string](failingCodec[string]{err: boom})

	data, err := codec.Encode("value")
	if !errors.Is(err, boom) {
		t.Fatalf("stacked Encode with a failing inner codec = %v, want %v", err, boom)
	}
	if data != nil {
		t.Fatalf("stacked Encode with a failing inner codec = %q, want no bytes", data)
	}
}

// TestStackedDecodePropagatesInnerFailure pins the decode branch of a stacked
// gob codec: the gob frame decodes to the inner payload, and the inner codec's
// own decode failure is reported rather than swallowed.
func TestStackedDecodePropagatesInnerFailure(t *testing.T) {
	boom := errors.New("inner decode exploded")
	enc := gobcodec.New[string](jsoncodec.New[string]())
	frame, err := enc.Encode("value")
	if err != nil {
		t.Fatal(err)
	}

	dec := gobcodec.New[string](failingCodec[string]{err: boom})
	got, err := dec.Decode(frame)
	if !errors.Is(err, boom) {
		t.Fatalf("stacked Decode with a failing inner codec = %v, want %v", err, boom)
	}
	if got != "" {
		t.Fatalf("stacked Decode with a failing inner codec = %q, want the zero value", got)
	}
}

// TestCodecNameLeavesAnUnspecifiedInnerCodecUnspecified pins composeTag's ""
// return for a stacked gob codec: an inner codec with no identity must leave
// the stack unspecified instead of claiming "gob" over bytes that are actually
// the inner codec's.
//
// gob.go:52 (a gob failure while wrapping the inner payload) is left uncovered
// on purpose (testing-strategy §5): the value handed to the gob encoder on that
// path is always the inner codec's []byte, and encoding a byte slice with
// encoding/gob fails only when its length exceeds the format's 32-bit length
// field — a payload no codec in this repository can produce.
func TestCodecNameLeavesAnUnspecifiedInnerCodecUnspecified(t *testing.T) {
	for _, tc := range []struct {
		name string
		next cas.Codec[string]
	}{
		{"an unnamed inner codec", unnamedCodec[string]{}},
		{"an inner codec with an empty tag", untaggedCodec[string]{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := gobcodec.New[string](tc.next).CodecName(); got != "" {
				t.Fatalf("CodecName() = %q, want \"\" for %s", got, tc.name)
			}
		})
	}
}
