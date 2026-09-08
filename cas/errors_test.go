package cas_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/memory"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// errorObject / failingCodec are defined in external_test.go. The FS-internal
// error-path tests (TestFSPutMkdirError, TestFSPutReaderError,
// TestFSListIgnoresRootStray, TestFSHashPathDigestClamp) moved into
// cas/backend/fs/fs_test.go where they can reach the unexported layout.

func TestHashDataUnknownAlgorithm(t *testing.T) {
	if _, err := hashData("nope", []byte("x")); !errors.Is(err, cas.ErrUnknownAlgorithm) {
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

func TestStoreEncodeError(t *testing.T) {
	ctx := context.Background()
	s, err := cas.New(backmem.New(), failingCodec[errorObject]{}, "sha256")
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
	s := newTestStore(t, backmem.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.PutDedup(ctx, testNote{Title: "t"}); err == nil {
		t.Fatal("PutDedup on cancelled ctx must error")
	}
}

// TestGetCorruptPayload pins ErrCorrupt: a stored payload the store
// codec cannot decode surfaces as ErrCorrupt from Get.
func TestGetCorruptPayload(t *testing.T) {
	ctx := context.Background()
	raw := backmem.New()
	store, err := cas.New(raw, jsoncodec.New[testNote](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	// Stored form: "<type>\n<codec payload>". A payload that is not valid
	// JSON for testNote will cause the codec Decode to fail, surfacing as
	// ErrCorrupt.
	stored := "note@1\nthis is not json"
	h, _ := hashData("sha256", []byte(stored))
	if err := raw.Put(ctx, h, strings.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, h); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(corrupt payload) = %v, want ErrCorrupt", err)
	}
}

func TestStoreBadEnvelope(t *testing.T) {
	ctx := context.Background()
	raw := backmem.New()
	s := newTestStore(t, raw)
	// Every case must fail Get with ErrUnknownType: no newline separator
	// (not the typed form), or an empty type name.
	for _, garbage := range []string{
		"not json at all", // no newline separator
		"",                // empty
		"\npayload",       // empty type name
		"note@1",          // no newline separator
	} {
		h, err := hashData("sha256", []byte(garbage))
		if err != nil {
			t.Fatal(err)
		}
		if err := raw.Put(ctx, h, strings.NewReader(garbage)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, h); !errors.Is(err, cas.ErrUnknownType) {
			t.Errorf("Get(%q) = %v, want ErrUnknownType", garbage, err)
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

func TestStoreGetRawMissing(t *testing.T) {
	s := newTestStore(t, backmem.New())
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := s.GetRaw(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetRaw = %v, want ErrNotFound", err)
	}
}
