package cbor

import (
	"math"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

func TestValueCodecCoverage(t *testing.T) {
	codec := NewValue()
	v := map[string]any{
		"ok":     true,
		"count":  int64(7),
		"n":      nil,
		"items":  []any{1, "two", []byte("x")},
		"nested": map[string]string{"b": "two", "a": "one"},
	}
	data, err := codec.Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if gotMap, ok := got.(map[string]any); !ok || len(gotMap) != 5 {
		t.Fatalf("Decode unexpected result: %#v", got)
	}
	if _, err := codec.Encode(struct{}{}); err == nil {
		t.Fatal("unsupported struct should fail")
	}
	if _, err := encodeFloat64(math.NaN()); err == nil {
		t.Fatal("NaN should fail")
	}
	if _, err := encodeFloat64(math.Inf(1)); err == nil {
		t.Fatal("Inf should fail")
	}
	if _, err := estimateAnySize(struct{}{}); err == nil {
		t.Fatal("estimateAnySize(struct{}) should fail")
	}

	values := []any{
		nil,
		true,
		false,
		int(-5),
		int8(4),
		int16(-7),
		int32(8),
		int64(-9),
		uint(10),
		uint8(11),
		uint16(12),
		uint32(13),
		uint64(14),
		float32(1.25),
		float64(2.5),
		"hello",
		[]byte("hi"),
		[]string{"a", "b"},
		[]any{"x", 1, nil},
		map[string]any{"a": 1, "b": "two"},
		map[string]string{"b": "two", "a": "one"},
	}
	for _, val := range values {
		encoded, err := appendEncodedValue(nil, val)
		if err != nil {
			t.Fatalf("appendEncodedValue(%T) = %v", val, err)
		}
		if _, err := estimateAnySize(val); err != nil {
			t.Fatalf("estimateAnySize(%T) = %v", val, err)
		}
		if _, _, err := decodeOne(encoded); err != nil {
			t.Fatalf("decodeOne(%T) = %v", val, err)
		}
	}
}

func TestCBORInternalHelpers(t *testing.T) {
	if got := lenMajorHeader(0, 0); got != 1 {
		t.Fatalf("lenMajorHeader(0,0)=%d, want 1", got)
	}
	if got := lenMajorHeader(3, 24); got != 2 {
		t.Fatalf("lenMajorHeader(3,24)=%d, want 2", got)
	}
	if got := lenMajorHeader(3, 300); got != 3 {
		t.Fatalf("lenMajorHeader(3,300)=%d, want 3", got)
	}
	if got := lenMajorHeader(3, 70000); got != 5 {
		t.Fatalf("lenMajorHeader(3,70000)=%d, want 5", got)
	}
	if got := lenMajorHeader(3, 1<<40); got != 9 {
		t.Fatalf("lenMajorHeader(3,1<<40)=%d, want 9", got)
	}

	buf := appendMajor(nil, 5, 2)
	if len(buf) != 1 || buf[0] != 0xa2 {
		t.Fatalf("appendMajor map header = %#v, want %#v", buf, []byte{0xa2})
	}

	if err := checkDecodeOne(t, appendBytes(nil, []byte("hi"))); err != nil {
		t.Fatal(err)
	}
	if err := checkDecodeOne(t, appendStringBytes(nil, []byte("name"))); err != nil {
		t.Fatal(err)
	}
	if data, err := appendArrayValue(nil, []any{1, "a", true}); err != nil {
		t.Fatal(err)
	} else if err := checkDecodeOne(t, data); err != nil {
		t.Fatal(err)
	}
	if err := checkDecodeOne(t, []byte{0x82, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	if err := checkDecodeOne(t, []byte{0xa1, 0x61, 'a', 0x01}); err != nil {
		t.Fatal(err)
	}
}

func checkDecodeOne(t *testing.T, data []byte) error {
	t.Helper()
	v, rest, err := decodeOne(data)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errTrailingData
	}
	if v == nil {
		return nil
	}
	return nil
}

var errTrailingData = &testErr{"trailing data"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestReadLengthAndDecoderErrors(t *testing.T) {
	pos := 0
	if _, err := readLength([]byte{0x18, 0x2a}, &pos, 24); err != nil || pos != 1 {
		t.Fatal("readLength(addl=24) should succeed")
	}
	pos = 0
	if _, err := readLength([]byte{0x01, 0x00}, &pos, 25); err != nil || pos != 2 {
		t.Fatal("readLength(addl=25) should succeed")
	}
	pos = 0
	if _, err := readLength([]byte{0x00, 0x00, 0x00, 0x01}, &pos, 26); err != nil || pos != 4 {
		t.Fatal("readLength(addl=26) should succeed")
	}
	pos = 0
	if _, err := readLength([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}, &pos, 27); err != nil || pos != 8 {
		t.Fatal("readLength(addl=27) should succeed")
	}
	for _, bad := range []struct {
		data []byte
		addl byte
	}{{[]byte{}, 24}, {[]byte{0x00}, 25}, {[]byte{0x00, 0x00}, 26}, {[]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 27}, {nil, 31}} {
		pos = 0
		if _, err := readLength(bad.data, &pos, bad.addl); err == nil {
			t.Fatalf("readLength addl=%d with %v should fail", bad.addl, bad.data)
		}
	}

	if _, _, err := decodeOne(nil); err == nil {
		t.Fatal("empty input should error")
	}
	if _, _, err := decodeOne([]byte{0x82, 0x01}); err == nil {
		t.Fatal("truncated array should error")
	}
	if _, _, err := decodeOne([]byte{0xA2, 0x01, 0x01}); err == nil {
		t.Fatal("map key type should fail")
	}
	for _, validSimple := range [][]byte{{0xf4}, {0xf5}, {0xf6}} {
		if _, _, err := decodeOne(validSimple); err != nil {
			t.Fatalf("simple value %x should decode: %v", validSimple, err)
		}
	}
	if _, _, err := decodeOne([]byte{0xF8, 0x00}); err == nil {
		t.Fatal("unsupported simple value should error")
	}
	if _, _, err := decodeOne([]byte{0xF8, 0x1f}); err == nil {
		t.Fatal("unsupported simple value 31 should error")
	}
	if _, _, err := decodeOne([]byte{0xFB, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}); err == nil {
		t.Fatal("truncated float64 should error")
	}
	if _, _, err := decodeOne([]byte{0xF9, 0x3c, 0x00}); err != nil {
		t.Fatal("float16 value should decode without error")
	}
	if _, _, err := decodeOne([]byte{0xC0}); err == nil {
		t.Fatal("unsupported major type should error")
	}
	if _, err := decodeMapValue([]byte{0x01}); err == nil {
		t.Fatal("decodeMapValue should reject non-map")
	}
	if got, err := decodeMapValue([]byte{0xa1, 0x61, 'a', 0x01}); err != nil || got["a"] != int64(1) {
		t.Fatalf("decodeMapValue valid map = %#v, err=%v", got, err)
	}
	if encoded, err := encodeMapValue(nil); err != nil || len(encoded) == 0 {
		t.Fatalf("encodeMapValue(nil) = %x, %v", encoded, err)
	}
	if _, err := decodeAny([]byte{0x01, 0x02}); err == nil {
		t.Fatal("decodeAny should reject trailing data")
	}
}

func TestCBORFallbackandHelpers(t *testing.T) {
	base := jsoncodec.New[map[string]any]()
	codec := New(base, nil, nil)
	data, err := codec.Encode(map[string]any{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(data); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRaw[bool](nil, func([]byte) (bool, error) { return false, nil }).Encode(true); err == nil {
		t.Fatal("nil encoder should fail")
	}
	if _, err := NewRaw[bool](func(bool) ([]byte, error) { return nil, nil }, nil).Decode(nil); err == nil {
		t.Fatal("nil decoder should fail")
	}
	if _, err := encodeFloat64Into(nil, 1.5); err != nil {
		t.Fatal("float64 encode should work")
	}
	if _, err := encodeFloat64Into(nil, math.NaN()); err == nil {
		t.Fatal("NaN should not encode")
	}
	if _, err := encodeInt64(-7); err != nil {
		t.Fatal("encodeInt64(-7) should work")
	}
	if _, err := encodeUint64(255); err != nil {
		t.Fatal("encodeUint64 should work")
	}
	if _, err := encodeStringBytes([]byte("abc")); err != nil {
		t.Fatal("encodeStringBytes should work")
	}
	if _, err := encodeBytes([]byte("abc")); err != nil {
		t.Fatal("encodeBytes should work")
	}
	if out, err := appendMapValue(nil, nil); err != nil || len(out) != 1 || out[0] != 0xa0 {
		t.Fatalf("appendMapValue(nil) = %x, %v, want single empty map", out, err)
	}
	if got, err := decodeMapValue([]byte{0xa1, 0x61, 'a', 0x01}); err != nil || got["a"] != int64(1) {
		t.Fatalf("decodeMapValue valid = %#v, %v", got, err)
	}
	for _, length := range []uint64{24, 255, 300, 65535, 65536, 1 << 32} {
		if out := appendMajor(nil, 3, length); len(out) == 0 {
			t.Fatalf("appendMajor should emit bytes for length %d", length)
		}
	}
}

func TestCBORRawHelperExhaustion(t *testing.T) {
	if out, err := encodeInt64Into(nil, 42); err != nil || out[0] != 0x18 || out[1] != 0x2a {
		t.Fatalf("encodeInt64Into 42 = %x, %v", out, err)
	}
	if out, err := encodeInt64Into(nil, -1); err != nil || out[0] != 0x20 {
		t.Fatalf("encodeInt64Into(-1) = %x, %v", out, err)
	}
	if out, err := encodeUint64Into(nil, 255); err != nil || len(out) != 2 || out[0] != 0x18 || out[1] != 0xff {
		t.Fatalf("encodeUint64Into(255) = %x, %v", out, err)
	}
	if out, err := encodeFloat64(2.5); err != nil || len(out) != 9 {
		t.Fatalf("encodeFloat64(2.5) = %x, %v", out, err)
	}
	if _, err := encodeFloat64(math.Inf(1)); err == nil {
		t.Fatal("encodeFloat64(inf) should fail")
	}
	if _, err := appendArrayValue(nil, []any{1, 2, 3}); err != nil {
		t.Fatal("appendArrayValue should succeed")
	}
	if _, err := appendMapValue(nil, map[string]any{"key": "value"}); err != nil {
		t.Fatal("appendMapValue should succeed")
	}
	if _, err := estimateAnySize(map[string]string{"a": "b"}); err != nil {
		t.Fatal("estimateAnySize(map[string]string) should succeed")
	}
	if _, err := estimateAnySize([]string{"x", "y"}); err != nil {
		t.Fatal("estimateAnySize([]string) should succeed")
	}
	if out, err := encodeArray([]any{1, 2, 3}); err != nil || len(out) == 0 {
		t.Fatalf("encodeArray = %x, %v", out, err)
	}
	if out, err := encodeInt64(5); err != nil || out[0] != 0x05 {
		t.Fatalf("encodeInt64(5) = %x, %v", out, err)
	}
	if out, err := encodeInt64(-5); err != nil || len(out) == 0 {
		t.Fatalf("encodeInt64(-5) = %x, %v", out, err)
	}
	if got, err := decodeAny([]byte{0xa1, 0x61, 'a', 0x01}); err != nil || got.(map[string]any)["a"] != int64(1) {
		t.Fatalf("decodeAny valid = %#v, %v", got, err)
	}
	if _, err := decodeAny([]byte{0x01, 0x02}); err == nil {
		t.Fatal("decodeAny trailing data should error")
	}
	if _, err := NewWithNext[[]string](jsoncodec.New[[]string](), func(v []string) ([]byte, error) {
		arr, err := encodeArray([]any{"a", "b"})
		if err != nil {
			return nil, err
		}
		return arr, nil
	}, func(data []byte) ([]string, error) { return []string{"a", "b"}, nil }).Encode([]string{"a", "b"}); err != nil {
		t.Fatal("NewWithNext should allow wrapped encode path")
	}
}
