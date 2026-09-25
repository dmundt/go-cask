package repo_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/repo"
)

// registrationObject is the object type registered with the registry in this
// test.
type registrationObject struct {
	ID string `json:"id"`
}

func (registrationObject) Type() string             { return "registered@1" }
func (registrationObject) References() []cas.Digest { return nil }

// reReadBackend serves one named digest from a queue of caller-supplied byte
// frames and everything else from the inner store. It is the seam that reaches
// RegisterStore's decoder error branch: repo.Resolve reads the header first and
// then asks the store to decode the same digest, so the second read is handed
// bytes the store cannot decode.
type reReadBackend struct {
	inner cas.Backend

	mu     sync.Mutex
	digest cas.Digest
	frames [][]byte
}

func (b *reReadBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	return b.inner.Put(ctx, d, r)
}

func (b *reReadBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	b.mu.Lock()
	if d.Equal(b.digest) && len(b.frames) > 0 {
		frame := b.frames[0]
		b.frames = b.frames[1:]
		b.mu.Unlock()
		return io.NopCloser(bytes.NewReader(frame)), nil
	}
	b.mu.Unlock()
	return b.inner.Get(ctx, d)
}

func (b *reReadBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	return b.inner.Exists(ctx, d)
}

func (b *reReadBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}

func (b *reReadBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(ctx)
}

func (b *reReadBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

// TestResolveThroughARegisteredStoreReportsADecodeFailure pins the decoder
// closure RegisterStore derives from store.Get: the registry learns the type
// from the envelope header, then the registered store reports the payload's own
// failure (cas.ErrCorrupt) rather than an unknown type, an empty object, or a
// silent skip.
func TestResolveThroughARegisteredStoreReportsADecodeFailure(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	codec := jsoncodec.New[registrationObject]()

	// The header read gets a well-formed frame; the decode read of the same
	// digest gets a frame whose payload is not valid JSON — damage the store
	// reports (cas.ErrCorrupt) instead of an unknown type.
	valid, err := cas.EncodeEnvelope("json", registrationObject{}.Type(), []byte(`{"id":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	damaged, err := cas.EncodeEnvelope("json", registrationObject{}.Type(), []byte(`{"id":`))
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Of(valid)
	backend := &reReadBackend{inner: inner, digest: d, frames: [][]byte{valid, damaged}}

	store := cas.New(backend, codec, sha256.New())
	registry := repo.NewRegistry(backend, sha256.New())
	if err := repo.RegisterStore(registry, registrationObject{}.Type(), store); err != nil {
		t.Fatal(err)
	}

	obj, err := registry.Resolve(ctx, d)
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Resolve over a damaged payload = (%v, %v), want cas.ErrCorrupt", obj, err)
	}
	if obj != nil {
		t.Fatalf("Resolve over a damaged payload = %v, want no object", obj)
	}
	if errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Resolve over a damaged payload = %v, must not be reported as an unknown type", err)
	}

	// The registry recorded the exact store RegisterStore was given, so a caller
	// can retry the typed read itself.
	got, err := repo.LookupStore[registrationObject](registry, registrationObject{}.Type())
	if err != nil {
		t.Fatalf("LookupStore = %v", err)
	}
	if got != store {
		t.Fatal("LookupStore returned a different store than the one registered")
	}
}

// TestResolveOverAMissingObjectReportsNotFound pins the neighbouring truth: a
// digest no object is stored at is ErrNotFound from the header read, before the
// registered store is consulted at all.
func TestResolveOverAMissingObjectReportsNotFound(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	store := cas.New(backend, jsoncodec.New[registrationObject](), sha256.New())
	registry := repo.NewRegistry(backend, sha256.New())
	if err := repo.RegisterStore(registry, registrationObject{}.Type(), store); err != nil {
		t.Fatal(err)
	}
	d, err := store.Put(ctx, registrationObject{ID: "then removed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	obj, err := registry.Resolve(ctx, d)
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Resolve over a removed object = (%v, %v), want cas.ErrNotFound", obj, err)
	}
	if obj != nil {
		t.Fatalf("Resolve over a removed object = %v, want no object", obj)
	}
}

// TestReachableReportsABrokenReference pins the closure Reachable hands to
// Walk: the walk's error is returned with no reachable set, rather than a
// partial set a caller could mistake for the complete root set of a GC.
func TestReachableReportsABrokenReference(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	store := cas.New(backend, jsoncodec.New[registrationObject](), sha256.New())
	registry := repo.NewRegistry(backend, sha256.New())
	if err := repo.RegisterStore(registry, registrationObject{}.Type(), store); err != nil {
		t.Fatal(err)
	}
	d, err := store.Put(ctx, registrationObject{ID: "then removed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}

	reachable, err := repo.Reachable(ctx, registry, []cas.Digest{d})
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Reachable over a removed root = (%v, %v), want cas.ErrNotFound", reachable, err)
	}
	if reachable != nil {
		t.Fatalf("Reachable returned %v alongside the error, want no partial set", reachable)
	}
}
