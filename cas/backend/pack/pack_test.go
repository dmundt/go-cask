package pack

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func TestPackBackendRoundTripAndList(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(10), WithPackMaxBytes(1024))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	d := cas.NewDigest([]byte("hello world"))
	if err := b.Put(ctx, d, bytesReader([]byte("hello world"))); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Exists(ctx, d); err != nil || !ok {
		t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	reader, err := b.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("Get() = %q, want %q", string(got), "hello world")
	}
	list, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}
	stats, err := b.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ObjectCount != 1 {
		t.Fatalf("Stats().ObjectCount = %d, want 1", stats.ObjectCount)
	}
}

func TestPackBackendDisabledMatchesLoose(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "plain")
	b, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	d := cas.NewDigest([]byte("absent"))
	if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	ok, err := b.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	if err := b.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	ok, err = b.Exists(ctx, d)
	if err != nil || ok {
		t.Fatalf("Exists() after Delete = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestPackBackendPersistsIndexAcrossRestart(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "persist")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("persist me"))
	if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	ok, err := reopened.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("reopened Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	reader, err := reopened.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("reopened payload = %q, want %q", string(payload), "payload")
	}
}

func TestPackBackendRotatesByEntryLimit(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "rotate")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(1), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	first := cas.NewDigest([]byte("first"))
	second := cas.NewDigest([]byte("second"))
	if err := b.Put(ctx, first, bytesReader([]byte("one"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, second, bytesReader([]byte("two"))); err != nil {
		t.Fatal(err)
	}

	if b.packEntries == 0 {
		t.Fatal("packEntries should be > 0 after rotation")
	}
	if len(b.index) != 2 {
		t.Fatalf("index len = %d, want 2", len(b.index))
	}
	if _, ok := b.index[string(first)]; !ok {
		t.Fatal("first digest missing from index")
	}
	if _, ok := b.index[string(second)]; !ok {
		t.Fatal("second digest missing from index")
	}
}

func TestPackBackendRejectsInvalidDigestAndCloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "invalid")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Put(ctx, nil, bytesReader([]byte("x"))); err == nil {
		t.Fatal("Put(nil) = nil, want error")
	}
	if _, err := b.Get(ctx, nil); err == nil {
		t.Fatal("Get(nil) = nil, want error")
	}
	if _, err := b.Exists(ctx, nil); err == nil {
		t.Fatal("Exists(nil) = nil, want error")
	}
	if err := b.Delete(ctx, nil); err == nil {
		t.Fatal("Delete(nil) = nil, want error")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close() first call = %v, want nil", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close() second call = %v, want nil", err)
	}
}

func bytesReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}
