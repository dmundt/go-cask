package cas_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// mustFS builds a filesystem backend rooted in a temp dir with the given
// options.
func mustFS(t *testing.T, opts ...fs.Option) *fs.Backend {
	s, err := fs.New(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// test.ErrorObj / test.FailingCodec are defined in external_test.go. The FS-internal
// error-path tests (TestFSPutMkdirError, TestFSPutReaderError,
// TestFSListIgnoresRootStray, TestFSDigestPathClamp) moved into
// cas/backend/fs/fs_test.go where they can reach the unexported layout.

func TestBackendCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := test.DigestData([]byte("x"))

	for _, bf := range []struct {
		name string
		fn   backendFactory
	}{
		{"fs", fsFactory},
		{"memory", memFactory},
	} {
		t.Run(bf.name, func(t *testing.T) {
			backend := bf.fn(t)
			if err := backend.Put(ctx, h, strings.NewReader("x")); err == nil {
				t.Error("Put on cancelled ctx must error")
			}
			if _, err := backend.Get(ctx, h); err == nil {
				t.Error("Get on cancelled ctx must error")
			}
			if _, err := backend.Exists(ctx, h); err == nil {
				t.Error("Exists on cancelled ctx must error")
			}
			if err := backend.Delete(ctx, h); err == nil {
				t.Error("Delete on cancelled ctx must error")
			}
			if _, err := backend.List(ctx); err == nil {
				t.Error("List on cancelled ctx must error")
			}
		})
	}
}

func TestStoreEncodeError(t *testing.T) {
	ctx := context.Background()
	s := cas.New(backmem.New(), test.FailingCodec[test.ErrorObj]{}, sha256.New())
	if _, err := s.Put(ctx, test.ErrorObj{}); err == nil {
		t.Fatal("Put with failing codec must error")
	}
	if _, _, err := s.PutDedup(ctx, test.ErrorObj{}); err == nil {
		t.Fatal("PutDedup with failing codec must error")
	}
}

func TestStorePutDedupCancelled(t *testing.T) {
	s := newTestStore(t, backmem.New())
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
	backend := backmem.New()
	store := cas.New(backend, jsoncodec.New[test.Note](), sha256.New())
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
	h := test.DigestData(stored)
	if err := backend.Put(ctx, h, bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, h); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(corrupt payload) = %v, want ErrCorrupt", err)
	}
}

func TestStoreBadEnvelope(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	s := newTestStore(t, backend)
	// Every case must fail Get with ErrCorrupt: version byte is not 1 or 2,
	// empty bytes, or truncated. ErrUnknownType would mean "not my type" and
	// would let a caller doing type dispatch mistake damage for an object it
	// simply does not handle (see TestStoreForeignTypeStaysUnknownType for the
	// other half).
	for _, garbage := range []string{
		"not json at all", // version byte is 'n' (0x6E) ≠ 1
		"",                // empty
		"\npayload",       // version byte is '\n' (0x0A) ≠ 1
		"note@1",          // version byte is 'n' (0x6E) ≠ 1
	} {
		h := test.DigestData([]byte(garbage))
		if err := backend.Put(ctx, h, strings.NewReader(garbage)); err != nil {
			t.Fatal(err)
		}
		_, err := s.Get(ctx, h)
		if !errors.Is(err, cas.ErrCorrupt) {
			t.Errorf("Get(%q) = %v, want ErrCorrupt", garbage, err)
		}
		if errors.Is(err, cas.ErrUnknownType) {
			t.Errorf("Get(%q) = %v, must not report a damaged frame as an unknown type", garbage, err)
		}
	}
}

// TestGetDistinguishesDamageFromAnUnknownType is the distinction the two
// sentinels exist for: a damaged object is ErrCorrupt, an intact envelope naming
// a type this store does not decode is ErrUnknownType. A caller that dispatches
// on errors.Is(err, cas.ErrUnknownType) — "skip it, it is not mine" — must never
// classify a damaged object that way; in a maintenance path that skip can mean
// delete.
func TestGetDistinguishesDamageFromAnUnknownType(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	notes := newTestStore(t, backend)

	// Unknown-type half: the bytes are intact, the type is one this reader does
	// not handle.
	intact, err := notes.Put(ctx, test.Note{Title: "intact"})
	if err != nil {
		t.Fatal(err)
	}
	nodes := cas.New(backend, jsoncodec.New[test.Node](), sha256.New())
	if _, err := nodes.Get(ctx, intact); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Get(intact object of another type) = %v, want ErrUnknownType", err)
	}

	// Damage half: the same frame truncated to its version byte. GetRaw hands
	// the bytes back unparsed (it does not decode), so the verdict comes from
	// Get and from EnvelopeFromBytes, and both say ErrCorrupt.
	raw, err := notes.GetRaw(ctx, intact)
	if err != nil {
		t.Fatal(err)
	}
	truncated := raw[:1]
	td := test.DigestData(truncated)
	if err := backend.Put(ctx, td, bytes.NewReader(truncated)); err != nil {
		t.Fatal(err)
	}
	if got, err := notes.GetRaw(ctx, td); err != nil || !bytes.Equal(got, truncated) {
		t.Fatalf("GetRaw(truncated envelope) = (%q, %v), want the bytes back unparsed", got, err)
	}
	if _, err := cas.EnvelopeFromBytes(truncated); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("EnvelopeFromBytes(truncated envelope) = %v, want ErrCorrupt", err)
	}
	if _, err := notes.Get(ctx, td); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Get(truncated envelope) = %v, want ErrCorrupt", err)
	} else if errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Get(truncated envelope) = %v, must not be reported as an unknown type", err)
	}
}

func TestVerifyCancelled(t *testing.T) {
	s := mustFS(t)
	h := test.DigestData([]byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Verify(ctx, h, sha256.New()); err == nil {
		t.Fatal("Verify on cancelled ctx must error")
	}
}

func TestStoreGetRawMissing(t *testing.T) {
	s := newTestStore(t, backmem.New())
	missing := sha256.Of([]byte("never stored"))
	if _, err := s.GetRaw(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetRaw = %v, want ErrNotFound", err)
	}
}
