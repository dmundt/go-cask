package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func newTestApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	return a
}

func writeArtifact(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Acceptance: same bytes → same hash → deduplicated: true.
func TestDedup(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	f := writeArtifact(t, work, "a.bin", "identical bytes")
	h1, dedup1, err := a.put(ctx, "target", f)
	if err != nil {
		t.Fatal(err)
	}
	if dedup1 {
		t.Fatal("first put must not be deduplicated")
	}
	h2, dedup2, err := a.put(ctx, "target", f)
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() != h2.String() {
		t.Fatalf("identical bytes must hash identically: %s vs %s", h1, h2)
	}
	if !dedup2 {
		t.Fatal("second put of identical bytes must report deduplicated: true")
	}
}

// Acceptance: the second get hits the cache (hit rate > 0).
func TestCacheHitRate(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	f := writeArtifact(t, work, "a.bin", "cache me")
	h, _, err := a.put(ctx, "target", f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.get(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := a.get(ctx, h); err != nil {
		t.Fatal(err)
	}
	st := a.cache.CacheStats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("cache stats = %+v, want 1 hit 1 miss", st)
	}
	if st.HitRate <= 0 {
		t.Fatalf("hit rate = %v, want > 0", st.HitRate)
	}
}

// Acceptance: gc deletes only unreferenced artifacts and leaves
// manifest-referenced ones intact.
func TestGC(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a := newTestApp(t)

	// Two builds of the same target: the manifest ends up referencing only
	// the second artifact; the first becomes garbage.
	f1 := writeArtifact(t, work, "v1.bin", "build v1")
	h1, _, err := a.put(ctx, "app", f1)
	if err != nil {
		t.Fatal(err)
	}
	f2 := writeArtifact(t, work, "v2.bin", "build v2")
	h2, _, err := a.put(ctx, "app", f2)
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() == h2.String() {
		t.Fatal("different content must hash differently")
	}

	n, err := a.gc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("gc deleted %d objects, want >= 1", n)
	}
	if ok, _ := a.raw.Exists(ctx, h1); ok {
		t.Fatal("unreferenced artifact survived gc")
	}
	if ok, _ := a.raw.Exists(ctx, h2); !ok {
		t.Fatal("manifest-referenced artifact was deleted")
	}
}

// A get of a missing hash fails with ErrNotFound.
func TestGetMissing(t *testing.T) {
	a := newTestApp(t)
	missing, err := cas.ParseHash("sha256double:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.get(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("get(missing) = %v, want ErrNotFound", err)
	}
}
