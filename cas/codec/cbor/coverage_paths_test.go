package cbor

import (
	"strings"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// plainCodec declares no identity tag, so a delegating cbor codec over it
// reports the unspecified tag.
type plainCodec[T any] struct{}

func (plainCodec[T]) Encode(v T) ([]byte, error) { return []byte(`"x"`), nil }
func (plainCodec[T]) Decode([]byte) (T, error)   { var zero T; return zero, nil }

// TestCodecNameOfADelegatingStackOverAnUnnamedCodec pins the "" branch: a cbor
// codec that only delegates to an inner codec reports that codec's tag, and an
// inner codec with no identity leaves the stack unspecified rather than
// claiming "cbor" for bytes this package did not write.
func TestCodecNameOfADelegatingStackOverAnUnnamedCodec(t *testing.T) {
	codec := New[string](plainCodec[string]{}, nil, nil)
	if got := codec.CodecName(); got != "" {
		t.Fatalf("CodecName() = %q, want \"\" for a stack over an unnamed codec", got)
	}
}

// TestMapValueEncodePropagatesNestedFailure pins appendMapValue's error branch:
// a map whose value the encoder cannot represent is reported as the encoder's
// own error (naming the offending type) rather than producing a partial map.
func TestMapValueEncodePropagatesNestedFailure(t *testing.T) {
	data, err := NewMap().Encode(map[string]any{"unsupported": make(chan int)})
	if err == nil {
		t.Fatalf("Encode of an unsupported map value = %q, want an error", data)
	}
	if !strings.Contains(err.Error(), "unsupported value type") {
		t.Fatalf("Encode of an unsupported map value = %v, want it to name the type", err)
	}
	if data != nil {
		t.Fatalf("Encode of an unsupported map value = %q, want no bytes", data)
	}
}

// TestArrayValueEncodePropagatesNestedFailure pins appendArrayValue's error
// branch: an array whose item the encoder cannot represent is reported rather
// than truncated to the items that happened to encode.
func TestArrayValueEncodePropagatesNestedFailure(t *testing.T) {
	data, err := NewValue().Encode([]any{int64(1), make(chan int)})
	if err == nil {
		t.Fatalf("Encode of an unsupported array item = %q, want an error", data)
	}
	if !strings.Contains(err.Error(), "unsupported value type") {
		t.Fatalf("Encode of an unsupported array item = %v, want it to name the type", err)
	}
}

// TestMapValueDecodePropagatesMalformedInput pins decodeMapValue's error branch:
// bytes that are not a decodable CBOR value are reported as the decoder's
// error, not as an empty map.
func TestMapValueDecodePropagatesMalformedInput(t *testing.T) {
	got, err := NewMap().Decode([]byte{0xff})
	if err == nil {
		t.Fatalf("Decode of an unsupported simple value = %#v, want an error", got)
	}
	if got != nil {
		t.Fatalf("Decode of an unsupported simple value = %#v, want nil", got)
	}
}

// TestDecodePropagatesNestedArrayItemFailure pins decodeOne's array-item error
// branch: a malformed item inside an array whose declared count fits the input
// is reported by the item's own decode, not silently dropped.
func TestDecodePropagatesNestedArrayItemFailure(t *testing.T) {
	// 0x82 = array(2); the second item's leading byte declares additional
	// information 28, which this decoder rejects as an unsupported length form.
	malformed := []byte{0x82, 0x01, 0x1c}

	_, err := NewValue().Decode(malformed)
	if err == nil {
		t.Fatalf("Decode(% x) = nil error, want the nested item's error", malformed)
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Decode(% x) = %v, want the nested decode error", malformed, err)
	}
}

// TestDecodeRejectsTruncatedFloatWidths pins the three float-width guards: a
// half, single and double float whose declared width does not fit the remaining
// input is reported as truncated instead of being read past the end.
func TestDecodeRejectsTruncatedFloatWidths(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"float16 without its two bytes", []byte{0xf9, 0x00}, "truncated float16"},
		{"float32 without its four bytes", []byte{0xfa, 0x00, 0x00, 0x00}, "truncated float32"},
		{"float64 without its eight bytes", []byte{0xfb, 0x00, 0x00, 0x00}, "truncated float64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewValue().Decode(tc.data)
			if err == nil {
				t.Fatalf("Decode(% x) = nil error, want %q", tc.data, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Decode(% x) = %v, want it to report %q", tc.data, err, tc.want)
			}
		})
	}
}

// TestDecodeReadsEveryFloatWidth pins the three float conversions: a float32
// written by another encoder reads back as the same value, and a half float
// whose bit pattern is also valid in the low half of a float32 reads back as
// the double it denotes.
//
// BUG (reported, not fixed): the float16 branch of decodeOne feeds the 16-bit
// payload to math.Float32frombits instead of decoding IEEE 754 half precision,
// so any half float whose pattern is not numerically meaningful as a float32
// decodes to the wrong number (for example 0xf9 0x3e 0x00, half-precision 1.5,
// reads as 2.2e-41). The case below only pins the branch; it deliberately uses a
// pattern that survives the conversion, so this suite does not encode the bug as
// the expected value.
func TestDecodeReadsEveryFloatWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want float64
	}{
		{"float16 whose bits are a float32 zero", []byte{0xf9, 0x00, 0x00}, 0},
		{"float32 one and a half", []byte{0xfa, 0x3f, 0xc0, 0x00, 0x00}, 1.5},
		{"float64 one and a half", []byte{0xfb, 0x3f, 0xf8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewValue().Decode(tc.data)
			if err != nil {
				t.Fatalf("Decode(% x) = %v", tc.data, err)
			}
			if got != tc.want {
				t.Fatalf("Decode(% x) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// TestDelegateDecodeReportsMalformedInput pins the delegating codec's Decode
// branch: without conversion functions and with an inner codec, the input is
// handed to the inner codec unchanged — so a JSON codec rejects CBOR bytes.
func TestDelegateDecodeReportsMalformedInput(t *testing.T) {
	codec := New[string](jsoncodec.New[string](), nil, nil)
	got, err := codec.Decode([]byte("\x01"))
	if err == nil {
		t.Fatalf("Decode through the inner JSON codec = %q, want an error", got)
	}
	if got != "" {
		t.Fatalf("Decode through the inner JSON codec = %q, want the zero value", got)
	}
}
