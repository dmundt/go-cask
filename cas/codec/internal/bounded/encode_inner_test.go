package bounded

import (
	"errors"
	"io"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// failingCodec is a cas.Codec[T] whose Encode always fails, so Encode's
// inner-serialization branch is reached without a format that fails on demand.
type failingCodec[T any] struct{ err error }

func (c failingCodec[T]) Encode(T) ([]byte, error) { return nil, c.err }
func (c failingCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, c.err }

// TestEncodePropagatesInnerSerializationFailure pins the branch between the
// nil-codec guard and the writer: a payload the inner codec refuses is reported
// as that codec's error, and the compression writer is never constructed — so a
// caller sees a serialization failure rather than an empty archive.
func TestEncodePropagatesInnerSerializationFailure(t *testing.T) {
	boom := errors.New("inner encode exploded")
	writerBuilt := false

	data, err := Encode(failingCodec[payload]{err: boom}, payload{Name: "demo"}, func(io.Writer) (io.WriteCloser, error) {
		writerBuilt = true
		return noOpWriteCloser{}, nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Encode with a failing inner codec = %v, want %v", err, boom)
	}
	if data != nil {
		t.Fatalf("Encode with a failing inner codec = %q, want no bytes", data)
	}
	if writerBuilt {
		t.Fatal("Encode constructed the compression writer despite the serialization failure")
	}
}

// TestEncodeInnerSuccessStillCompresses is the positive control for the branch
// above: the same seam with a working inner codec produces a payload the reader
// can inflate back to the inner codec's bytes.
func TestEncodeInnerSuccessStillCompresses(t *testing.T) {
	next := jsoncodec.New[payload]()
	want, err := next.Encode(payload{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	blob, err := Encode(next, payload{Name: "demo"}, flateWriter)
	if err != nil {
		t.Fatalf("Encode = %v", err)
	}
	got, err := Decode(next, blob, MaxDecodedBytes, flateReader)
	if err != nil {
		t.Fatalf("Decode = %v", err)
	}
	plain, err := next.Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(want) {
		t.Fatalf("round-trip payload = %s, want %s", plain, want)
	}
}
