//go:build windows

package persistent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func TestPersistentMmapWindowsBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mmap.bin")
	if err := os.WriteFile(path, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	mapped, data, err := mmapBytes(file, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 8 {
		t.Fatalf("mmapBytes(file,8) len = %d, want 8", len(data))
	}
	if mapped {
		t.Fatal("windows fallback is expected to return mapped=false")
	}
	if err := closeMapped(nil); err != nil {
		t.Fatal(err)
	}
	if err := closeMapped([]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := closeMapped(data); err != nil {
		t.Fatal(err)
	}
	if err := flushMapped(data); err != nil {
		t.Fatal(err)
	}
	if mapped2, data2, err := mmapBytes(file, 0); err != nil || mapped2 || data2 != nil {
		t.Fatalf("mmapBytes(0) = (%v, %v, %v), want (false, nil, nil)", mapped2, data2, err)
	}
	if mapped3, data3, err := mmapBytes(file, -1); err != nil || mapped3 || data3 != nil {
		t.Fatalf("mmapBytes(-1) = (%v, %v, %v), want (false, nil, nil)", mapped3, data3, err)
	}
}

func TestFilterPersistentWindowsUsesHeapMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "windows-heap.bin")
	f, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if f.IsMapped() {
		t.Fatal("windows filters are heap-backed until real memory mapping is implemented")
	}
	d := cas.NewDigest([]byte("windows heap value"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("heap-backed windows filter should contain the stored digest")
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.Contains(d) {
		t.Fatal("windows filter should survive reopen")
	}
}

func TestPersistentWindowsMmapFallbackErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mmapBytes(file, 8); err == nil {
		t.Fatal("expected mmapBytes to fail on a deleted file path")
	}

	dir := filepath.Join(t.TempDir(), "mmap-dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	df, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer df.Close()
	if _, _, err := mmapBytes(df, 16); err == nil {
		t.Fatal("expected mmapBytes to fail for a directory-backed file handle")
	}
}
