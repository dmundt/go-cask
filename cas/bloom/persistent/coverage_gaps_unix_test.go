//go:build !windows

package persistent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestNewFilterReportsResizeFailureOnAnUntruncatableFile pins the constructor's
// resize branch: the backing path opens and stats fine but cannot be truncated
// to the configured size, so the constructor reports the resize step instead of
// memory-mapping a file it could not size (which would leave the bitset and its
// header disagreeing about the file's length). /dev/null is the deterministic
// case: a character device whose size is always 0 and whose ftruncate is
// rejected with EINVAL.
func TestNewFilterReportsResizeFailureOnAnUntruncatableFile(t *testing.T) {
	_, err := New(os.DevNull, 256, 0.01)
	if err == nil {
		t.Fatal("New over an untruncatable device = nil, want a resize failure")
	}
	if !strings.Contains(err.Error(), "bloom/persistent: resize persistent file") {
		t.Fatalf("New = %v, want the resize step named", err)
	}
}

// TestMmapBytesHeapFallbackBranches pins every branch of the unix mapping helper
// that ends in the heap fallback: the file is shorter than the requested size
// and its path is gone, the descriptor cannot be mapped and the path is gone,
// and the descriptor cannot be mapped while the path is still readable. The last
// case is the one that must succeed, with a padded buffer, because that is what
// keeps a filter durable on a platform or path without a usable mapping.
func TestMmapBytesHeapFallbackBranches(t *testing.T) {
	t.Run("shorter than the request and unreadable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "short.bin")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, err := file.Write([]byte("1234")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		mapped, data, err := mmapBytes(file, 16)
		if err == nil {
			t.Fatalf("mmapBytes over a vanished shorter file = (%v, %d bytes), want an error", mapped, len(data))
		}
		if mapped || data != nil {
			t.Fatalf("mmapBytes = (%v, %v), want (false, nil)", mapped, data)
		}
		if !strings.Contains(err.Error(), "bloom/persistent: read persistent file") {
			t.Fatalf("mmapBytes = %v, want the read step named", err)
		}
	})

	t.Run("unmappable descriptor and unreadable path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "unmappable-gone.bin")
		if err := os.WriteFile(path, bytes.Repeat([]byte("A"), 64), 0o644); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		// Remove the path first, then close the handle: mmap then fails on the
		// released descriptor and the heap reader has no path to fall back to.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		mapped, data, err := mmapBytes(file, 64)
		if err == nil {
			t.Fatalf("mmapBytes over a released descriptor = (%v, %d bytes), want an error", mapped, len(data))
		}
		if mapped || data != nil {
			t.Fatalf("mmapBytes = (%v, %v), want (false, nil)", mapped, data)
		}
		if !strings.Contains(err.Error(), "bloom/persistent: read persistent file") {
			t.Fatalf("mmapBytes = %v, want the read step named", err)
		}
	})

	t.Run("unmappable descriptor with a readable path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "unmappable.bin")
		want := bytes.Repeat([]byte("B"), 64)
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		mapped, data, err := mmapBytes(file, len(want))
		if err != nil {
			t.Fatalf("mmapBytes with a readable heap fallback = %v, want nil", err)
		}
		if mapped {
			t.Fatal("a released descriptor cannot be mapped, so the result must be a heap buffer")
		}
		if !bytes.Equal(data, want) {
			t.Fatalf("heap fallback returned %d bytes, want the file's %d bytes verbatim", len(data), len(want))
		}
	})

	t.Run("heap fallback pads a shorter file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "padded.bin")
		if err := os.WriteFile(path, []byte("1234"), 0o644); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()

		mapped, data, err := mmapBytes(file, 16)
		if err != nil {
			t.Fatalf("mmapBytes over a short file = %v, want nil", err)
		}
		if mapped {
			t.Fatal("a file shorter than the request cannot be mapped to the requested size")
		}
		if len(data) != 16 {
			t.Fatalf("heap fallback length = %d, want 16", len(data))
		}
		if !bytes.Equal(data[:4], []byte("1234")) {
			t.Fatalf("heap fallback = %q, want the stored prefix first", data[:4])
		}
		if !bytes.Equal(data[4:], make([]byte, 12)) {
			t.Fatalf("heap fallback padding = %q, want zero bytes", data[4:])
		}
	})
}

// TestCloseMappedReportsADoubleRelease pins the unmap error branch: releasing a
// range twice is exactly the mistake the wrapper exists to surface instead of
// swallowing, so the second release reports the kernel's refusal (EINVAL) with
// the step named.
func TestCloseMappedReportsADoubleRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "double-unmap.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("C"), 4096), 0o644); err != nil {
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
	if err := closeMapped(data); err != nil {
		t.Fatalf("first release = %v, want nil", err)
	}
	err = closeMapped(data)
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("second release = %v, want the kernel's EINVAL", err)
	}
	if !strings.Contains(err.Error(), "bloom/persistent: unmap persistent file") {
		t.Fatalf("second release = %v, want the unmap step named", err)
	}
}

// TestFlushMappedNoOpsOnAnEmptyViewAndReportsAReleasedRange pins both halves of
// the flush wrapper: an empty view is a no-op (there is nothing to write back),
// while a range that is no longer mapped is reported with the step named rather
// than silently accepted as a durable flush. The released-range shape is the
// only deterministic way to make msync fail — a live mapping of a real file on a
// healthy filesystem always succeeds — and the wrapper's caller contract already
// forbids it, so the test pins the reporting, not a supported call.
func TestFlushMappedNoOpsOnAnEmptyViewAndReportsAReleasedRange(t *testing.T) {
	if err := flushMapped(nil); err != nil {
		t.Fatalf("flushMapped(nil) = %v, want nil", err)
	}
	if err := flushMapped([]byte{}); err != nil {
		t.Fatalf("flushMapped(empty) = %v, want nil", err)
	}

	path := filepath.Join(t.TempDir(), "released-flush.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("D"), 4096), 0o644); err != nil {
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
	if err := flushMapped(data); err != nil {
		t.Fatalf("flushMapped(live mapping) = %v, want nil", err)
	}
	if err := closeMapped(data); err != nil {
		t.Fatal(err)
	}

	err = flushMapped(data)
	if !errors.Is(err, syscall.ENOMEM) {
		t.Fatalf("flushMapped(released range) = %v, want the kernel's ENOMEM", err)
	}
	if !strings.Contains(err.Error(), "bloom/persistent: flush persistent file") {
		t.Fatalf("flushMapped(released range) = %v, want the flush step named", err)
	}
}
