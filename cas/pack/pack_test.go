package pack_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas/codec/gob"
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
	ctx := context.Background()
	want := pack.Data{"kind": "test", "owner": "team-a"}
	codec := jsoncodec.New[pack.Data]()
	b, err := pack.EncodeWith(want, codec)
	if err != nil {
		t.Fatalf("EncodeWith: %v", err)
	}
	got, err := pack.DecodeWith(b, codec)
	if err != nil {
		t.Fatalf("DecodeWith: %v", err)
	}
	if got["kind"] != want["kind"] || got["owner"] != want["owner"] {
		t.Fatalf("round trip mismatch: got %#v, want %#v", got, want)
	}
	path := filepath.Join(t.TempDir(), "state", "meta.json")
	if err := pack.SaveWith(ctx, path, want, codec); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}
	loaded, err := pack.LoadWith(ctx, path, codec)
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if loaded["kind"] != want["kind"] || loaded["owner"] != want["owner"] {
		t.Fatalf("LoadWith mismatch: got %#v, want %#v", loaded, want)
	}
}

// TestManifestRoundTripOverAnotherCodec proves the seam instead of assuming it
// (#307): the same manifest survives EncodeWith/DecodeWith and SaveWith/LoadWith
// over a shipped codec that is not JSON, so nothing in this package has an
// opinion about the wire format.
func TestManifestRoundTripOverAnotherCodec(t *testing.T) {
	ctx := context.Background()
	want := pack.Data{"kind": "gob-manifest", "owner": "team-b"}
	codec := gob.NewRaw[pack.Data]()

	b, err := pack.EncodeWith(want, codec)
	if err != nil {
		t.Fatalf("EncodeWith(gob): %v", err)
	}
	got, err := pack.DecodeWith(b, codec)
	if err != nil {
		t.Fatalf("DecodeWith(gob): %v", err)
	}
	if got["kind"] != want["kind"] || got["owner"] != want["owner"] {
		t.Fatalf("gob round trip mismatch: got %#v, want %#v", got, want)
	}

	path := filepath.Join(t.TempDir(), "meta.gob")
	store, err := pack.New(path, codec)
	if err != nil {
		t.Fatalf("pack.New(gob): %v", err)
	}
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("Store.Save(gob): %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Store.Load(gob): %v", err)
	}
	if loaded["kind"] != want["kind"] || loaded["owner"] != want["owner"] {
		t.Fatalf("Store(gob) mismatch: got %#v, want %#v", loaded, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		t.Fatalf("a gob manifest was written as JSON: %q", raw)
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
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.txt")
	want := "demo:ok"
	if err := pack.SaveWith(ctx, path, want, customCodec{}); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}
	got, err := pack.LoadWith(ctx, path, customCodec{})
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if got != want {
		t.Fatalf("LoadWith mismatch: got %q, want %q", got, want)
	}
}

// TestNilCodecIsRejected pins the codec contract of #256: the pack layer never
// substitutes a codec, because the codec decides what is written on disk. A nil
// one is ErrNilCodec everywhere it can appear, and a rejected call writes
// nothing.
func TestNilCodecIsRejected(t *testing.T) {
	ctx := context.Background()
	if _, err := pack.EncodeWith("demo", nil); !errors.Is(err, pack.ErrNilCodec) {
		t.Fatalf("EncodeWith(nil) = %v, want ErrNilCodec", err)
	}
	if _, err := pack.DecodeWith[string]([]byte(`"demo"`), nil); !errors.Is(err, pack.ErrNilCodec) {
		t.Fatalf("DecodeWith(nil) = %v, want ErrNilCodec", err)
	}
	path := filepath.Join(t.TempDir(), "nil-codec", "meta.json")
	if _, err := pack.New[string](path, nil); !errors.Is(err, pack.ErrNilCodec) {
		t.Fatalf("New(nil) = %v, want ErrNilCodec", err)
	}
	if _, err := pack.LoadWith[string](ctx, path, nil); !errors.Is(err, pack.ErrNilCodec) {
		t.Fatalf("LoadWith(nil) = %v, want ErrNilCodec", err)
	}
	if err := pack.SaveWith[string](ctx, path, "demo", nil); !errors.Is(err, pack.ErrNilCodec) {
		t.Fatalf("SaveWith(nil) = %v, want ErrNilCodec", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a rejected call wrote %s: %v", path, err)
	}

	fails := mustStore(t, filepath.Join(t.TempDir(), "bad", "meta.json"), customFailCodec{})
	if err := fails.Save(ctx, "demo"); err == nil {
		t.Fatal("Save with failing codec should error")
	}
	if _, err := pack.LoadWith(ctx, filepath.Join(t.TempDir(), "missing.json"), customFailCodec{}); err == nil {
		t.Fatal("LoadWith missing file should error")
	}
}

// TestSaveWithInvalidPath keeps a filesystem failure a failure: the temp file
// and the rename both live under the target directory, so an unusable path
// reports the error rather than falling back to a partial write.
func TestSaveWithInvalidPath(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "bad\x00name", "meta.json")
	if err := pack.SaveWith(context.Background(), invalid, "demo", customCodec{}); err == nil {
		t.Fatal("SaveWith invalid path should error")
	}
}

// TestContextCanceledBeforeIO covers the first defect of #256: every I/O entry
// point takes a context and reports context.Canceled instead of touching the
// filesystem.
func TestContextCanceledBeforeIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "canceled", "meta.json")

	if err := pack.SaveWith(ctx, path, "demo", customCodec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveWith(canceled) = %v, want context.Canceled", err)
	}
	if _, err := pack.LoadWith(ctx, path, customCodec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadWith(canceled) = %v, want context.Canceled", err)
	}
	if err := pack.SaveWith(ctx, path, pack.Data{"kind": "x"}, jsoncodec.New[pack.Data]()); !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveWith(canceled) = %v, want context.Canceled", err)
	}
	if _, err := pack.LoadWith(ctx, path, jsoncodec.New[pack.Data]()); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadWith(canceled) = %v, want context.Canceled", err)
	}
	store := mustStore(t, path, customCodec{})
	if err := store.Save(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Store.Save(canceled) = %v, want context.Canceled", err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Store.Load(canceled) = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled write created its directory: %v", err)
	}
}

// TestSaveWithPublishesAtomically covers the third defect of #256: the manifest
// is written to a temp file in its own directory and renamed into place, so a
// successful save leaves no scratch file behind, a short manifest fully replaces
// a longer one, and a failed publish cleans the temp up.
func TestSaveWithPublishesAtomically(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	long := pack.Data{"kind": strings.Repeat("long", 64)}
	if err := pack.SaveWith(ctx, path, long, jsoncodec.New[pack.Data]()); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}
	short := pack.Data{"kind": "short"}
	if err := pack.SaveWith(ctx, path, short, jsoncodec.New[pack.Data]()); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}
	loaded, err := pack.LoadWith(ctx, path, jsoncodec.New[pack.Data]())
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if loaded["kind"] != "short" {
		t.Fatalf("the shorter manifest did not replace the longer one: %q", loaded["kind"])
	}
	assertNoScratch(t, dir)

	// A publish that cannot rename (the target is a directory) still reports the
	// error and removes its temp file.
	blocked := filepath.Join(dir, "blocked.json")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pack.SaveWith(ctx, blocked, "demo", customCodec{}); err == nil {
		t.Fatal("SaveWith onto a directory should error")
	}
	if info, err := os.Stat(blocked); err != nil || !info.IsDir() {
		t.Fatalf("the target directory was disturbed: %v, %v", info, err)
	}
	assertNoScratch(t, dir)
}

// assertNoScratch fails when dir holds a leftover temp file.
func assertNoScratch(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a scratch file survived: %s", e.Name())
		}
	}
}

// mustStore builds a path-bound store for the tests that expect success.
func mustStore[T any](t *testing.T, path string, codec pack.Codec[T]) *pack.Store[T] {
	t.Helper()
	store, err := pack.New(path, codec)
	if err != nil {
		t.Fatalf("pack.New: %v", err)
	}
	return store
}

type customCodec struct{}

func (customCodec) Encode(v string) ([]byte, error)    { return []byte(v), nil }
func (customCodec) Decode(data []byte) (string, error) { return string(data), nil }

type customFailCodec struct{}

func (customFailCodec) Encode(string) ([]byte, error) { return nil, os.ErrInvalid }
func (customFailCodec) Decode([]byte) (string, error) { return "", os.ErrInvalid }
