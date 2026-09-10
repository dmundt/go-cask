package fs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestPathToDigest pins the path→digest reconstruction: the last path element
// is the hex digest, whatever the fan-out depth, and a file whose name is not a
// digest is rejected (List/Stats then skip it).
func TestPathToDigest(t *testing.T) {
	d := digestOf([]byte("path to digest"))
	hexDigest := d.String()

	for _, rel := range []string{
		hexDigest,                            // flat layout
		filepath.Join("aa", hexDigest),       // one fan-out level
		filepath.Join("aa", "bb", hexDigest), // two levels
	} {
		got, err := pathToDigest(rel)
		if err != nil {
			t.Errorf("pathToDigest(%q) = %v", rel, err)
			continue
		}
		if !got.Equal(d) {
			t.Errorf("pathToDigest(%q) = %s, want %s", rel, got, d)
		}
	}

	for _, rel := range []string{"", "not-hex", filepath.Join("aa", "zz"), "ab.tmp"} {
		if _, err := pathToDigest(rel); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("pathToDigest(%q) = %v, want ErrInvalidDigest", rel, err)
		}
	}
}

// TestVerifyRejectsWrongWidthDigest pins the injected hasher's guard: Verify
// validates the key before reading, so a digest that cannot name an object is
// reported as invalid rather than as a mismatch or a miss.
func TestVerifyRejectsWrongWidthDigest(t *testing.T) {
	s := mustFS(t)
	short := cas.NewDigest([]byte{1, 2, 3})
	if err := s.Verify(context.Background(), short, sha256.New()); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Verify(short digest) = %v, want ErrInvalidDigest", err)
	}
}

// TestDirSyncRoundTrip exercises the WithDirSync path: a Put and a Delete each
// sync the object's parent directory (so the rename survives a crash).
func TestDirSyncRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t, WithDirSync())

	data := []byte("dir-synced object")
	d := digestOf(data)
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put with WithDirSync = %v", err)
	}
	rc, err := s.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllAndClose(rc)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("Verify = %v", err)
	}
	if err := s.Delete(ctx, d); err != nil {
		t.Fatalf("Delete with WithDirSync = %v", err)
	}
}

// TestCleanRemovesTempCollisionFallbacks pins Clean's scope: both "<hex>.tmp"
// and the "<hex>.tmp.<n>" collision fallbacks createTempExcl may leave behind
// are reclaimed, while a real object file is not.
func TestCleanRemovesTempCollisionFallbacks(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	d := digestOf([]byte("kept object"))
	if err := s.Put(ctx, d, strings.NewReader("kept object")); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Dir(s.hashPath(d))
	temps := []string{
		filepath.Join(dir, d.String()+".tmp"),
		filepath.Join(dir, d.String()+".tmp.1"),
		filepath.Join(dir, d.String()+".tmp.2"),
	}
	for _, p := range temps {
		if err := os.WriteFile(p, []byte("crash leftover"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.Clean(ctx, 0)
	if err != nil {
		t.Fatalf("Clean = %v", err)
	}
	if removed != len(temps) {
		t.Fatalf("Clean removed %d files, want %d", removed, len(temps))
	}
	for _, p := range temps {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("temp file %s survived Clean", p)
		}
	}
	// The object itself is not a temp file and must survive.
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("object file was reclaimed by Clean: %v", err)
	}
}

// TestCleanKeepsFreshTempFiles pins the grace rule: with olderThan > 0 a temp
// file younger than the cutoff is left for a later sweep.
func TestCleanKeepsFreshTempFiles(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	dir := filepath.Join(s.base, "aa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, strings.Repeat("ab", 32)+".tmp")
	if err := os.WriteFile(tmp, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Clean(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("Clean(hour) removed %d fresh temp files, want 0", removed)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("fresh temp file was removed: %v", err)
	}
}

// TestPutUniqueTempPerWriterFallback pins that a Put into a directory holding a
// stale temp file still succeeds: createTempExcl falls back to "<hex>.tmp.<n>".
func TestPutUniqueTempPerWriterFallback(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	data := []byte("fallback temp")
	d := digestOf(data)
	dir := filepath.Dir(s.hashPath(d))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Occupy the first candidate name.
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(s.hashPath(d))+".tmp"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put with an occupied temp name = %v", err)
	}
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("Verify after fallback Put = %v", err)
	}
	// The stale temp file is still there, and the object is intact.
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(s.hashPath(d))+".tmp")); err != nil {
		t.Fatalf("stale temp file disappeared: %v", err)
	}
}

// TestBackendOptionsAreAcceptable pins that the exported options compose, so a
// caller can build a store with a custom layout and dir sync together.
func TestBackendOptionsCompose(t *testing.T) {
	s, err := New(t.TempDir(), WithFanOut(4), WithFanLevels(2), WithDirSync())
	if err != nil {
		t.Fatalf("New with combined options = %v", err)
	}
	if err := s.Put(context.Background(), digestOf([]byte("x")), strings.NewReader("x")); err != nil {
		t.Fatalf("Put into a custom layout = %v", err)
	}
	if _, err := New(t.TempDir(), WithFanOut(3), WithFanLevels(22)); err == nil {
		t.Fatal("fan-out beyond MaxFanDepth must be rejected")
	}
}
