package cas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingReader fails after reading some bytes — exercises the write-path
// error/cleanup branches of FSRawStore.Put.
type failingReader struct {
	data []byte
	off  int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, errors.New("simulated read failure")
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

func TestHashDataUnknownAlgorithm(t *testing.T) {
	if _, err := hashData("nope", []byte("x")); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Fatalf("err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestBackendCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h, _ := hashData("sha256", []byte("x"))

	for _, bf := range []struct {
		name string
		fn   backendFactory
	}{
		{"fs", fsFactory},
		{"memory", memFactory},
	} {
		t.Run(bf.name, func(t *testing.T) {
			raw := bf.fn(t)
			if err := raw.Put(ctx, h, strings.NewReader("x")); err == nil {
				t.Error("Put on cancelled ctx must error")
			}
			if _, err := raw.Get(ctx, h); err == nil {
				t.Error("Get on cancelled ctx must error")
			}
			if _, err := raw.Exists(ctx, h); err == nil {
				t.Error("Exists on cancelled ctx must error")
			}
			if err := raw.Delete(ctx, h); err == nil {
				t.Error("Delete on cancelled ctx must error")
			}
			if _, err := raw.List(ctx, ""); err == nil {
				t.Error("List on cancelled ctx must error")
			}
		})
	}
}

func TestFSPutMkdirError(t *testing.T) {
	// Make the algorithm directory unusable: create a FILE where the
	// algorithm dir would go, so MkdirAll fails.
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("x"))
	blocker := filepath.Join(s.base, "sha256")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), h, strings.NewReader("x")); err == nil {
		t.Fatal("Put must fail when the object dir cannot be created")
	}
}

func TestFSPutReaderError(t *testing.T) {
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("x"))
	err := s.Put(context.Background(), h, &failingReader{data: []byte("partial")})
	if err == nil {
		t.Fatal("Put with failing reader must error")
	}
	// The temp file must be cleaned up and the object must not exist.
	list, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("failed Put left objects behind: %v", list)
	}
	if _, err := os.Stat(s.hashPath(h) + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("failed Put left a .tmp file behind")
	}
}

func TestFSListIgnoresRootStray(t *testing.T) {
	s := mustFS(t)
	// A stray file directly in the base directory is not an object.
	if err := os.WriteFile(filepath.Join(s.base, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("stray root file listed as object: %v", list)
	}
}

func TestFSHashPathDigestClamp(t *testing.T) {
	// sha1 digests are 40 hex chars; layouts that exceed the digest length
	// must clamp (end > len) or stop (start >= len) rather than overrun.
	h, _ := hashData("sha1", []byte("clamp"))
	cases := []struct {
		opts []FSOption
	}{
		{[]FSOption{WithFanOut(16), WithFanLevels(3)}}, // 3rd chunk clamps: 32..40
		{[]FSOption{WithFanOut(16), WithFanLevels(4)}}, // 4th level breaks: 48 >= 40
		{[]FSOption{WithFanOut(8), WithFanLevels(8)}},  // many levels, digest exhausted
	}
	for _, tc := range cases {
		s := mustFS(t, tc.opts...)
		p := s.hashPath(h)
		// The file name must still be the full hex digest.
		base := filepath.Base(p)
		if base != h.String()[strings.IndexByte(h.String(), ':')+1:] {
			t.Errorf("opts %v: basename = %q, want full digest", tc.opts, base)
		}
		// And the path must round-trip.
		rel, err := filepath.Rel(s.base, p)
		if err != nil {
			t.Fatal(err)
		}
		back, err := pathToHash(rel)
		if err != nil || !back.Equal(h) {
			t.Errorf("opts %v: pathToHash(%q) = %v, %v", tc.opts, rel, back, err)
		}
	}
}

// errorObject is a minimal Object[T] used with a failing codec.
type errorObject struct{}

func (errorObject) Type() string       { return "err@1" }
func (errorObject) References() []Hash { return nil }

// failingCodec always fails to encode — the serialization authority is the
// codec now, so an encode failure must surface from Store.Put/PutDedup.
type failingCodec[T any] struct{}

func (failingCodec[T]) Encode(T) ([]byte, error) { return nil, errors.New("encode exploded") }
func (failingCodec[T]) Decode([]byte) (T, error) { var z T; return z, nil }

func TestStoreEncodeError(t *testing.T) {
	ctx := context.Background()
	s, err := NewStore(NewMemoryRawStore(), failingCodec[errorObject]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, errorObject{}); err == nil {
		t.Fatal("Put with failing codec must error")
	}
	if _, _, err := s.PutDedup(ctx, errorObject{}); err == nil {
		t.Fatal("PutDedup with failing codec must error")
	}
}

func TestStorePutDedupCancelled(t *testing.T) {
	s := newTestStore(t, NewMemoryRawStore())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.PutDedup(ctx, testNote{Title: "t"}); err == nil {
		t.Fatal("PutDedup on cancelled ctx must error")
	}
}

// plain is a struct that does NOT implement Object[plain] — used to hit the
// "decoded value is not an Object[T]" branch of Store.Get.
type plain struct{ X int }

func TestStoreGetNonObjectValue(t *testing.T) {
	ctx := context.Background()
	raw := NewMemoryRawStore()
	store, err := NewStore(raw, JSONCodec[plain]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	// Hand-store an envelope whose payload decodes to plain.
	payload := base64.StdEncoding.EncodeToString([]byte(`{"X":1}`))
	data, err := json.Marshal(envelope{Type: "plain@1", Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := hashData("sha256", data)
	if err := raw.Put(ctx, h, strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, h); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("Get = %v, want ErrUnknownType (plain does not implement Object)", err)
	}
}

func TestStoreBadEnvelope(t *testing.T) {
	ctx := context.Background()
	raw := NewMemoryRawStore()
	s := newTestStore(t, raw)
	for _, garbage := range []string{
		"not json at all",
		`{"data":"AAAA"}`, // missing type
		`{"type":"note@1","data":"%%%"}` + `"` + `}`, // bad base64
	} {
		h, err := hashData("sha256", []byte(garbage))
		if err != nil {
			t.Fatal(err)
		}
		if err := raw.Put(ctx, h, strings.NewReader(garbage)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetTyped(ctx, h); !errors.Is(err, ErrUnknownType) {
			t.Errorf("GetTyped(%q) = %v, want ErrUnknownType", garbage, err)
		}
	}
}

func TestVerifyCancelled(t *testing.T) {
	s := mustFS(t)
	h, _ := hashData("sha256", []byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Verify(ctx, h); err == nil {
		t.Fatal("Verify on cancelled ctx must error")
	}
}

func TestCachedLoadErrorMemoized(t *testing.T) {
	ctx := context.Background()
	store, c := newCachedSuite(t)
	h, err := store.Put(ctx, testNote{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	co, err := c.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	// Remove the object, then Load: the error must be memoized.
	if err := store.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := co.Load(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load = %v, want ErrNotFound", err)
	}
	if !co.IsLoaded() {
		t.Fatal("failed Load must still mark the object loaded")
	}
	if _, err := co.Load(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Load = %v, want memoized ErrNotFound", err)
	}
}

func TestCachedPreloadError(t *testing.T) {
	ctx := context.Background()
	_, c := newCachedSuite(t)
	missing, _ := ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err := c.Preload(ctx, []Hash{missing}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Preload = %v, want ErrNotFound", err)
	}
}

func TestCachedPreloadRecursiveMissingRef(t *testing.T) {
	ctx := context.Background()
	ns, err := NewStore(NewMemoryRawStore(), JSONCodec[testNode]{}, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	cn := NewCachedStore(ns)
	missing, _ := ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	root, err := ns.Put(ctx, testNode{Name: "root", Refs: []Hash{missing}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cn.PreloadRecursive(ctx, root, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PreloadRecursive = %v, want ErrNotFound", err)
	}
}

func TestStoreGetRawMissing(t *testing.T) {
	s := newTestStore(t, NewMemoryRawStore())
	missing, _ := ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := s.GetRaw(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRaw = %v, want ErrNotFound", err)
	}
}
