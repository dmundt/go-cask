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

func TestPersistentWindowsMappedHelpers(t *testing.T) {
	if err := flushMapped(nil); err != nil {
		t.Fatal(err)
	}
	if err := closeMappedByAddr(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := flushMappedByAddr(0, 0); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "mapped-helpers.bin")
	if err := os.WriteFile(path, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	mapped, data, err := mmapBytes(file, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !mapped && len(data) == 0 {
		t.Fatal("expected a mapped view or fallback bytes")
	}
	ptr := slicePtr(data)
	mappedViews.Store(ptr, true)
	if err := flushMapped(data); err != nil {
		_ = err
	}
	if err := flushMappedByAddr(ptr, len(data)); err != nil {
		_ = err
	}
	if err := closeMapped(data); err != nil {
		_ = err
	}
	if err := closeMappedByAddr(ptr, len(data)); err != nil {
		_ = err
	}
	if err := closeMappedByAddr(0, 8); err != nil {
		t.Fatal(err)
	}
	if err := flushMappedByAddr(0, 8); err != nil {
		t.Fatal(err)
	}
	if !mapped && len(data) > 0 {
		_ = data
	}

	badPtr := uintptr(0xDEADBEEF)
	mappedViews.Store(badPtr, true)
	if err := flushMappedByAddr(badPtr, 8); err == nil {
		t.Fatal("expected invalid mapped pointer to fail flush")
	}
	if err := closeMappedByAddr(badPtr, 8); err == nil {
		t.Fatal("expected invalid mapped pointer to fail close")
	}

	badFile, err := os.CreateTemp(t.TempDir(), "bad-filter-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer badFile.Close()
	f := &Filter{file: badFile, data: []byte("abcd"), mapped: true, mappedAddr: badPtr, path: badFile.Name()}
	if err := f.Sync(); err != nil {
		_ = err
	}
	if err := f.Close(); err != nil {
		_ = err
	}
}

func TestPersistentWindowsMappedAddrHelpers(t *testing.T) {
	_ = flushMapped(nil)
	_ = closeMapped(nil)
	_ = flushMappedByAddr(0, 64)
	_ = closeMappedByAddr(0, 64)

	buf := make([]byte, 32)
	addr := slicePtr(buf)
	mappedViews.Store(addr, true)
	_ = flushMapped(buf)
	_ = flushMappedByAddr(addr, len(buf))
	_ = closeMapped(buf)
	_ = closeMappedByAddr(addr, len(buf))
	mappedViews.Delete(addr)

	invalidAddr := uintptr(0xFEEDFACE)
	mappedViews.Store(invalidAddr, true)
	_ = flushMappedByAddr(invalidAddr, 8)
	_ = closeMappedByAddr(invalidAddr, 8)
	mappedViews.Delete(invalidAddr)
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
