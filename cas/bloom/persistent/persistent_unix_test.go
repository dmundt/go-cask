//go:build !windows

package persistent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFilterPersistentMappedAddrLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapped-addr.bin")
	f, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if !f.mapped || f.mappedAddr == 0 {
		t.Fatal("expected persistent filter to retain mapped view")
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentMmapUnixBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mmap.bin")
	want := bytes.Repeat([]byte("A"), 64)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, data, err := mmapBytes(file, len(want))
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !mapped {
		t.Fatal("expected mmapBytes to use a mapped backing store for a file large enough to cover the request")
	}
	if len(data) != len(want) {
		t.Fatalf("mmapBytes(file,%d) len = %d, want %d", len(want), len(data), len(want))
	}
	addr := slicePtr(data)
	if _, ok := mappedViews.Load(addr); !ok {
		t.Fatal("mmapBytes did not register mapped view")
	}
	if err := flushMappedByAddr(addr, len(data)); err != nil {
		t.Fatal(err)
	}
	if err := closeMappedByAddr(addr, len(data)); err != nil {
		t.Fatal(err)
	}
	if _, ok := mappedViews.Load(addr); ok {
		t.Fatal("closeMappedByAddr did not remove mapped view")
	}
	if err := closeMapped(nil); err != nil {
		t.Fatal(err)
	}
	if err := closeMapped([]byte{}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, data, err = mmapBytes(file, 16)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if mapped || len(data) != 16 {
		t.Fatalf("mmapBytes(file,16) = (%v, %d), want (false, 16)", mapped, len(data))
	}
	if err := closeMapped(data); err != nil {
		t.Fatal("closeMapped on a fallback buffer should be a no-op")
	}
	if mapped2, data2, err := mmapBytes(file, 0); err != nil || mapped2 || data2 != nil {
		t.Fatalf("mmapBytes(0) = (%v, %v, %v), want (false, nil, nil)", mapped2, data2, err)
	}
	if mapped3, data3, err := mmapBytes(file, -1); err != nil || mapped3 || data3 != nil {
		t.Fatalf("mmapBytes(-1) = (%v, %v, %v), want (false, nil, nil)", mapped3, data3, err)
	}
}
