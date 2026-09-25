package index

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// contextIgnoringBackend delegates to an in-memory store but ignores the
// context on List, so BuildSnapshot's own per-entry context check is the only
// place an already-canceled context can be observed.
type contextIgnoringBackend struct {
	inner cas.Backend
}

func (b *contextIgnoringBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	return b.inner.Put(ctx, d, r)
}

func (b *contextIgnoringBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return b.inner.Get(ctx, d)
}

func (b *contextIgnoringBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	return b.inner.Exists(ctx, d)
}

func (b *contextIgnoringBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}

func (b *contextIgnoringBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(context.Background())
}

func (b *contextIgnoringBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

func (b *contextIgnoringBackend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	return b.inner.(cas.Statter).Size(ctx, d)
}

func (b *contextIgnoringBackend) ModTime(ctx context.Context, d cas.Digest) (time.Time, error) {
	return b.inner.(cas.Statter).ModTime(ctx, d)
}

// TestBuildSnapshotChecksTheContextPerEntry pins the scan's own cancellation
// check: List succeeds, and a context canceled while the entries are being read
// stops the snapshot with the context error instead of indexing a store for a
// caller that has already given up.
func TestBuildSnapshotChecksTheContextPerEntry(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	frame := test.V2Envelope("json", "blob@1", []byte("payload"))
	d := sha256.Of(frame)
	if err := inner.Put(ctx, d, bytes.NewReader(frame)); err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	snapshot, err := BuildSnapshot(canceled, &contextIgnoringBackend{inner: inner})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildSnapshot with a canceled context = (%v, %v), want context.Canceled", snapshot, err)
	}
	if snapshot != nil {
		t.Fatalf("BuildSnapshot returned %#v alongside the cancellation", snapshot)
	}
}
