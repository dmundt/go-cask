package web

import (
	"bytes"
	"context"
	"strings"
	"testing"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

// TestObjectMetaIsCached proves the list's per-object metadata is read once.
// The object is deleted between the two lookups: a second read would fail and
// report the object unreadable, so an unchanged answer can only come from the
// cache.
func TestObjectMetaIsCached(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := tlvEnvelope("blob@1", []byte("cached"))
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := backend.Put(ctx, h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}

	first := srv.objectMetaFor(ctx, h)
	if first.Type != "blob@1" || first.Size != int64(len(env)) || first.Unreadable {
		t.Fatalf("objectMetaFor() = %#v, want a readable blob@1 of %d bytes", first, len(env))
	}
	if err := backend.Delete(ctx, h); err != nil {
		t.Fatal(err)
	}
	if second := srv.objectMetaFor(ctx, h); second != first {
		t.Fatalf("objectMetaFor() after delete = %#v, want the cached %#v", second, first)
	}
}

// TestObjectMetaDoesNotCacheFailures keeps a transient read failure from
// sticking: an object that becomes readable must stop reporting as unreadable.
func TestObjectMetaDoesNotCacheFailures(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("cd", 32))
	if missing := srv.objectMetaFor(ctx, h); !missing.Unreadable {
		t.Fatalf("objectMetaFor(absent) = %#v, want unreadable", missing)
	}
	env := tlvEnvelope("tree@1", []byte("now here"))
	if err := backend.Put(ctx, h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	if meta := srv.objectMetaFor(ctx, h); meta.Unreadable || meta.Type != "tree@1" {
		t.Fatalf("objectMetaFor(present) = %#v, want a readable tree@1", meta)
	}
}

// TestMetaCacheDropsAtLimit checks the cache stays bounded rather than growing
// with the store.
func TestMetaCacheDropsAtLimit(t *testing.T) {
	cache := newMetaCache()
	for i := range objectMetaCacheLimit {
		cache.store(string(rune(i)), objectMeta{Size: int64(i)})
	}
	if len(cache.byDigest) != objectMetaCacheLimit {
		t.Fatalf("cache size = %d, want %d", len(cache.byDigest), objectMetaCacheLimit)
	}
	cache.store("overflow", objectMeta{})
	if len(cache.byDigest) != 1 {
		t.Fatalf("cache size after overflow = %d, want 1", len(cache.byDigest))
	}
	if _, ok := cache.lookup("overflow"); !ok {
		t.Fatal("the entry that triggered the drop should survive it")
	}
}
