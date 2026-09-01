package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/examples/gitlike"
)

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// objectPath rebuilds the on-disk path for a hash under the default (2,1)
// fan-out layout: <dir>/<algo>/<2 hex>/<full hex>.
func objectPath(dir string, h string) string {
	algo, hex, _ := strings.Cut(h, ":")
	return filepath.Join(dir, algo, hex[:2], hex)
}

// Acceptance: add → commit → log → cat round-trips.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	f1 := writeTempFile(t, work, "a.txt", "hello world")
	f2 := writeTempFile(t, work, "b.txt", "second file")

	tree, err := a.add(ctx, []string{f1, f2})
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("add returned no tree hash")
	}
	commit, err := a.commit(ctx, "initial")
	if err != nil {
		t.Fatal(err)
	}
	if commit == nil {
		t.Fatal("commit returned no hash")
	}

	// log shows the commit message.
	var logBuf bytes.Buffer
	if err := a.log(ctx, &logBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logBuf.String(), "initial") {
		t.Fatalf("log = %q, want commit message", logBuf.String())
	}

	// cat resolves the blob and returns identical bytes.
	blob, err := a.repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello world")})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := a.cat(ctx, blob, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello world" {
		t.Fatalf("cat = %q, want %q", out.String(), "hello world")
	}
}

// Dedup: identical bytes stored via add produce one blob object.
func TestDedup(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f1 := writeTempFile(t, work, "x1.txt", "same content")
	f2 := writeTempFile(t, work, "x2.txt", "same content")

	if _, err := a.add(ctx, []string{f1}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.add(ctx, []string{f2}); err != nil {
		t.Fatal(err)
	}
	st, err := a.raw.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 1 blob (deduplicated) + 2 trees (different entry names).
	if st.AlgorithmCounts["sha256"] != 3 {
		t.Fatalf("sha256 object count = %d, want 3", st.AlgorithmCounts["sha256"])
	}
}

// Acceptance: verify passes after a clean commit and reports a mismatch
// after a stored file is corrupted on disk.
func TestVerify(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, work, "a.txt", "verify me")
	if _, err := a.add(ctx, []string{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.commit(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if err := a.verify(ctx); err != nil {
		t.Fatalf("verify on clean store: %v", err)
	}

	// Corrupt one stored object on disk.
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) == 0 {
		t.Fatal("no objects stored")
	}
	path := objectPath(a.dir, hashes[0].String())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.verify(ctx); err == nil {
		t.Fatal("verify must report corruption")
	}
}
