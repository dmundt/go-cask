package cbor_test

import (
	"bytes"
	"reflect"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas/codec/cbor"
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
