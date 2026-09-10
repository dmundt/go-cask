package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// TestManifestJSONPayloadPinned locks the stored payload shape: it is the JSON
// the hand-written marshaller used to emit, so moving the hash JSON shape into
// the codec's field type (jsoncodec.Hash) does not re-address stored manifests
// (the manifest hash is part of the GC reachability set).
func TestManifestJSONPayloadPinned(t *testing.T) {
	h := cas.HashBytes([]byte("artifact payload"))
	raw, err := json.Marshal(Manifest{Name: "target", Artifacts: refs(h)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"target","artifacts":["` + h.String() + `"]}`
	if string(raw) != want {
		t.Fatalf("Manifest JSON = %s, want %s", raw, want)
	}

	// An empty artifact list stays omitted, as before.
	raw, err = json.Marshal(Manifest{Name: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"name":"target"}` {
		t.Fatalf("empty-artifacts Manifest JSON = %s", raw)
	}
}

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
	missing, err := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.get(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("get(missing) = %v, want ErrNotFound", err)
	}
}

func TestRunCommands(t *testing.T) {
	store := t.TempDir()
	work := t.TempDir()
	f := filepath.Join(work, "a.bin")
	if err := os.WriteFile(f, []byte("hello artifacts"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// put
	if code := run([]string{"-store", store, "put", "v1", f}, &stdout, &stderr); code != 0 {
		t.Fatalf("put code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deduplicated: false") {
		t.Fatalf("put out=%q", stdout.String())
	}
	hash := strings.Split(stdout.String(), " ")[0]
	stdout.Reset()
	stderr.Reset()

	// get
	if code := run([]string{"-store", store, "get", hash}, &stdout, &stderr); code != 0 {
		t.Fatalf("get code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "15 bytes") {
		t.Fatalf("get out=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()

	// stats
	if code := run([]string{"-store", store, "stats"}, &stdout, &stderr); code != 0 {
		t.Fatalf("stats code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()

	// gc
	if code := run([]string{"-store", store, "gc"}, &stdout, &stderr); code != 0 {
		t.Fatalf("gc code=%d", code)
	}
}

func TestRunUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cases := [][]string{
		{},                      // no args
		{"-store"},              // -store missing value
		{"-store", t.TempDir()}, // -store with no command (must not panic)
		{"put"},                 // put missing args
		{"get"},                 // get missing hash
		{"unknown"},             // unknown command
	}
	for _, c := range cases {
		stdout.Reset()
		stderr.Reset()
		if code := run(c, &stdout, &stderr); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", c, code)
		}
	}
}
