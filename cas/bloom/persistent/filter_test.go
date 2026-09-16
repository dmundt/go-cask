package persistent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"github.com/dmundt/go-cask/cas"
)

func TestFilterPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bloom.bin")
	p, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("persistent value"))
	p.Add(d)
	if !p.Contains(d) {
		t.Fatal("persistent filter should contain stored digest")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	reopen, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	defer reopen.Close()
	if !reopen.Contains(d) {
		t.Fatal("persistent filter should survive reopen")
	}
}

func TestFilterPersistentValidationAndLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.bin")
	if _, err := New(path, 0, 0.01); err == nil {
		t.Fatal("expected error for zero expected items")
	}
	if _, err := New(path, 1024, 0); err == nil {
		t.Fatal("expected error for invalid false positive rate")
	}
	if _, err := NewFilter(Config{ExpectedItems: 32, FalsePositiveRate: 0.01}, filepath.Join(t.TempDir(), "missing", "nested", "file.bin")); err == nil {
		t.Fatal("expected error when parent directory is missing")
	}

	p, err := New(path, 1024, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	zero := cas.Digest{}
	if p.Contains(zero) {
		t.Fatal("zero digest should never be present")
	}
	p.Add(zero)
	p.Reset()
	if err := p.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	var nilFilter *Filter
	if err := nilFilter.Close(); err != nil {
		t.Fatal("nil filter Close should be a no-op")
	}
	if err := nilFilter.Sync(); err != nil {
		t.Fatal("nil filter Sync should be a no-op")
	}
}

func TestFilterPersistentSyncWriteError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "nested", "file.bin")
	f := &Filter{file: &os.File{}, data: []byte{0x01, 0x02}, path: path, mapped: false}
	if err := f.Sync(); err == nil {
		t.Fatal("Sync with unwritable target path should return an error")
	}
}

func TestFilterPersistentCloseWithFallbackWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fallback.bin")
	p, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	p.mapped = false
	p.data = []byte{0xFF, 0x00, 0x01}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterPersistentLargeFileResize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resize.bin")
	if err := os.WriteFile(path, []byte{0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(path, 64, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.data) == 0 {
		t.Fatal("resized persistent filter should have bytes allocated")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterPersistentMappedAndSyncBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapped.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0x01, 0x02, 0x03, 0x04}); err != nil {
		t.Fatal(err)
	}
	f := &Filter{file: file, data: []byte{0x01, 0x02, 0x03, 0x04}, mapped: true, path: path}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentHelpersAndLifecycleBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helpers.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	ok, data, err := mmapBytes(file, 0)
	if err != nil || ok || data != nil {
		t.Fatalf("mmapBytes(0) = (%v, %v, %v), want (false, nil, nil)", ok, data, err)
	}

	if _, err := file.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	ok, data, err = mmapBytes(file, 8)
	if err != nil || ok {
		t.Fatalf("mmapBytes(8) = (%v, %v, %v), want (false, data, nil)", ok, data, err)
	}
	if len(data) != 8 {
		t.Fatalf("mmapBytes(8) len(data) = %d, want 8", len(data))
	}
	if err := closeMapped(data); err != nil {
		t.Fatal(err)
	}

	file2, err := os.Create(filepath.Join(t.TempDir(), "mapped.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer file2.Close()
	if err := file2.Truncate(16); err != nil {
		t.Fatal(err)
	}
	f, err := New(file2.Name(), 64, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.mapped = true
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var nilFilter *Filter
	if err := nilFilter.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := nilFilter.Close(); err != nil {
		t.Fatal(err)
	}

	p, err := New(filepath.Join(t.TempDir(), "sync.bin"), 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	p.mapped = false
	p.data = nil
	if err := p.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterPersistentWrapperErrorBranches(t *testing.T) {
	oldOps := mmapOps
	defer func() { mmapOps = oldOps }()

	mmapOps = mmapDriver{
		mmapBytes: func(*os.File, int) (bool, []byte, error) { return false, nil, os.ErrNotExist },
		closeMapped: func([]byte) error { return nil },
		flushMapped: func([]byte) error { return nil },
		closeMappedByAddr: func(uintptr, int) error { return nil },
		flushMappedByAddr: func(uintptr, int) error { return nil },
	}
	_, err := NewFilter(Config{ExpectedItems: 32, FalsePositiveRate: 0.01}, filepath.Join(t.TempDir(), "wrapper.bin"))
	if err == nil {
		t.Fatal("expected mmap wrapper failure")
	}

	path := filepath.Join(t.TempDir(), "wrapper-close.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f := &Filter{file: file, data: []byte("abc"), mapped: false, path: filepath.Join(t.TempDir(), "missing", "close.bin")}
	mmapOps.closeMappedByAddr = func(uintptr, int) error { return nil }
	if err := f.Close(); err == nil {
		t.Fatal("expected Close to error when non-mapped Sync cannot write to invalid path")
	}

	f = &Filter{file: file, data: []byte("abc"), mappedAddr: uintptr(unsafe.Pointer(&f.data[0])), path: path}
	mmapOps.flushMappedByAddr = func(uintptr, int) error { return os.ErrInvalid }
	if err := f.Sync(); err == nil {
		t.Fatal("expected Sync to fail when mapped flush returns an error")
	}

	mmapOps.closeMappedByAddr = func(uintptr, int) error { return errors.New("close failure") }
	f = &Filter{file: file, data: []byte("abc"), mappedAddr: uintptr(unsafe.Pointer(&f.data[0])), path: path}
	if err := f.Close(); err == nil {
		t.Fatal("expected Close to fail when mapped close returns an error")
	}
}

func TestFilterPersistentCustomHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "customhash.bin")
	f, err := NewFilter(Config{ExpectedItems: 256, FalsePositiveRate: 0.01, Hash: func(data []byte, i int) uint64 { return uint64(i + len(data)) }}, path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := cas.NewDigest([]byte("custom"))
	f.Add(want)
	if !f.Contains(want) {
		t.Fatal("custom hash filter should contain inserted digest")
	}
}

func TestFilterPersistentMmapEdgeCases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mmap-edge.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, data, err := mmapBytes(file, 0)
	if err != nil || mapped || data != nil {
		t.Fatalf("mmapBytes(0) = (%v, %v, %v), want false, nil, nil", mapped, data, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closeMapped(nil); err != nil {
		t.Fatal(err)
	}
	if err := closeMapped([]byte{}); err != nil {
		t.Fatal(err)
	}

	want := bytes.Repeat([]byte("A"), 64)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, data, err = mmapBytes(file, len(want))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(want) {
		t.Fatalf("mmapBytes(file,%d) len = %d, want %d", len(want), len(data), len(want))
	}
	if err := closeMapped(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mapped, data, err = mmapBytes(file, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 16 {
		t.Fatalf("mmapBytes(file,16) len = %d, want 16", len(data))
	}
	if mapped {
		if err := closeMapped(data); err != nil {
			t.Fatal(err)
		}
	} else if err := closeMapped(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterPersistentMappedAddrLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapped-addr.bin")
	f, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.data) == 0 {
		t.Fatal("expected data buffer to be allocated")
	}
	f.mapped = true
	f.mappedAddr = uintptr(unsafe.Pointer(&f.data[0]))
	mappedViews.Store(f.mappedAddr, true)
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterPersistentExtendedCoverageBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extended.bin")
	oldOps := mmapOps
	defer func() { mmapOps = oldOps }()

	mmapOps = mmapDriver{
		mmapBytes: func(*os.File, int) (bool, []byte, error) { return true, []byte("abcdef"), nil },
		closeMapped: func([]byte) error { return nil },
		flushMapped: func([]byte) error { return nil },
		closeMappedByAddr: func(uintptr, int) error { return nil },
		flushMappedByAddr: func(uintptr, int) error { return nil },
	}
	f, err := NewFilter(Config{ExpectedItems: 32, FalsePositiveRate: 0.01}, path)
	if err != nil {
		t.Fatal(err)
	}
	if !f.mapped || f.mappedAddr == 0 {
		t.Fatal("expected mapped filter to retain mapped address")
	}
	f.data = make([]byte, 1024)
	f.m = 8192
	if f.Contains(cas.NewDigest([]byte("missing"))) {
		t.Fatal("missing digest should not be found")
	}
	f.mappedAddr = uintptr(unsafe.Pointer(&f.data[0]))
	mmapOps.closeMappedByAddr = func(uintptr, int) error { return syscall.EINVAL }
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	mmapOps.closeMappedByAddr = func(uintptr, int) error { return nil }

	data := []byte("xyz")
	if err := mmapOps.closeMappedByAddr(0, len(data)); err != nil {
		t.Fatal(err)
	}
	if err := mmapOps.flushMappedByAddr(0, len(data)); err != nil {
		t.Fatal(err)
	}
	addr := uintptr(unsafe.Pointer(&data[0]))
	_ = mmapOps.closeMappedByAddr(addr, len(data))
	_ = mmapOps.flushMappedByAddr(addr, len(data))
}
