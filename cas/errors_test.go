package cas_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// mustFS builds a filesystem backend rooted in a temp dir with the given
// options.
func mustFS(t *testing.T, opts ...backend.Option) *fs.Backend {
	s, err := fs.New(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// test.ErrorObj / test.FailingCodec are defined in external_test.go. The FS-internal
// error-path tests (TestFSPutMkdirError, TestFSPutReaderError,
// TestFSListIgnoresRootStray, TestFSHashPathDigestClamp) moved into
// cas/backend/fs/fs_test.go where they can reach the unexported layout.

func TestBackendCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := test.HashData([]byte("x"))

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
			if _, err := raw.List(ctx); err == nil {
				t.Error("List on cancelled ctx must error")
			}
		})
	}
}

func TestStoreEncodeError(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), test.FailingCodec[test.ErrorObj]{}, sha256.New())
	if _, err := s.Put(ctx, test.ErrorObj{}); err == nil {
		t.Fatal("Put with failing codec must error")
	}
	if _, _, err := s.PutDedup(ctx, test.ErrorObj{}); err == nil {
		t.Fatal("PutDedup with failing codec must error")
	}
}

func TestStorePutDedupCancelled(t *testing.T) {
	s := newTestStore(t, mem.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.PutDedup(ctx, test.Note{Title: "t"}); err == nil {
		t.Fatal("PutDedup on cancelled ctx must error")
	}
}

// TestGetCorruptPayload pins ErrCorrupt: a stored payload the store
// codec cannot decode surfaces as ErrCorrupt from Get.
func TestGetCorruptPayload(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	store := cas.New(raw, jsoncodec.New[test.Note](), sha256.New())
	// TLV envelope: [version][uvarint typeLen][type][uvarint payloadLen][payload].
	// A payload that is not valid JSON for test.Note will cause the codec
	// Decode to fail, surfacing as ErrCorrupt.
	payload := []byte("this is not json")
	var buf bytes.Buffer
	buf.WriteByte(1) // version
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len("note@1")))
	buf.Write(lenBuf[:n])
	buf.WriteString("note@1")
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	stored := buf.Bytes()
	h := test.HashData(stored)
	if err := raw.Put(ctx, h, bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, h); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(corrupt payload) = %v, want ErrCorrupt", err)
	}
}

func TestStoreBadEnvelope(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	s := newTestStore(t, raw)
	// Every case must fail Get with ErrUnknownType: version byte is not 1,
	// empty bytes, or truncated.
	for _, garbage := range []string{
		"not json at all", // version byte is 'n' (0x6E) ≠ 1
		"",                // empty
		"\npayload",       // version byte is '\n' (0x0A) ≠ 1
		"note@1",          // version byte is 'n' (0x6E) ≠ 1
	} {
		h := test.HashData([]byte(garbage))
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
	h := test.HashData([]byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Verify(ctx, h, sha256.New()); err == nil {
		t.Fatal("Verify on cancelled ctx must error")
	}
}

func TestStoreGetRawMissing(t *testing.T) {
	s := newTestStore(t, mem.New())
	missing := sha256.Of([]byte("never stored"))
	if _, err := s.GetRaw(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetRaw = %v, want ErrNotFound", err)
	}
}
