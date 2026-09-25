package packfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
)

// TestAppendPackRecordIsANoOpWhenPackingIsDisabled pins the guard at the top of
// appendPackRecord: with packing disabled the function writes nothing at all —
// no pack bytes, no index entry, and no change to the active pack file — so a
// caller that reaches it outside Put (Put returns before the spool) still gets
// the loose-only behaviour packfs promises without WithEnabled.
func TestAppendPackRecordIsANoOpWhenPackingIsDisabled(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "disabled-append"))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	before, err := os.Stat(backend.packFilePath)
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("disabled-append"))
	if err := backend.appendPackRecord(ctx, d, bytesReader([]byte("payload")), int64(len("payload"))); err != nil {
		t.Fatalf("appendPackRecord with packing disabled = %v, want nil", err)
	}
	if len(backend.index) != 0 {
		t.Fatalf("appendPackRecord indexed %v, want nothing while packing is disabled", backend.index)
	}
	if backend.packBytes != 0 || backend.packEntries != 0 {
		t.Fatalf("appendPackRecord accounted %d bytes / %d entries, want 0 / 0", backend.packBytes, backend.packEntries)
	}
	after, err := os.Stat(backend.packFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("pack file grew from %d to %d bytes with packing disabled", before.Size(), after.Size())
	}
}

// TestPutReportsPackRotationFailures pins both rotation triggers: when the
// active pack must be rotated because it reached its entry limit and because it
// reached its byte limit, a rotation that cannot open the next pack file fails
// the Put instead of recording an index entry for a record that was never
// written. The open is driven through the backend's own file seam because which
// directory a pack can be created in is a platform question; the failure is the
// real open error the seam returns.
func TestPutReportsPackRotationFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"entry limit", []Option{WithEnabled(), WithPackMaxEntries(1), WithPackMaxBytes(0)}},
		{"byte limit", []Option{WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			backend, err := New(filepath.Join(t.TempDir(), "rotate-fail"), tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()

			first := cas.NewDigest([]byte("rotate-first"))
			if err := backend.Put(ctx, first, bytesReader([]byte("one"))); err != nil {
				t.Fatalf("first Put = %v", err)
			}
			if backend.packEntries == 0 {
				t.Fatal("the first Put must have appended one pack record")
			}

			rotateErr := errors.New("pack rotation failed")
			backend.op.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
				if strings.HasPrefix(filepath.Base(name), "pack-") {
					return nil, rotateErr
				}
				return os.OpenFile(name, flag, perm)
			}

			second := cas.NewDigest([]byte("rotate-second"))
			err = backend.Put(ctx, second, bytesReader([]byte("two")))
			if !errors.Is(err, rotateErr) {
				t.Fatalf("Put across a failing rotation = %v, want the rotation failure", err)
			}
			if _, ok := backend.index[string(second)]; ok {
				t.Fatal("Put indexed an object whose pack record was never written")
			}
			if backend.packFile != nil {
				t.Fatal("a failed rotation left a pack file installed")
			}
		})
	}
}

// TestAppendPackRecordReportsPackHeaderWriteFailure pins the header-write branch:
// the backend is handed a read-only descriptor for the active pack file — a real
// regular file, so the seek succeeds and the write is refused by the kernel — and
// the append must report that failure, wrapped with the step that failed, without
// recording an index entry.
func TestAppendPackRecordReportsPackHeaderWriteFailure(t *testing.T) {
	// A descriptor opened O_CREATE|O_RDONLY is not write-protected on Windows,
	// so the header write this test needs to fail succeeds there. The branch is
	// platform-specific rather than untested.
	if runtime.GOOS == "windows" {
		t.Skip("a read-only descriptor does not refuse a write on Windows")
	}
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "header-write"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	backend.op.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.OpenFile(name, os.O_CREATE|os.O_RDONLY, perm)
	}
	if err := backend.packFile.Close(); err != nil {
		t.Fatal(err)
	}
	backend.packFile = nil

	d := cas.NewDigest([]byte("header-write"))
	err = backend.appendPackRecord(ctx, d, bytesReader([]byte("payload")), int64(len("payload")))
	if err == nil {
		t.Fatal("appendPackRecord into an unwritable pack file = nil, want an error")
	}
	if !strings.Contains(err.Error(), "cas: write pack header") {
		t.Fatalf("appendPackRecord = %v, want the header write named", err)
	}
	if _, ok := backend.index[string(d)]; ok {
		t.Fatal("appendPackRecord indexed an object whose header was never written")
	}
}

// TestAppendPackRecordReportsPackPayloadWriteFailure pins the payload-copy
// branch: the header is written, then the copy from the source fails, and the
// append must report that failure (wrapped with the step, errors.Is intact)
// without recording an index entry or folding the record into the byte count.
func TestAppendPackRecordReportsPackPayloadWriteFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "payload-write"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	copyErr := errors.New("payload source failed")
	d := cas.NewDigest([]byte("payload-write"))
	err = backend.appendPackRecord(ctx, d, errReader{err: copyErr}, int64(len("payload")))
	if !errors.Is(err, copyErr) {
		t.Fatalf("appendPackRecord with a failing payload = %v, want the source's error", err)
	}
	if !strings.Contains(err.Error(), "cas: write pack payload") {
		t.Fatalf("appendPackRecord = %v, want the payload write named", err)
	}
	if _, ok := backend.index[string(d)]; ok {
		t.Fatal("appendPackRecord indexed an object whose payload was never written")
	}
	if backend.packBytes != 0 || backend.packEntries != 0 {
		t.Fatalf("failed append accounted %d bytes / %d entries, want 0 / 0", backend.packBytes, backend.packEntries)
	}
}

// TestPutReportsSpoolRewindFailure pins the rewind branch of the streaming Put:
// the spool handle is not seekable (here the write end of a pipe, which accepts
// the bytes but cannot be rewound), so Put reports the rewind step by name and
// publishes nothing — the loose Put has not run yet at that point.
func TestPutReportsSpoolRewindFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "spool-rewind"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	backend.op.createTemp = func(string, string) (*os.File, error) { return writeEnd, nil }

	d := cas.NewDigest([]byte("spool-rewind"))
	err = backend.Put(ctx, d, bytesReader([]byte("payload")))
	if err == nil {
		t.Fatal("Put with an unrewindable spool file = nil, want an error")
	}
	if !strings.Contains(err.Error(), "cas: rewind spool file") {
		t.Fatalf("Put = %v, want the spool rewind named", err)
	}
	if ok, existsErr := backend.Exists(ctx, d); existsErr != nil || ok {
		t.Fatalf("Exists() after a failed spool rewind = (%v, %v), want (false, nil)", ok, existsErr)
	}
}

// TestGetReportsPersistFailureWhenPruningAStaleRecord pins Get's pruning branch:
// a record whose pack file is gone is dropped from the index, and when the pruned
// index cannot be persisted the failure surfaces instead of the object being
// served as if the index were up to date. The unwritable manifest is real
// filesystem state: its directory does not exist.
func TestGetReportsPersistFailureWhenPruningAStaleRecord(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "get-persist"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("get-persist"))
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 1}
	backend.mu.Unlock()
	backend.manifestPath = filepath.Join(backend.base, "absent-dir", "index.json")

	if _, err := backend.Get(ctx, d); err == nil {
		t.Fatal("Get with a pruned index that cannot be persisted = nil, want the persist failure")
	} else if !strings.Contains(err.Error(), "cas: write pack manifest") {
		t.Fatalf("Get = %v, want the manifest write named", err)
	}
	backend.mu.Lock()
	_, stillIndexed := backend.index[string(d)]
	backend.mu.Unlock()
	if stillIndexed {
		t.Fatal("Get left the stale record in the index")
	}
}

// TestGetManyStopsBeforeTheLoosePassOnCancellation pins the context check that
// guards the loose half of a batch: the batch holds one packed object and one
// loose-only object, the callback cancels the context while the packed group is
// served, and the loose digest must not be served afterwards — the batch reports
// the cancellation with exactly one callback for the object already in flight.
func TestGetManyStopsBeforeTheLoosePassOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-loose-cancel"), WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	digests := putMany(t, backend, 2)
	backend.mu.Lock()
	delete(backend.index, string(digests[1]))
	backend.mu.Unlock()

	served := 0
	err = backend.GetMany(ctx, digests, func(_ cas.Digest, reader io.ReadCloser) error {
		served++
		cancel()
		_, readErr := io.Copy(io.Discard, reader)
		return readErr
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMany(digest packed, loose pass still pending) = %v, want context.Canceled", err)
	}
	if served != 1 {
		t.Fatalf("GetMany served %d objects, want only the one already in flight", served)
	}
}

// closeErrorReader serves bytes and then refuses to close, so the batch's own
// close of a loose reader is what fails.
type closeErrorReader struct {
	io.Reader
	err error
}

func (r closeErrorReader) Close() error { return r.err }

// TestGetManyReportsLooseReaderCloseFailure pins callBatchFn's close handling on
// the loose half of a batch, in both directions: a close that fails after the
// callback succeeded is reported (named with the digest, errors.Is intact),
// while a callback that failed already keeps its own error — a close failure
// never masks the real cause.
func TestGetManyReportsLooseReaderCloseFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-close-loose"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("getmany-close-loose"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	delete(backend.index, string(d))
	backend.mu.Unlock()

	closeErr := errors.New("reader close failed")
	backend.op.looseGet = func(context.Context, *fsbackend.Backend, cas.Digest) (io.ReadCloser, error) {
		return closeErrorReader{Reader: bytes.NewReader([]byte("payload")), err: closeErr}, nil
	}

	t.Run("callback succeeds", func(t *testing.T) {
		err := backend.GetMany(ctx, []cas.Digest{d}, func(_ cas.Digest, reader io.ReadCloser) error {
			_, readErr := io.ReadAll(reader)
			return readErr
		})
		if !errors.Is(err, closeErr) {
			t.Fatalf("GetMany with an unclosable loose reader = %v, want the close failure", err)
		}
		if !strings.Contains(err.Error(), "cas: get many: close") {
			t.Fatalf("GetMany = %v, want the close step named", err)
		}
	})

	t.Run("callback fails", func(t *testing.T) {
		fnErr := errors.New("callback failed")
		err := backend.GetMany(ctx, []cas.Digest{d}, func(cas.Digest, io.ReadCloser) error { return fnErr })
		if !errors.Is(err, fnErr) {
			t.Fatalf("GetMany(callback failure) = %v, want the callback's error", err)
		}
		if errors.Is(err, closeErr) {
			t.Fatalf("GetMany = %v, want the callback's error, not the close failure masking it", err)
		}
	})
}

// TestPackfsReadsReportRecordValidationFailures pins the error (not stale)
// half of the record validator on every read that consults the index: a record
// with a negative offset is corrupt rather than missing, so Exists, Size and
// ModTime each report it — wrapped with the step and errors.Is intact — instead
// of answering as if the record were absent or falling back to the loose tree.
func TestPackfsReadsReportRecordValidationFailures(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "read-validate"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("read-validate"))
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: backend.packFilePath, Offset: -1, Size: 1}
	backend.mu.Unlock()

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"Exists", func() error { _, err := backend.Exists(ctx, d); return err }},
		{"Size", func() error { _, err := backend.Size(ctx, d); return err }},
		{"ModTime", func() error { _, err := backend.ModTime(ctx, d); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if !errors.Is(err, errInvalidPackRecord) {
				t.Fatalf("%s with a corrupt record = %v, want errInvalidPackRecord", tc.name, err)
			}
			if !strings.Contains(err.Error(), "cas: validate pack record") {
				t.Fatalf("%s = %v, want the validation step named", tc.name, err)
			}
		})
	}
}

// TestModTimeReportsPersistFailureWhenPruningAStaleRecord pins ModTime's pruning
// branch, which Size and Exists already cover: the stale record is dropped, and a
// pruned index that cannot be persisted is reported rather than answered from.
func TestModTimeReportsPersistFailureWhenPruningAStaleRecord(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "modtime-persist"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("modtime-persist"))
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 1}
	backend.mu.Unlock()
	backend.manifestPath = filepath.Join(backend.base, "absent-dir", "index.json")

	if _, err := backend.ModTime(ctx, d); err == nil {
		t.Fatal("ModTime with a pruned index that cannot be persisted = nil, want the persist failure")
	} else if !strings.Contains(err.Error(), "cas: write pack manifest") {
		t.Fatalf("ModTime = %v, want the manifest write named", err)
	}
	backend.mu.Lock()
	_, stillIndexed := backend.index[string(d)]
	backend.mu.Unlock()
	if stillIndexed {
		t.Fatal("ModTime left the stale record in the index")
	}
}

// TestCleanReportsALooseSweepFailure pins Clean's first failure branch: the
// loose tree is swept by the wrapped fs backend, and a loose directory the
// process may not read fails the sweep with the loose error attached instead of
// being reported as a clean store. The pack directory is not swept in that case
// — the loose tree is the durable object tree, so its failure stops the
// maintenance run. POSIX permission bits are the only portable way to produce
// that error, so the test is skipped on Windows (ACLs, not mode bits) and under
// a superuser account (permission checks do not apply).
func TestCleanReportsALooseSweepFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "clean-loose-fail"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	if err := os.Chmod(backend.BasePath(), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backend.BasePath(), 0o755) })

	removed, err := backend.Clean(ctx, 0)
	if err == nil {
		t.Fatal("Clean over an unreadable loose tree = nil, want the loose sweep failure")
	}
	if !strings.Contains(err.Error(), "cas: clean:") {
		t.Fatalf("Clean = %v, want the loose sweep error wrapped", err)
	}
	if removed != 0 {
		t.Fatalf("Clean removed %d files before failing, want 0", removed)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Clean = %v, want the permission failure preserved for errors.Is", err)
	}
}

// Branches in this package that no deterministic test reaches, with the reason
// recorded (testing-strategy.md §5):
//
//   - persistIndex's json.MarshalIndent failure: the manifest is a map with
//     string keys to a struct of two int64s and a string, and encoding/json only
//     fails on unsupported types (channels, functions, cyclic values) or
//     non-finite floats. Nothing the index can hold produces one, and a key that
//     is not valid UTF-8 is escaped, not rejected — which is exactly why the
//     manifest keys by hex (see manifest). The wrap exists so a future field type
//     cannot silently drop the write.
//
//   - appendPackRecord's second ensurePackFile failure: the first call in the
//     same invocation either leaves b.packFile non-nil or returns, and
//     rotatePackFile either returns an error or installs a non-nil handle, so
//     ensurePackFile at that point is always the "already open" no-op. Only a
//     seam that violates os.OpenFile's contract (returning a nil *os.File and a
//     nil error) could reach it, which no real filesystem does.
//
//   - Put's second spool rewind: it is the same Seek(0, io.SeekStart) on the
//     same *os.File as the first, with only the loose Put in between, and the
//     loose backend never takes ownership of the reader it is handed. A regular
//     file cannot become unseekable, so the first rewind's failure (covered
//     through a pipe-backed spool) is the only one a caller can cause; reaching
//     the second would need the loose backend to close a reader it does not own.
//
//   - ModTime's os.Stat failure and its ErrNotFound branch: validPackRecord
//     already Lstat'd the same path successfully microseconds earlier, so the
//     only way either fires is the pack file being deleted between the two calls.
//     That window is a concurrent-delete race with nothing to hook between the
//     calls — the same race TestPackfsModTimeRejectsStaleRecordAndMissingPackFile
//     documents for the stale-record path — and a test can only win it by
//     accident, so it stays uncovered rather than guarded by a timing-dependent
//     test.
