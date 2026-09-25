package binary

import (
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// failingCodec is a cas.Codec[T] whose both directions fail, so the binary
// wrapper's propagation branches are reached without a real format that fails.
type failingCodec[T any] struct{ err error }

func (c failingCodec[T]) Encode(T) ([]byte, error) { return nil, c.err }
func (c failingCodec[T]) Decode([]byte) (T, error) { var zero T; return zero, c.err }

// unnamedCodec declares no identity tag, so composeTag reports "" for a stack
// built over it.
type unnamedCodec[T any] struct{}

func (unnamedCodec[T]) Encode(v T) ([]byte, error) { return nil, nil }
func (unnamedCodec[T]) Decode([]byte) (T, error)   { var zero T; return zero, nil }

// untaggedCodec names itself but declares the empty tag, the other way a stack
// becomes unspecified.
type untaggedCodec[T any] struct{}

func (untaggedCodec[T]) Encode(v T) ([]byte, error) { return nil, nil }
func (untaggedCodec[T]) Decode([]byte) (T, error)   { var zero T; return zero, nil }
func (untaggedCodec[T]) CodecName() string          { return "" }

// TestEncodeInnerFailureIsPropagatedUnchanged pins the stacked encoder's error
// branch: an inner codec that cannot serialize the value is reported as its own
// error, not wrapped and not replaced by a nil-encoded payload.
func TestEncodeInnerFailureIsPropagatedUnchanged(t *testing.T) {
	boom := errors.New("inner encode exploded")
	codec := New(failingCodec[string]{err: boom}, func(b []byte) ([]byte, error) { return b, nil }, func(b []byte) ([]byte, error) { return b, nil })

	data, err := codec.Encode("value")
	if !errors.Is(err, boom) {
		t.Fatalf("Encode with a failing inner codec = %v, want %v", err, boom)
	}
	if data != nil {
		t.Fatalf("Encode with a failing inner codec = %q, want no bytes", data)
	}
}

// TestDecodeRestoreFailureIsPropagatedUnchanged pins the stacked decoder's
// error branch: a restore step that rejects the stored bytes is reported as its
// own error and the inner codec is never asked to decode the restored payload.
func TestDecodeRestoreFailureIsPropagatedUnchanged(t *testing.T) {
	boom := errors.New("restore exploded")
	codec := New(erroringRestoreCodec{}, func(b []byte) ([]byte, error) { return b, nil }, func([]byte) ([]byte, error) { return nil, boom })

	got, err := codec.Decode([]byte("stored"))
	if !errors.Is(err, boom) {
		t.Fatalf("Decode with a failing restore = %v, want %v", err, boom)
	}
	if got != "" {
		t.Fatalf("Decode with a failing restore = %q, want the zero value", got)
	}
}

// erroringRestoreCodec is never reached: the restore step must fail first.
type erroringRestoreCodec struct{}

func (erroringRestoreCodec) Encode(string) ([]byte, error) { return nil, errors.New("unused") }
func (erroringRestoreCodec) Decode([]byte) (string, error) { return "decoded", nil }

// TestCodecNameLeavesAnUnspecifiedInnerCodecUnspecified pins composeTag's ""
// return: stacking over a codec that declares no identity must read as
// unspecified rather than manufacturing "binary+json" or a bare "binary" that
// would later compare unequal to the bytes actually stored.
func TestCodecNameLeavesAnUnspecifiedInnerCodecUnspecified(t *testing.T) {
	for _, tc := range []struct {
		name string
		next cas.Codec[string]
	}{
		{"an unnamed inner codec", unnamedCodec[string]{}},
		{"an inner codec with an empty tag", untaggedCodec[string]{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codec := New(tc.next, func(b []byte) ([]byte, error) { return b, nil }, func(b []byte) ([]byte, error) { return b, nil })
			if got := codec.CodecName(); got != "" {
				t.Fatalf("CodecName() = %q, want \"\" for %s", got, tc.name)
			}
		})
	}
}
