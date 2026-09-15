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
