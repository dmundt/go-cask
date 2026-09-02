package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := New(context.Background(), Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return st, dir
}

func put(t *testing.T, st *Store, data string) cas.Hash {
	t.Helper()
	h, err := cas.HashBytes("sha256", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(context.Background(), h, strings.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPutGetListSizeStats(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)

	h := put(t, st, "hello world")
	rc, err := st.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(data) != "hello world" {
		t.Fatalf("Get = %q, %v", data, err)
	}

	size, err := st.Size(h)
	if err != nil || size != 11 {
		t.Fatalf("Size = %d, %v; want 11", size, err)
	}

	missing, _ := cas.HashBytes("sha256", []byte("nope"))
	if _, err := st.Size(missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Size(missing) err = %v, want ErrNotFound", err)
	}

	hashes, err := st.List(ctx, "")
	if err != nil || len(hashes) != 1 || !hashes[0].Equal(h) {
		t.Fatalf("List = %v, %v", hashes, err)
	}

	stats, err := st.Stats(ctx)
	if err != nil || stats.ObjectCount != 1 || stats.TotalSize != 11 {
		t.Fatalf("Stats = %+v, %v", stats, err)
	}
}

// TestPreExistingObjectSize is the regression test for the removed in-memory
// sizes map: an object written directly through cas (or by an older process)
// must report its true size, not 0.
func TestPreExistingObjectSize(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw, err := cas.NewFSRawStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	content := "written outside the store service"
	h, err := cas.HashBytes("sha256", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Put(ctx, h, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}

	st, err := New(ctx, Config{Dir: dir}) // a fresh Store over the same dir
	if err != nil {
		t.Fatal(err)
	}
	size, err := st.Size(h)
	if err != nil || size != int64(len(content)) {
		t.Fatalf("Size of pre-existing object = %d, %v; want %d", size, err, len(content))
	}
}

func TestVerifyDeleteGCPrune(t *testing.T) {
	ctx := context.Background()
	st, _ := newStore(t)
	h1 := put(t, st, "one")
	h2 := put(t, st, "two")

	if err := st.Verify(ctx, h1); err != nil {
		t.Fatalf("Verify = %v", err)
	}

	deleted, err := st.GC(ctx, map[string]bool{h1.String(): true})
	if err != nil || deleted != 1 {
		t.Fatalf("GC = %d, %v; want 1", deleted, err)
	}
	if ok, _ := st.Exists(ctx, h2); ok {
		t.Fatal("unreferenced object survived GC")
	}

	// Prune dry-run is a no-op report.
	doomed, err := st.Prune(ctx, []cas.Hash{h1}, time.Nanosecond, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 0 {
		t.Fatalf("prune dry-run returned %d, want 0 (h1 reachable)", len(doomed))
	}

	if err := st.Delete(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, h1); err != nil { // missing is a no-op
		t.Fatal(err)
	}
	if ok, _ := st.Exists(ctx, h1); ok {
		t.Fatal("deleted object still present")
	}
}
