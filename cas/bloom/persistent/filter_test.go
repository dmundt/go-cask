package persistent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// recordingDriver is a fake mmap driver. It is injected through newFilter, the
// same constructor production code uses, so no package-level state is mutated
// and one test's stub cannot leak into another test.
type recordingDriver struct {
	mapped   bool
	mapErr   error
	flushErr error

	flushes int
	closes  int
}

func (r *recordingDriver) driver() mmapDriver {
	return mmapDriver{
		mmapBytes: func(_ *os.File, size int) (bool, []byte, error) {
			if r.mapErr != nil {
				return false, nil, r.mapErr
			}
			return r.mapped, make([]byte, size), nil
		},
		closeMapped: func([]byte) error {
			r.closes++
			return nil
		},
		flushMapped: func([]byte) error {
			r.flushes++
			return r.flushErr
		},
	}
}

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
	nilFilter.Add(cas.NewDigest([]byte("nil")))
	nilFilter.Reset()
	if nilFilter.Contains(cas.NewDigest([]byte("nil"))) {
		t.Fatal("nil filter should report no membership")
	}
	if nilFilter.IsMapped() {
		t.Fatal("nil filter should not report a mapped view")
	}
}

func TestFilterPersistentRejectsUnboundedParameters(t *testing.T) {
	// The bit-count formula would ask for ~3.8e16 bits here, far above
	// bloom.MaxBits, so the constructor must report an error instead of trying
	// a multi-petabyte allocation.
	if _, err := New(filepath.Join(t.TempDir(), "huge.bin"), 4e15, 0.01); err == nil {
		t.Fatal("expected error for an expected-item count above bloom.MaxBits")
	}
	if _, err := NewFilter(Config{ExpectedItems: 1 << 40, FalsePositiveRate: 1e-9}, filepath.Join(t.TempDir(), "huge2.bin")); err == nil {
		t.Fatal("expected error for an expected-item count above bloom.MaxBits")
	}
}

func TestFilterPersistentSyncWriteError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "nested", "file.bin")
	f := &Filter{file: &os.File{}, raw: []byte{0x01, 0x02}, path: path, mapped: false}
	if err := f.Sync(); err == nil {
		t.Fatal("Sync with unwritable target path should return an error")
	}
}

func TestFilterPersistentCloseWithFallbackWrite(t *testing.T) {
	rec := &recordingDriver{mapped: false}
	path := filepath.Join(t.TempDir(), "fallback.bin")
	p, err := newFilter(Config{ExpectedItems: 256, FalsePositiveRate: 0.01}, path, rec.driver())
	if err != nil {
		t.Fatal(err)
	}
	// A heap-backed filter owns the whole file — header and bitset — so Close
	// writes it in one call.
	p.raw[headerSize] = 0x01
	want := int64(len(p.raw))
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != want {
		t.Fatalf("persistent file size = %d, want %d (header plus bitset)", info.Size(), want)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(written, magic[:]) {
		t.Fatalf("heap-backed Close did not persist the header: % x", written[:min(len(written), headerSize)])
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
	f := &Filter{file: file, raw: []byte{0x01, 0x02, 0x03, 0x04}, mapped: true, path: path}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFilterPersistentReportsAndFlushesMappedMode(t *testing.T) {
	rec := &recordingDriver{mapped: true}
	path := filepath.Join(t.TempDir(), "mapped-report.bin")
	f, err := newFilter(Config{ExpectedItems: 64, FalsePositiveRate: 0.01}, path, rec.driver())
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsMapped() {
		t.Fatal("expected IsMapped() to report true for a mapped filter")
	}
	d := cas.NewDigest([]byte("mapped value"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("mapped filter should contain the stored digest")
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if rec.flushes != 1 {
		t.Fatalf("flush calls = %d, want 1", rec.flushes)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if rec.closes != 1 {
		t.Fatalf("unmap calls = %d, want 1", rec.closes)
	}
	if f.IsMapped() {
		t.Fatal("IsMapped() should report false after Close")
	}
}

func TestFilterPersistentHeapModeWritesThroughToDisk(t *testing.T) {
	rec := &recordingDriver{mapped: false}
	path := filepath.Join(t.TempDir(), "heap.bin")
	f, err := newFilter(Config{ExpectedItems: 64, FalsePositiveRate: 0.01}, path, rec.driver())
	if err != nil {
		t.Fatal(err)
	}
	if f.IsMapped() {
		t.Fatal("expected IsMapped() to report false for a heap-backed filter")
	}
	d := cas.NewDigest([]byte("heap value"))
	f.Add(d)
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if rec.flushes != 0 {
		t.Fatalf("flush calls = %d, want 0 for a heap-backed filter", rec.flushes)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(path, 64, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.Contains(d) {
		t.Fatal("bits written by a heap-backed Sync should survive a reopen")
	}
}

func TestFilterPersistentUseAfterCloseIsSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.bin")
	f, err := New(path, 256, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("closed value"))
	f.Add(d)
	if !f.Contains(d) {
		t.Fatal("filter should contain the stored digest before Close")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if f.IsMapped() {
		t.Fatal("IsMapped() should report false after Close")
	}

	// None of these may touch released memory: on unix the mapping is gone, so
	// a stale f.data would fault here.
	f.Add(cas.NewDigest([]byte("after close")))
	f.Reset()
	if f.Contains(d) {
		t.Fatal("Contains on a closed filter should report false")
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync after Close = %v, want nil no-op", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
}

func TestFilterPersistentClosePropagatesDriverErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close-error.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &Filter{
		file:   file,
		raw:    []byte("abc"),
		mapped: true,
		path:   path,
		driver: mmapDriver{closeMapped: func([]byte) error { return errors.New("unmap failure") }},
	}
	if err := f.Close(); err == nil {
		t.Fatal("expected Close to report the unmapping failure")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
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
	// data is a heap fallback buffer, not a mapping: it must not be handed to
	// closeMapped, because unmapping heap memory is not valid.

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
	if err := p.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewFilterWithoutDriverReportsError(t *testing.T) {
	// A Filter built without a mapping driver must report an error rather than
	// panicking on a nil driver function.
	if _, err := newFilter(Config{ExpectedItems: 32, FalsePositiveRate: 0.01}, filepath.Join(t.TempDir(), "no-driver.bin"), mmapDriver{}); err == nil {
		t.Fatal("expected a zero-value mmap driver to report an error")
	}
}

func TestFilterPersistentWrapperErrorBranches(t *testing.T) {
	rec := &recordingDriver{mapErr: os.ErrNotExist}
	if _, err := newFilter(Config{ExpectedItems: 32, FalsePositiveRate: 0.01}, filepath.Join(t.TempDir(), "wrapper.bin"), rec.driver()); err == nil {
		t.Fatal("expected mmap driver failure to surface")
	}

	path := filepath.Join(t.TempDir(), "wrapper-close.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f := &Filter{file: file, raw: []byte("abc"), mapped: false, path: filepath.Join(t.TempDir(), "missing", "close.bin")}
	if err := f.Close(); err == nil {
		t.Fatal("expected Close to error when a heap-backed Sync cannot write to an invalid path")
	}

	rec = &recordingDriver{mapped: true, flushErr: os.ErrInvalid}
	f, err = newFilter(Config{ExpectedItems: 32, FalsePositiveRate: 0.01}, filepath.Join(t.TempDir(), "flush.bin"), rec.driver())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err == nil {
		t.Fatal("expected Sync to fail when mapped flush returns an error")
	}
	if err := f.Close(); err == nil {
		t.Fatal("expected Close to report the mapped flush failure")
	}
	if rec.closes != 1 {
		t.Fatalf("unmap calls = %d, want 1: Close must release the view even after a flush failure", rec.closes)
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
	if err := os.WriteFile(path, []byte{0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
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
	if mapped {
		if err := closeMapped(data); err != nil {
			t.Fatal(err)
		}
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
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
