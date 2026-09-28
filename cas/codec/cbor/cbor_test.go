package cbor_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas/codec/cbor"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type doc struct {
	Title  string   `cbor:"title"`
	Count  int      `cbor:"count"`
	Active bool     `cbor:"active"`
	Body   []byte   `cbor:"body"`
	Tags   []string `cbor:"tags"`
}

func TestRoundTripMap(t *testing.T) {
	codec := cbor.NewMap()
	orig := map[string]any{
		"name":  "demo",
		"count": 42,
		"ok":    true,
		"data":  []byte("hi"),
		"list":  []any{1, 2, "three", nil},
	}

	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalizeMap(got), normalizeMap(orig)) {
		t.Fatalf("round-trip mismatch: %#v != %#v", normalizeMap(got), normalizeMap(orig))
	}
}

func normalizeMap(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeMap(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = normalizeMap(val)
		}
		return out
	case []byte:
		return []byte(x)
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case uint:
		return uint64(x)
	case uint8:
		return uint64(x)
	case uint16:
		return uint64(x)
	case uint32:
		return uint64(x)
	case uint64:
		return x
	case float32:
		return float64(x)
	case float64:
		return x
	default:
		return x
	}
}

func encodeDoc(v doc) ([]byte, error) {
	return cbor.NewMap().Encode(map[string]any{
		"title":  v.Title,
		"count":  v.Count,
		"active": v.Active,
		"body":   v.Body,
		"tags":   v.Tags,
	})
}

func decodeDoc(data []byte) (doc, error) {
	m, err := cbor.NewMap().Decode(data)
	if err != nil {
		return doc{}, err
	}
	out := doc{
		Title:  m["title"].(string),
		Count:  int(m["count"].(int64)),
		Active: m["active"].(bool),
		Body:   m["body"].([]byte),
	}
	for _, tag := range m["tags"].([]any) {
		out.Tags = append(out.Tags, tag.(string))
	}
	return out, nil
}

func TestRoundTripStruct(t *testing.T) {
	codec := cbor.NewRaw[doc](encodeDoc, decodeDoc)
	orig := doc{
		Title:  "hello",
		Count:  7,
		Active: true,
		Body:   []byte("payload"),
		Tags:   []string{"a", "b"},
	}

	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != orig.Title || got.Count != orig.Count || got.Active != orig.Active || !bytes.Equal(got.Body, orig.Body) || !reflect.DeepEqual(got.Tags, orig.Tags) {
		t.Fatalf("struct round-trip mismatch: %#v != %#v", got, orig)
	}
}

func TestDeterministicMapEncoding(t *testing.T) {
	codec := cbor.NewMap()
	left := map[string]any{"b": 2, "a": 1}
	right := map[string]any{"a": 1, "b": 2}

	leftData, err := codec.Encode(left)
	if err != nil {
		t.Fatal(err)
	}
	rightData, err := codec.Encode(right)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftData, rightData) {
		t.Fatalf("map encoding must be deterministic: %x != %x", leftData, rightData)
	}
}

func TestNextCodecFallback(t *testing.T) {
	base := jsoncodec.New[doc]()
	codec := cbor.New(base, nil, nil)
	orig := doc{Title: "next", Count: 3, Active: true, Body: []byte("payload")}

	data, err := codec.Encode(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != orig.Title || got.Count != orig.Count || got.Active != orig.Active || !bytes.Equal(got.Body, orig.Body) {
		t.Fatalf("next codec fallback mismatch: %#v != %#v", got, orig)
	}
}

// TestDecodedByteStringsDoNotAliasInput pins the ownership contract of #382: a
// decoded []byte field is a copy of its own bytes, not a sub-slice of the buffer
// Decode was handed. The aliasing would be two defects at once — a retained value
// would keep the whole object buffer alive, and reusing that buffer (a store's
// read scratch) would mutate a value already decoded.
func TestDecodedByteStringsDoNotAliasInput(t *testing.T) {
	codec := cbor.NewMap()
	data, err := codec.Encode(map[string]any{"body": []byte("payload")})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	body, ok := decoded["body"].([]byte)
	if !ok {
		t.Fatalf("decoded body = %T, want []byte", decoded["body"])
	}

	// Overwrite the whole input buffer with a sentinel: a decode that aliased it
	// would now report the sentinel, not the encoded field.
	for i := range data {
		data[i] = 0x00
	}
	if want := []byte("payload"); !bytes.Equal(body, want) {
		t.Fatalf("decoded byte string changed with its input buffer: got %q, want %q", body, want)
	}

	// The value model codec decodes a byte string through the same arm, so it
	// owns the same contract for a bare []byte value.
	raw, err := cbor.NewValue().Encode([]byte("bare"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := cbor.NewValue().Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	bare, ok := value.([]byte)
	if !ok {
		t.Fatalf("decoded value = %T, want []byte", value)
	}
	for i := range raw {
		raw[i] = 0x00
	}
	if want := []byte("bare"); !bytes.Equal(bare, want) {
		t.Fatalf("decoded NewValue byte string changed with its input buffer: got %q, want %q", bare, want)
	}
}

// TestCodecName pins the identity tag: "cbor" when this codec owns the value's
// conversion, and the inner codec's own tag when it only delegates to one
// (the bytes are the inner codec's, so claiming cbor would read as a mismatch
// against an identically encoded object).
func TestCodecName(t *testing.T) {
	if got := cbor.NewMap().CodecName(); got != "cbor" {
		t.Fatalf("NewMap CodecName() = %q, want cbor", got)
	}
	raw := cbor.NewRaw[doc](encodeDoc, decodeDoc)
	if got := raw.CodecName(); got != "cbor" {
		t.Fatalf("NewRaw CodecName() = %q, want cbor", got)
	}
	delegating := cbor.New(jsoncodec.New[doc](), nil, nil)
	if got := delegating.CodecName(); got != "json" {
		t.Fatalf("delegating CodecName() = %q, want the inner tag json", got)
	}
}

// TestNewRejectsInnerCodecWithConversion pins the constructor contract of #270:
// New builds either a delegating codec or a converting one. An inner codec
// together with conversion functions cannot work — encode/decode already produce
// the stored bytes — so it is reported instead of silently ignoring the inner
// codec, which is what used to happen.
func TestNewRejectsInnerCodecWithConversion(t *testing.T) {
	mixed := cbor.New(jsoncodec.New[doc](), encodeDoc, decodeDoc)
	if _, err := mixed.Encode(doc{Title: "mixed"}); err == nil {
		t.Fatal("Encode with both an inner codec and conversion functions must be refused")
	} else if !strings.Contains(err.Error(), "inner codec") {
		t.Fatalf("Encode error = %v, want it to name the conflict", err)
	}
	if _, err := mixed.Decode([]byte(`{}`)); err == nil {
		t.Fatal("Decode with both an inner codec and conversion functions must be refused")
	}

	// New(nil, encode, decode) is the documented equivalent of NewRaw, and
	// New(next, nil, nil) keeps delegating.
	converting := cbor.New[doc](nil, encodeDoc, decodeDoc)
	data, err := converting.Encode(doc{Title: "converting"})
	if err != nil {
		t.Fatalf("New(nil, encode, decode).Encode = %v, want nil", err)
	}
	if _, err := converting.Decode(data); err != nil {
		t.Fatalf("New(nil, encode, decode).Decode = %v, want nil", err)
	}
	if got := converting.CodecName(); got != "cbor" {
		t.Fatalf("converting CodecName() = %q, want cbor", got)
	}
	delegating := cbor.New(jsoncodec.New[doc](), nil, nil)
	if _, err := delegating.Encode(doc{Title: "delegated"}); err != nil {
		t.Fatalf("delegating Encode = %v, want nil", err)
	}
}
