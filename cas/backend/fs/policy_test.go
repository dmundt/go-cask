package fs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateBase(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		".",
		string(filepath.Separator),
		"..",
		"../x",
		".." + string(filepath.Separator) + "x",
		filepath.Join("a", "..", ".."), // cleans to ".."
	}
	if vol := filepath.VolumeName(filepath.Clean(os.TempDir())); vol != "" {
		// "C:" and "C:\" are volume roots, not store directories.
		invalid = append(invalid, vol, vol+string(filepath.Separator))
	}
	for _, base := range invalid {
		if err := ValidateBase(base); err == nil {
			t.Errorf("ValidateBase(%q) = nil error, want error", base)
		}
	}

	valid := []string{
		filepath.Join("tmp", "store"),
		filepath.Join("a", "b", "c"),
	}
	if abs := filepath.Join(t.TempDir(), "store"); abs != "" {
		valid = append(valid, abs)
	}
	for _, base := range valid {
		if err := ValidateBase(base); err != nil {
			t.Errorf("ValidateBase(%q) = %v, want nil", base, err)
		}
	}
}

func TestEnsureBaseAndCleanupTemp(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	if err := EnsureBase(ctx, base); err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "tmp-001.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatalf("Write temp file: %v", err)
	}
	if err := CleanupTemp(ctx, base); err != nil {
		t.Fatalf("CleanupTemp: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "tmp-001.tmp")); !os.IsNotExist(err) {
		t.Fatalf("CleanupTemp should remove .tmp files: stat err=%v", err)
	}
}

// TestCleanupTempEdges covers the branches CleanupTemp has beyond the happy
// path: an unusable base is rejected before any walk, a base that does not
// exist is not an error — the helper is advisory, and there is nothing to clean
// up in a store that was never created — and a canceled context stops the
// sweep before it touches anything.
func TestCleanupTempEdges(t *testing.T) {
	ctx := context.Background()
	if err := CleanupTemp(ctx, ""); err == nil {
		t.Fatal("CleanupTemp(empty) = nil error, want error")
	}
	missing := filepath.Join(t.TempDir(), "never-created")
	if err := CleanupTemp(ctx, missing); err != nil {
		t.Fatalf("CleanupTemp(missing base) = %v, want nil", err)
	}
	if err := EnsureBase(ctx, ""); err == nil {
		t.Fatal("EnsureBase(empty) = nil error, want error")
	}

	base := t.TempDir()
	tempPath := filepath.Join(base, "stale.tmp")
	if err := os.WriteFile(tempPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := CleanupTemp(canceled, base); err == nil {
		t.Fatal("CleanupTemp(canceled ctx) = nil error, want context.Canceled")
	}
	if _, err := os.Stat(tempPath); err != nil {
		t.Fatalf("a canceled sweep must not remove files: %v", err)
	}
	if err := EnsureBase(canceled, filepath.Join(base, "sub")); err == nil {
		t.Fatal("EnsureBase(canceled ctx) = nil error, want context.Canceled")
	}
}

// TestValidateBaseRejectsTraversal pins the guard the doc comment promises:
// ".." and any "..<sep>" prefix would make CleanupTemp walk the parent
// directory and delete foreign *.tmp files, so they must be rejected.
func TestValidateBaseRejectsTraversal(t *testing.T) {
	for _, base := range []string{"..", ".." + string(filepath.Separator), filepath.Join("..", "sibling")} {
		if err := ValidateBase(base); err == nil {
			t.Fatalf("ValidateBase(%q) = nil, want rejection", base)
		}
		if err := EnsureBase(context.Background(), base); err == nil {
			t.Fatalf("EnsureBase(%q) = nil, want rejection", base)
		}
		if err := CleanupTemp(context.Background(), base); err == nil {
			t.Fatalf("CleanupTemp(%q) = nil, want rejection", base)
		}
	}
}

// TestCleanupTempReportsRemovalFailure covers CleanupTemp's removal branch: a
// temp file the sweep may not delete is reported rather than silently left
// behind, because "no error" would tell an operator the store is tidy when it
// is not. POSIX permission bits are the only portable way to produce the
// failure, so the test is skipped on Windows (ACLs, not mode bits) and under a
// superuser account (permission checks do not apply).
func TestCleanupTempReportsRemovalFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	base := t.TempDir()
	locked := filepath.Join(base, "aa")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "stale.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Readable and searchable (so the walk reaches the temp file) but not
	// writable (so the removal fails).
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if err := CleanupTemp(context.Background(), base); err == nil {
		t.Fatal("CleanupTemp must report a temp file it cannot remove")
	}
}
