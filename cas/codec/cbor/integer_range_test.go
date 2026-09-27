package cbor

import (
	"errors"
	"math"
	"testing"
)

// TestDecodeRejectsUnsignedArgumentsPastInt64 is the go-cask#363 regression.
// CBOR major type 0 carries an *unsigned* argument, and the value model this
// package decodes into is int64, so an argument at or above 2^63 has no
// representation: it was converted with int64(length) regardless, so
// Encode(uint64(1<<63)) came back as a negative int64 and the round-trip
// contract ("Decode(Encode(v)) == v") was broken silently. The boundary is
// pinned from both sides — 2^63-1 is MaxInt64 and still decodes, 2^63 and above
// are ErrIntegerRange — and the two wire values that collided (uint64 max and
// -1) are pinned as never decoding to the same Go value.
func TestDecodeRejectsUnsignedArgumentsPastInt64(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		want   int64
		reason string
	}{
		{"2^63-1 is MaxInt64", majorArg(0, math.MaxInt64), math.MaxInt64, ""},
		{"2^63 has no int64 form", majorArg(0, 1<<63), 0, "2^63 is one past MaxInt64"},
		{"uint64 max has no int64 form", majorArg(0, math.MaxUint64), 0, "uint64 max is one past MaxInt64"},
		{"2^63 written short form", []byte{0x1b, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 0, "the same value in the eight-byte head"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rest, err := decodeOne(tt.data, 0)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("decodeOne(%x) = %v, want success", tt.data, err)
				}
				if got != tt.want {
					t.Fatalf("decodeOne(%x) = %#v, want int64(%d)", tt.data, got, tt.want)
				}
				if len(rest) != 0 {
					t.Fatalf("decodeOne(%x) left %d trailing bytes", tt.data, len(rest))
				}
				if _, err := NewValue().Decode(tt.data); err != nil {
					t.Fatalf("NewValue().Decode(%x) = %v, want success", tt.data, err)
				}
				return
			}
			if !errors.Is(err, ErrIntegerRange) {
				t.Fatalf("decodeOne(%x) = (%#v, %v), want ErrIntegerRange: %s", tt.data, got, err, tt.reason)
			}
			if got != nil {
				t.Fatalf("decodeOne(%x) = %#v beside the error, want no value", tt.data, got)
			}
			if _, err := NewValue().Decode(tt.data); !errors.Is(err, ErrIntegerRange) {
				t.Fatalf("NewValue().Decode(%x) = %v, want ErrIntegerRange", tt.data, err)
			}
			if _, err := NewMap().Decode(tt.data); !errors.Is(err, ErrIntegerRange) {
				t.Fatalf("NewMap().Decode(%x) = %v, want ErrIntegerRange", tt.data, err)
			}
		})
	}
}

// TestDecodeRejectsMajorTypeOneArgumentsPastInt64 pins the negative-numeral arm
// (major type 1, whose value is -1-argument): the argument may be at most 2^63-1
// because its value is at least MinInt64 exactly there, and one past that has no
// int64 form. The accepting side is the boundary itself, so the guard cannot be
// tightened into rejecting a representable value.
func TestDecodeRejectsMajorTypeOneArgumentsPastInt64(t *testing.T) {
	tests := []struct {
		name string
		arg  uint64
		want int64
		ok   bool
	}{
		{"argument 2^63-1 is MinInt64", math.MaxInt64, math.MinInt64, true},
		{"argument 2^62 is a large negative", 1 << 62, -(1 << 62) - 1, true},
		{"argument 0 is -1", 0, -1, true},
		{"argument 2^63 has no int64 form", 1 << 63, 0, false},
		{"argument uint64 max has no int64 form", math.MaxUint64, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := majorArg(1, tt.arg)
			got, rest, err := decodeOne(data, 0)
			if !tt.ok {
				if !errors.Is(err, ErrIntegerRange) {
					t.Fatalf("decodeOne(%x) = (%#v, %v), want ErrIntegerRange", data, got, err)
				}
				if got != nil {
					t.Fatalf("decodeOne(%x) = %#v beside the error, want no value", data, got)
				}
				if _, err := NewValue().Decode(data); !errors.Is(err, ErrIntegerRange) {
					t.Fatalf("NewValue().Decode(%x) = %v, want ErrIntegerRange", data, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeOne(%x) = %v, want success", data, err)
			}
			if got != tt.want {
				t.Fatalf("decodeOne(%x) = %#v, want int64(%d)", data, got, tt.want)
			}
			if len(rest) != 0 {
				t.Fatalf("decodeOne(%x) left %d trailing bytes", data, len(rest))
			}
		})
	}
}

// TestCollidingWireValuesStayDistinct pins the data-integrity consequence the
// round trip states in the abstract: CBOR uint64 max (0x1b ffffffffffffffff) and
// CBOR -1 (0x20) both decoded to int64(-1), so two distinct stored byte strings
// resolved to one Go value and a store could not tell them apart. One of them now
// fails, so no two wire values share a decoded value.
func TestCollidingWireValuesStayDistinct(t *testing.T) {
	negativeOne, err := NewValue().Decode([]byte{0x20})
	if err != nil {
		t.Fatalf("Decode(0x20) = %v, want int64(-1)", err)
	}
	if negativeOne != int64(-1) {
		t.Fatalf("Decode(0x20) = %#v, want int64(-1)", negativeOne)
	}
	if _, err := NewValue().Decode(majorArg(0, math.MaxUint64)); !errors.Is(err, ErrIntegerRange) {
		t.Fatalf("Decode(uint64 max) = %v, want ErrIntegerRange rather than the int64(-1) it used to decode to", err)
	}
}

// TestRoundTripAcrossTheIntegerBoundary is the acceptance shape the issue names:
// for every boundary value the encoder accepts, either the decoded value equals
// the encoded one or the decode is a named error — never a silently different
// value. int64 boundaries round-trip exactly; a uint64 past MaxInt64 fails.
func TestRoundTripAcrossTheIntegerBoundary(t *testing.T) {
	t.Run("int64 boundaries round-trip", func(t *testing.T) {
		for _, want := range []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64} {
			data, err := NewValue().Encode(want)
			if err != nil {
				t.Fatalf("Encode(int64 %d) = %v", want, err)
			}
			got, err := NewValue().Decode(data)
			if err != nil {
				t.Fatalf("Decode(Encode(int64 %d)) = %v", want, err)
			}
			if got != want {
				t.Fatalf("round trip of int64 %d = %#v, want the same value", want, got)
			}
		}
	})

	t.Run("uint64 at or past 2^63 is a named error", func(t *testing.T) {
		for _, value := range []uint64{1 << 63, 1<<63 + 1, math.MaxUint64} {
			data, err := NewValue().Encode(value)
			if err != nil {
				t.Fatalf("Encode(uint64 %d) = %v, want the encoder to accept the value it can write", value, err)
			}
			_, err = NewValue().Decode(data)
			if !errors.Is(err, ErrIntegerRange) {
				t.Fatalf("Decode(Encode(uint64 %d)) = %v, want ErrIntegerRange", value, err)
			}
		}
	})

	t.Run("uint64 at or below MaxInt64 round-trips", func(t *testing.T) {
		for _, value := range []uint64{0, 1, 1 << 62, math.MaxInt64} {
			data, err := NewValue().Encode(value)
			if err != nil {
				t.Fatalf("Encode(uint64 %d) = %v", value, err)
			}
			got, err := NewValue().Decode(data)
			if err != nil {
				t.Fatalf("Decode(Encode(uint64 %d)) = %v", value, err)
			}
			if got != int64(value) {
				t.Fatalf("round trip of uint64 %d = %#v, want int64(%d)", value, got, value)
			}
		}
	})
}
