package pack_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas/pack"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	want := bytes.Repeat([]byte("abc123"), 19)
	parts := pack.Split(want, 7)
	if len(parts) == 0 {
		t.Fatal("Split returned no chunks")
	}
	got := pack.Join(parts)
	if !bytes.Equal(got, want) {
		t.Fatalf("Join(Split(x)) mismatch: got %q, want %q", got, want)
	}
}

func TestCountAndZero(t *testing.T) {
	if got := pack.Count(0, 16); got != 0 {
		t.Fatalf("Count(0,16) = %d, want 0", got)
	}
	if got := pack.Count(10, 0); got != 1 {
		t.Fatalf("Count(10,0) = %d, want 1", got)
	}
	if got := pack.Count(15, 4); got != 4 {
		t.Fatalf("Count(15,4) = %d, want 4", got)
	}
	if got := pack.Split(nil, 8); len(got) != 0 {
		t.Fatalf("Split(nil) = %#v, want empty slice", got)
	}
	if got := pack.Split([]byte("abc"), 0); len(got) != 1 || string(got[0]) != "abc" {
		t.Fatalf("Split(data,0) = %#v, want single chunk abc", got)
	}
	if got := pack.Join(nil); got != nil {
		t.Fatalf("Join(nil) = %#v, want nil", got)
	}
	if got := pack.Join([][]byte{}); got != nil {
		t.Fatalf("Join(empty) = %#v, want nil", got)
	}
}

func TestManifestRoundTripAndStore(t *testing.T) {
	want := pack.Data{"kind": "test", "owner": "team-a"}
	b, err := pack.Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := pack.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["kind"] != want["kind"] || got["owner"] != want["owner"] {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
	path := filepath.Join(t.TempDir(), "state", "meta.json")
	if err := pack.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := pack.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded["kind"] != want["kind"] || loaded["owner"] != want["owner"] {
		t.Fatalf("Load mismatch: got %#v, want %#v", loaded, want)
	}
}

func TestManifestCodecRoundTrip(t *testing.T) {
	want := struct {
		Name  string
		Count int
	}{Name: "demo", Count: 3}
	b, err := pack.EncodeWith(want, jsoncodec.New[struct {
		Name  string
		Count int
	}]())
	if err != nil {
		t.Fatalf("EncodeWith: %v", err)
	}
	got, err := pack.DecodeWith(b, jsoncodec.New[struct {
		Name  string
		Count int
	}]())
	if err != nil {
		t.Fatalf("DecodeWith: %v", err)
	}
	if got.Name != want.Name || got.Count != want.Count {
		t.Fatalf("DecodeWith mismatch: got %#v, want %#v", got, want)
	}
}

func TestCustomCodecFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.txt")
	want := "demo:ok"
	if err := pack.SaveWith(path, want, customCodec{}); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}
	got, err := pack.LoadWith(path, customCodec{})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if got != want {
		t.Fatalf("LoadWith mismatch: got %q, want %q", got, want)
	}
}

func TestNilCodecDefaultsAndFailures(t *testing.T) {
	want := "demo"
	if _, err := pack.EncodeWith(want, nil); err != nil {
		t.Fatalf("EncodeWith nil codec should default: %v", err)
	}
	if got, err := pack.DecodeWith[string]([]byte("\"demo\""), nil); err != nil {
		t.Fatalf("DecodeWith nil codec should default: %v", err)
	} else if got != want {
		t.Fatalf("DecodeWith nil codec mismatch: got %q, want %q", got, want)
	}
	store := pack.New[string](filepath.Join(t.TempDir(), "subdir", "meta.json"), nil)
	if err := store.Save(want); err != nil {
		t.Fatalf("Store.Save with nil codec: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Store.Load with nil codec: %v", err)
	}
	if got != want {
		t.Fatalf("Store.Load mismatch: got %q, want %q", got, want)
	}

	fails := pack.New[string](filepath.Join(t.TempDir(), "bad", "meta.json"), customFailCodec{})
	if err := fails.Save(want); err == nil {
		t.Fatal("Save with failing codec should error")
	}
	if _, err := pack.LoadWith(filepath.Join(t.TempDir(), "missing.json"), customFailCodec{}); err == nil {
		t.Fatal("LoadWith missing file should error")
	}
}

func TestSaveWithInvalidPath(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "bad\x00name", "meta.json")
	if err := pack.SaveWith(invalid, "demo", customCodec{}); err == nil {
		t.Fatal("SaveWith invalid path should error")
	}
}

type customCodec struct{}

func (customCodec) Encode(v string) ([]byte, error)    { return []byte(v), nil }
func (customCodec) Decode(data []byte) (string, error) { return string(data), nil }

type customFailCodec struct{}

func (customFailCodec) Encode(string) ([]byte, error) { return nil, os.ErrInvalid }
func (customFailCodec) Decode([]byte) (string, error) { return "", os.ErrInvalid }
