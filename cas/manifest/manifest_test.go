package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas/manifest"
)

type customCodec struct{}

func (customCodec) Encode(v string) ([]byte, error)    { return []byte(v), nil }
func (customCodec) Decode(data []byte) (string, error) { return string(data), nil }

func TestEncodeDecodeRoundTrip(t *testing.T) {
	want := manifest.Data{"kind": "test", "owner": "team-a"}
	b, err := manifest.Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := manifest.Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["kind"] != want["kind"] || got["owner"] != want["owner"] {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
}

func TestGenericCodecRoundTrip(t *testing.T) {
	want := struct {
		Name  string
		Count int
	}{Name: "demo", Count: 3}
	b, err := manifest.EncodeWith(want, jsoncodec.New[struct {
		Name  string
		Count int
	}]())
	if err != nil {
		t.Fatalf("EncodeWith: %v", err)
	}
	got, err := manifest.DecodeWith(b, jsoncodec.New[struct {
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
	if err := manifest.SaveWith(path, want, customCodec{}); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}
	got, err := manifest.LoadWith(path, customCodec{})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if got != want {
		t.Fatalf("LoadWith mismatch: got %q, want %q", got, want)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	want := manifest.Data{"retention": "7d", "tag": "demo"}
	if err := manifest.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got["tag"] != want["tag"] || got["retention"] != want["retention"] {
		t.Fatalf("Load mismatch: got %#v, want %#v", got, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat path: %v", err)
	}
}

func TestNilCodecDefaultsAndFailures(t *testing.T) {
	want := "demo"
	if _, err := manifest.EncodeWith(want, nil); err != nil {
		t.Fatalf("EncodeWith nil codec should default: %v", err)
	}
	if got, err := manifest.DecodeWith[string]([]byte("\"demo\""), nil); err != nil {
		t.Fatalf("DecodeWith nil codec should default: %v", err)
	} else if got != want {
		t.Fatalf("DecodeWith nil codec mismatch: got %q, want %q", got, want)
	}
	store := manifest.New[string](filepath.Join(t.TempDir(), "subdir", "meta.json"), nil)
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

	fails := manifest.New[string](filepath.Join(t.TempDir(), "bad", "meta.json"), customFailCodec{})
	if err := fails.Save(want); err == nil {
		t.Fatal("Save with failing codec should error")
	}
	if _, err := manifest.LoadWith(filepath.Join(t.TempDir(), "missing.json"), customFailCodec{}); err == nil {
		t.Fatal("LoadWith missing file should error")
	}
}

type customFailCodec struct{}

func (customFailCodec) Encode(string) ([]byte, error) { return nil, os.ErrInvalid }
func (customFailCodec) Decode([]byte) (string, error) { return "", os.ErrInvalid }
