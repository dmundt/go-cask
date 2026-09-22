//go:build !windows

package persistent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func TestFilterPersistentMapsFileOnUnix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapped-addr.bin")
	f, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsMapped() {
		t.Fatal("expected the persistent filter to memory map its backing file on unix")
	}
	d := cas.NewDigest([]byte("mapped on unix"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("mapped filter should contain the stored digest")
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if f.IsMapped() {
		t.Fatal("IsMapped() should report false after Close")
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
	if err := closeMapped(data); err != nil {
		t.Fatal(err)
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
	if mapped2, data2, err := mmapBytes(file, 0); err != nil || mapped2 || data2 != nil {
		t.Fatalf("mmapBytes(0) = (%v, %v, %v), want (false, nil, nil)", mapped2, data2, err)
	}
	if mapped3, data3, err := mmapBytes(file, -1); err != nil || mapped3 || data3 != nil {
		t.Fatalf("mmapBytes(-1) = (%v, %v, %v), want (false, nil, nil)", mapped3, data3, err)
	}
}

func TestPersistentFlushMappedUnix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flush.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("B"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	mapped, data, err := mmapBytes(file, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !mapped {
		t.Fatal("expected a writable mapping for an O_RDWR handle")
	}
	data[0] = 'C'
	if err := flushMapped(data); err != nil {
		t.Fatal(err)
	}
	if err := closeMapped(data); err != nil {
		t.Fatal(err)
	}
}
