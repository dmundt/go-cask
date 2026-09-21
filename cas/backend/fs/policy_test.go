package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBase(t *testing.T) {
	if err := ValidateBase(""); err == nil {
		t.Fatal("ValidateBase(empty) = nil error, want error")
	}
	if err := ValidateBase("."); err == nil {
		t.Fatal("ValidateBase('.') = nil error, want error")
	}
	if err := ValidateBase(filepath.Join("tmp", "store")); err != nil {
		t.Fatalf("ValidateBase(tmp/store) = %v, want nil", err)
	}
}

func TestEnsureBaseAndCleanupTemp(t *testing.T) {
	base := filepath.Join(t.TempDir(), "store")
	if err := EnsureBase(base); err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "tmp-001.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatalf("Write temp file: %v", err)
	}
	if err := CleanupTemp(base); err != nil {
		t.Fatalf("CleanupTemp: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "tmp-001.tmp")); !os.IsNotExist(err) {
		t.Fatalf("CleanupTemp should remove .tmp files: stat err=%v", err)
	}
}

// TestCleanupTempEdges covers the two branches CleanupTemp has beyond the
// happy path: an unusable base is rejected before any walk, and a base that
// does not exist is not an error — the helper is advisory, and there is
// nothing to clean up in a store that was never created.
func TestCleanupTempEdges(t *testing.T) {
	if err := CleanupTemp(""); err == nil {
		t.Fatal("CleanupTemp(empty) = nil error, want error")
	}
	missing := filepath.Join(t.TempDir(), "never-created")
	if err := CleanupTemp(missing); err != nil {
		t.Fatalf("CleanupTemp(missing base) = %v, want nil", err)
	}
	if err := EnsureBase(""); err == nil {
		t.Fatal("EnsureBase(empty) = nil error, want error")
	}
}
