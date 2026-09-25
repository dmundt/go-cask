package persistent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFilterWithNoBackingBytesSyncsAndClosesAsANoOp pins syncLocked's empty-view
// guard: a Filter whose raw slice is empty (a hand-built one; newFilter never
// produces it) reports a successful flush and a successful close instead of
// writing or unmapping a buffer it does not own. That guard is what keeps the
// close path safe for a Filter built without a driver.
func TestFilterWithNoBackingBytesSyncsAndClosesAsANoOp(t *testing.T) {
	f := &Filter{}
	if f.IsMapped() {
		t.Fatal("a Filter with no backing bytes must not report a mapped view")
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync with no backing bytes = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close with no backing bytes = %v, want nil", err)
	}
	if f.raw != nil || f.data != nil {
		t.Fatalf("Close left raw=%v data=%v, want both released", f.raw, f.data)
	}
	if f.file != nil {
		t.Fatal("Close left a file handle behind")
	}
}

// TestFilterCloseReportsAFileCloseFailure pins Close's handle-release branch: the
// heap-backed flush succeeds, but releasing the file handle fails (it was closed
// behind the filter's back), and that failure is reported rather than hidden —
// while the flush that did succeed is still on disk, because the write happens
// before the handle is released.
func TestFilterCloseReportsAFileCloseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close-failure.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	raw := []byte("bitset bytes")
	f := &Filter{file: file, raw: raw, mapped: false, path: path}
	err = f.Close()
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Close with an already-closed handle = %v, want os.ErrClosed", err)
	}
	if !strings.Contains(err.Error(), "bloom/persistent: close persistent file") {
		t.Fatalf("Close = %v, want the handle release named", err)
	}
	written, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(written) != string(raw) {
		t.Fatalf("flushed bytes = %q, want %q (the flush precedes the release)", written, raw)
	}
	if f.file != nil {
		t.Fatal("Close must clear the handle even when releasing it failed")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil (Close is idempotent)", err)
	}
}

// TestFilterSyncReportsAMappedFileSyncFailure pins the mapped flush path's second
// step: the mapping is flushed to the page cache successfully, then the file
// handle's own fsync fails (the handle was closed behind the filter's back), and
// that failure is reported — the filter never claims a durable flush it did not
// get.
func TestFilterSyncReportsAMappedFileSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapped-sync.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	f := &Filter{file: file, raw: []byte("mapped bitset"), mapped: true, path: path}
	err = f.Sync()
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Sync with a closed mapped handle = %v, want os.ErrClosed", err)
	}
	if !strings.Contains(err.Error(), "bloom/persistent: sync persistent file") {
		t.Fatalf("Sync = %v, want the file sync named", err)
	}
}

// TestReadPaddedReportsAnUnreadablePath pins the heap fallback's read step: the
// reader opens the backing path by name, so a path that is no longer there is
// reported with that step named (and no buffer returned) rather than yielding a
// zero-filled view that would silently answer "absent" for every recorded
// digest.
func TestReadPaddedReportsAnUnreadablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vanished.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	data, err := readPadded(file, 16)
	if err == nil {
		t.Fatalf("readPadded over a removed path = %d bytes, want an error", len(data))
	}
	if !strings.Contains(err.Error(), "bloom/persistent: read persistent file") {
		t.Fatalf("readPadded = %v, want the read step named", err)
	}
}

// TestRandomKeyReturnsAFreshNonZeroKey pins the success contract that the
// header's process-stability rule rests on, and that the unreachable failure
// branch below exists to protect: every call yields a key that is neither the
// zero key (a zero key would collapse the keyed index hash into an unkeyed one)
// nor a repeat of the previous one (two files must not share an index key by
// accident).
func TestRandomKeyReturnsAFreshNonZeroKey(t *testing.T) {
	first, err := randomKey()
	if err != nil {
		t.Fatalf("randomKey = %v", err)
	}
	if first == (indexKey{}) {
		t.Fatal("randomKey returned the zero key, want a fresh index key")
	}
	second, err := randomKey()
	if err != nil {
		t.Fatalf("second randomKey = %v", err)
	}
	if second == (indexKey{}) {
		t.Fatal("second randomKey returned the zero key, want a fresh index key")
	}
	if first == second {
		t.Fatal("two randomKey calls returned the same key")
	}
}

// Branches in this package's portable code that no deterministic test reaches,
// with the reason recorded (testing-strategy.md §5):
//
//   - newFilter's file.Stat failure (the `_ = file.Close()` before the stat
//     error): the handle was just created by os.OpenFile, and fstat on a live
//     regular file fails only for a hardware fault (EIO) or a descriptor the
//     runtime has already released. Every usable path shape — a missing parent
//     directory, a directory where the file should be, a read-only file, a
//     device, a FIFO — fails earlier, at the open or truncate step, and those
//     branches are covered.
//
//   - randomKey's failure and the constructor's cleanup around initIndexHash
//     (filter.go's `_ = f.closeLocked(); return nil, err`): both fire only when
//     crypto/rand.Read fails, which is a system-entropy failure with no
//     deterministic precondition a test can stage. The cleanup branch exists so
//     that when it does happen the freshly opened handle and mapping are
//     released rather than leaked.
