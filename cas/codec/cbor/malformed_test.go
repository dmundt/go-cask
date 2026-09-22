package cbor

import (
	"encoding/binary"
	"testing"
)

// majorArg builds a CBOR head for major type mt carrying arg in the
// eight-byte "additional information 27" form, so a hostile 64-bit length
// reaches the decoder in a single byte.
func majorArg(mt byte, arg uint64) []byte {
	head := make([]byte, 9)
	head[0] = mt<<5 | 27
	binary.BigEndian.PutUint64(head[1:], arg)
	return head
}

// assertDecodeFails requires every decode entry point to report an error for
// malformed input; a panic fails the test through the deferred recover.
func assertDecodeFails(t *testing.T, data []byte) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("decoding %x panicked: %v", data, r)
		}
	}()
	if _, _, err := decodeOne(data); err == nil {
		t.Fatalf("decodeOne(%x) = nil error, want error", data)
	}
	if _, err := NewValue().Decode(data); err == nil {
		t.Fatalf("NewValue().Decode(%x) = nil error, want error", data)
	}
}

// TestDecodeMalformedLengthHeaders covers declared lengths that exceed the
// bytes actually present. Both overflow shapes matter: a length near 2^63
// overflows the int conversion and produced a negative slice bound, and a
// length near 2^61 was used directly as a slice/map capacity hint.
func TestDecodeMalformedLengthHeaders(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"byte string truncated", []byte{0x43, 0x01, 0x02}},
		{"byte string length 2^61", majorArg(2, 1<<61)},
		{"byte string length 2^63", majorArg(2, 1<<63)},
		{"byte string length max uint64", majorArg(2, 1<<64-1)},
		{"text string truncated", []byte{0x63, 'a'}},
		{"text string length 2^61", majorArg(3, 1<<61)},
		{"text string length 2^63", majorArg(3, 1<<63)},
		{"text string length max uint64", majorArg(3, 1<<64-1)},
		{"array count 2^61", majorArg(4, 1<<61)},
		{"array count 2^63", majorArg(4, 1<<63)},
		{"array count max uint64", majorArg(4, 1<<64-1)},
		{"array truncated", []byte{0x83, 0x01, 0x02}},
		{"map count 2^61", majorArg(5, 1<<61)},
		{"map count 2^63", majorArg(5, 1<<63)},
		{"map count max uint64", majorArg(5, 1<<64-1)},
		{"map truncated key", []byte{0xa1, 0x61, 'a'}},
		{"map missing pair", []byte{0xa2, 0x61, 'a', 0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeFails(t, tt.data)
		})
	}
}

// TestDecodeLengthBoundaryAccepted pins the accepting side of the guard: a
// declared length that matches the bytes remaining must still decode.
func TestDecodeLengthBoundaryAccepted(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"byte string exact", append(majorArg(2, 2), 0xaa, 0xbb)},
		{"text string exact", append(majorArg(3, 2), 'h', 'i')},
		{"byte string empty", majorArg(2, 0)},
		{"array exact", append(majorArg(4, 3), 0x01, 0x02, 0x03)},
		{"array empty", majorArg(4, 0)},
		{"map exact", append(majorArg(5, 1), 0x61, 'a', 0x01)},
		{"map empty", majorArg(5, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, rest, err := decodeOne(tt.data); err != nil {
				t.Fatalf("decodeOne(%x) = %v, want success", tt.data, err)
			} else if len(rest) != 0 {
				t.Fatalf("decodeOne(%x) left %d trailing bytes", tt.data, len(rest))
			}
			if _, err := NewValue().Decode(tt.data); err != nil {
				t.Fatalf("NewValue().Decode(%x) = %v, want success", tt.data, err)
			}
		})
	}
}
