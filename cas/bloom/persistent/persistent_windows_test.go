//go:build windows

package persistent

import (
	"os"
	"path/filepath"
	"testing"
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
	if mapped2, data2, err := mmapBytes(file, 0); err != nil || mapped2 || data2 != nil {
		t.Fatalf("mmapBytes(0) = (%v, %v, %v), want (false, nil, nil)", mapped2, data2, err)
	}
	if mapped3, data3, err := mmapBytes(file, -1); err != nil || mapped3 || data3 != nil {
		t.Fatalf("mmapBytes(-1) = (%v, %v, %v), want (false, nil, nil)", mapped3, data3, err)
	}
}
