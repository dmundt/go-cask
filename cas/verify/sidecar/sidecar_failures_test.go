package sidecar_test

// This file covers the failure and maintenance branches of the package that the
// happy-path suite in sidecar_test.go does not reach. Every case is built from
// real filesystem state or from a test double for a public interface (a
// cas.Backend, a cas.Hasher, a context.Context); no production seam was added.
//
// The branches this file deliberately leaves uncovered are listed with their
// reason at the bottom of the file.

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
	"time"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/verify/adler32"
	"github.com/dmundt/go-cask/cas/verify/crc32"
	"github.com/dmundt/go-cask/cas/verify/sidecar"
)

var (
	errExistsFailed = errors.New("exists failed")
	errListFailed   = errors.New("list failed")
	errHasherFailed = errors.New("hasher failed")
)

// existsErrorBackend fails every Exists call, so a reconcile pass cannot decide
// whether a record has lost its object.
type existsErrorBackend struct{ baseReporting }

func (b *existsErrorBackend) Exists(context.Context, cas.Digest) (bool, error) {
	return false, errExistsFailed
}

// listErrorBackend fails every List call, so a pass that enumerates the store
// cannot start.
type listErrorBackend struct{ baseReporting }

func (b *listErrorBackend) List(context.Context) ([]cas.Digest, error) {
	return nil, errListFailed
}

// cancellingExistsBackend answers from the inner backend and then cancels the
// caller's context: a pass that checks one record per iteration observes
// cancellation on its next iteration, with no scheduling involved.
type cancellingExistsBackend struct {
	baseReporting
	cancel context.CancelFunc
}

func (b *cancellingExistsBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	exists, err := b.Backend.Exists(ctx, d)
	b.cancel()
	return exists, err
}

// cancellingListBackend returns the inner backend's list and then cancels the
// caller's context, so the loop that consumes the list sees cancellation on its
// first iteration.
type cancellingListBackend struct {
	baseReporting
	cancel context.CancelFunc
}

func (b *cancellingListBackend) List(ctx context.Context) ([]cas.Digest, error) {
	digests, err := b.Backend.List(ctx)
	b.cancel()
	return digests, err
}

// cancellingGetBackend reads through the inner backend and then cancels the
// caller's context, the state a Put reaches with the bytes stored and the record
// not yet published.
type cancellingGetBackend struct {
	baseReporting
	cancel context.CancelFunc
}

func (b *cancellingGetBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	rc, err := b.Backend.Get(ctx, d)
	b.cancel()
	return rc, err
}

// failDigestHasher fails every Digest call: the state of a checksum algorithm
// that cannot read the bytes it is handed. Validate accepts everything, because
// the sidecar records a checksum, it never validates a key with it.
type failDigestHasher struct{}

func (failDigestHasher) Digest(io.Reader) (cas.Digest, error) { return nil, errHasherFailed }
func (failDigestHasher) Validate(cas.Digest) error            { return nil }

// errAfterContext reports context.Canceled from the (allow+1)-th Err() call on.
// A context cancelled between a directory read and the first iteration of the
// loop over its entries cannot be produced by filesystem state — nothing runs
// between the two polls — so a Keys test uses this stub to place the
// cancellation exactly at the loop's first check. Keys must report it, not
// swallow it.
type errAfterContext struct {
	context.Context
	allow int
	calls int
}

func (c *errAfterContext) Err() error {
	c.calls++
	if c.calls > c.allow {
		return context.Canceled
	}
	return c.Context.Err()
}

// metaFileFixture returns a backend whose record directory path is a regular
// file: the store's own bytes are healthy while every record-directory
// operation fails, because a file sits where <base>/.meta is expected.
func metaFileFixture(t *testing.T) (*fsbackend.Backend, string, *sidecar.Backend) {
	t.Helper()
	backend, base := mustFS(t)
	if err := os.WriteFile(filepath.Join(base, metaDir), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	return backend, base, crc32Recorder(t, backend)
}

// recordPathDir puts a non-empty directory where d's record file is expected,
// so a record read fails after the open and a rename onto the record path
// cannot succeed.
func recordPathDir(t *testing.T, base string, d cas.Digest) {
	t.Helper()
	p := recordPath(base, d)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// objectPathDir puts a non-empty directory where the fs backend expects d's
// object file, the shape a foreign entry at the object path takes.
func objectPathDir(t *testing.T, base string, d cas.Digest) {
	t.Helper()
	p := objectPath(base, d)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordBody is a well-formed v1 record for d, so a test can place a readable
// record without going through Put.
func recordBody(d cas.Digest) string {
	return `{"version":1,"digest":"` + d.String() + `","checksum_algo":"crc32","checksum":"00112233","size":1,"created_at":"2026-01-01T00:00:00Z"}`
}

func TestNewSkipsNilOptions(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)

	rec, err := sidecar.New(backend, nil, sidecar.WithChecksum(crc32.Name, crc32.New()), nil)
	if err != nil {
		t.Fatalf("New with nil options: %v", err)
	}
	if rec.BasePath() != base {
		t.Errorf("BasePath = %q, want %q", rec.BasePath(), base)
	}
	// A nil option is skipped, not treated as "no options": the checksum option
	// between the two nils still takes effect.
	d := put(t, rec, []byte("nil options are skipped"))
	if _, err := rec.Load(ctx, d); err != nil {
		t.Errorf("Load after Put through a decorator built with nil options: %v", err)
	}
}

func TestNewResolvesBaseForABackendWithoutABasePath(t *testing.T) {
	ctx := context.Background()
	// A trailing separator exercises the path cleaning on the WithBase branch
	// taken for a backend that reports no base of its own: backmem keeps no
	// bytes on disk, so the caller names the directory the records live in.
	base := filepath.Join(t.TempDir(), "objects") + string(filepath.Separator)
	rec, err := sidecar.New(backmem.New(),
		sidecar.WithBase(base),
		sidecar.WithChecksum(crc32.Name, crc32.New()))
	if err != nil {
		t.Fatalf("New over backmem with WithBase: %v", err)
	}
	want := filepath.Clean(base)
	if rec.BasePath() != want {
		t.Fatalf("BasePath = %q, want %q", rec.BasePath(), want)
	}

	data := []byte("recorded beside a memory backend")
	d := put(t, rec, data)
	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !record.Checksum.Equal(crc32.Of(data)) {
		t.Errorf("Checksum = %s, want %s", record.Checksum, crc32.Of(data))
	}
	if _, err := os.Stat(recordPath(want, d)); err != nil {
		t.Errorf("record file under the WithBase directory: %v", err)
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestPutRewritesAForeignAlgorithmRecordKeepingCreationTime(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	crc := crc32Recorder(t, backend)
	data := []byte("recorded by crc32, rewritten by adler32")
	d := put(t, crc, data)
	first, err := crc.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// A record that names another algorithm is derived metadata the new writer
	// cannot use, so it is rewritten — and the creation time it carries is
	// preserved, because the object itself did not change.
	time.Sleep(2 * time.Millisecond)
	adler := mustSidecar(t, backend, sidecar.WithChecksum(adler32.Name, adler32.New()))
	if err := adler.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put under another algorithm: %v", err)
	}
	rewritten, err := adler.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load after the rewrite: %v", err)
	}
	if rewritten.ChecksumAlgo != adler32.Name {
		t.Errorf("ChecksumAlgo = %q, want %q", rewritten.ChecksumAlgo, adler32.Name)
	}
	if !rewritten.Checksum.Equal(adler32.Of(data)) {
		t.Errorf("Checksum = %s, want %s", rewritten.Checksum, adler32.Of(data))
	}
	if !rewritten.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %s, want the original %s", rewritten.CreatedAt, first.CreatedAt)
	}
	if err := adler.Verifier(adler32.Name, adler32.New()).Verify(ctx, d); err != nil {
		t.Errorf("Verify under the rewriting algorithm: %v", err)
	}
	if err := adler.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); !errors.Is(err, sidecar.ErrChecksumAlgorithm) {
		t.Errorf("Verify under the old algorithm = %v, want ErrChecksumAlgorithm", err)
	}
}

func TestDeleteAndLoadRejectAnAbsentDigest(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)

	if err := rec.Delete(ctx, cas.Digest{}); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Errorf("Delete with an absent digest = %v, want cas.ErrInvalidDigest", err)
	}
	if _, err := rec.Load(ctx, cas.Digest{}); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Errorf("Load with an absent digest = %v, want cas.ErrInvalidDigest", err)
	}
}

func TestKeysOnAStoreWithNoRecordsReturnsNoKeys(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	readOnly := mustSidecar(t, backend)
	if _, err := os.Stat(filepath.Join(base, metaDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the record directory should not exist yet: %v", err)
	}

	keys, err := readOnly.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys without a record directory: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Keys = %v, want none", keys)
	}
	report, err := readOnly.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile without a record directory: %v", err)
	}
	if report.Records != 0 || len(report.Removed) != 0 || len(report.Unrecorded) != 0 {
		t.Errorf("Reconcile = %+v, want an empty report", report)
	}
}

func TestKeysReportsAnUnreadableRecordDirectory(t *testing.T) {
	// A file used as a directory reports ENOTDIR on POSIX, which Keys must
	// report, but ERROR_PATH_NOT_FOUND on Windows — which Go maps to
	// fs.ErrNotExist, so Keys correctly answers "no records yet" there. The
	// branch is unreachable on that platform, not untested.
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a file-as-directory as fs.ErrNotExist, so the listing does not fail")
	}
	ctx := context.Background()
	_, _, rec := metaFileFixture(t)

	keys, err := rec.Keys(ctx)
	if err == nil {
		t.Fatal("Keys succeeded although the record directory path is a file")
	}
	if !strings.Contains(err.Error(), "list records") {
		t.Errorf("Keys error = %v, want it to name the failed listing", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a present but unusable record directory was reported as absent: %v", err)
	}
	if keys != nil {
		t.Errorf("Keys = %v, want no partial result", keys)
	}
}

func TestLoadReportsAnUnopenableRecordPath(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)

	// A self-referential symlink where the record belongs: the record path
	// exists (so this is not "no record"), and opening it fails — which is
	// damage, because a record is either usable or cas.ErrCorrupt.
	d := sha256.Of([]byte("record path cannot be opened"))
	p := recordPath(base, d)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, p); err != nil {
		t.Skipf("this platform cannot create a symlink loop: %v", err)
	}

	_, err := rec.Load(ctx, d)
	if err == nil || !strings.Contains(err.Error(), "open record") {
		t.Errorf("Load with an unopenable record path = %v, want the open failure", err)
	}
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Errorf("Load with an unopenable record = %v, want cas.ErrCorrupt: Load documents an unreadable record as damage", err)
	}
	if errors.Is(err, cas.ErrNotFound) {
		t.Errorf("an unopenable record path was reported as absent: %v", err)
	}
}

func TestPutReportsAnUncreatableRecordDirectory(t *testing.T) {
	ctx := context.Background()
	backend, _, rec := metaFileFixture(t)
	data := []byte("the record directory path is a file")
	d := sha256.Of(data)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Put succeeded although the record directory cannot be created")
	}
	if !strings.Contains(err.Error(), "create record directory") {
		t.Errorf("Put error = %v, want it to name the failed directory creation", err)
	}
	// The object is the store's data and the record is derived metadata: an
	// unusable record path does not block the object's own write.
	exists, err := backend.Exists(ctx, d)
	if err != nil || !exists {
		t.Errorf("Exists after the failed record write = %v, %v; want true, nil", exists, err)
	}
	if _, err := rec.Load(ctx, d); err == nil {
		t.Error("Load found a record although none could be published")
	}
}

func TestReconcileReportsAnUnreadableRecordDirectory(t *testing.T) {
	// Same platform split as TestKeysReportsAnUnreadableRecordDirectory:
	// Reconcile lists through Keys, and Windows maps a file-as-directory to
	// fs.ErrNotExist, so it sees an empty record set rather than a failure.
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a file-as-directory as fs.ErrNotExist, so the listing does not fail")
	}
	ctx := context.Background()
	_, _, rec := metaFileFixture(t)

	report, err := rec.Reconcile(ctx)
	if err == nil {
		t.Fatal("Reconcile succeeded although the record directory cannot be listed")
	}
	if report != nil {
		t.Errorf("Reconcile = %+v, want a nil report when no record could be read", report)
	}
	if !strings.Contains(err.Error(), "list records") {
		t.Errorf("Reconcile error = %v, want it to name the failed listing", err)
	}
}

func TestLoadReportsAnUnreadableRecordFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to open a directory at all, which is the other open-record branch")
	}
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := sha256.Of([]byte("record file cannot be read"))
	recordPathDir(t, base, d)

	// The open succeeds (a directory can be opened on a POSIX filesystem) and
	// the read fails, so the record is present and damaged rather than absent.
	// The read-error arm and the open-error arm both report cas.ErrCorrupt, the
	// same sentinel as the oversized and wrong-digest arms, because Load and
	// Verify document a record that exists and cannot be read as damage.
	_, err := rec.Load(ctx, d)
	if err == nil || !strings.Contains(err.Error(), "read record") {
		t.Errorf("Load with an unreadable record file = %v, want the read failure", err)
	}
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Errorf("Load with an unreadable record = %v, want cas.ErrCorrupt: Load documents an unreadable record as damage", err)
	}
	if errors.Is(err, cas.ErrNotFound) {
		t.Errorf("an unreadable record was reported as absent: %v", err)
	}
}

func TestPutOverADirectoryRecordFailsToPublish(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	data := []byte("the record path is a directory")
	d := sha256.Of(data)
	recordPathDir(t, base, d)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Put succeeded although the record cannot be renamed into place")
	}
	if !strings.Contains(err.Error(), "publish record") {
		t.Errorf("Put error = %v, want it to name the failed publish", err)
	}
	// The object landed and stays readable: the record is what failed.
	rc, err := rec.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get after the failed record write: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stored object: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("stored object = %q, want %q", got, data)
	}
}

func TestDeleteReportsARecordRemovalFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	data := []byte("the record cannot be removed")
	d := sha256.Of(data)
	if err := backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("backend.Put: %v", err)
	}
	recordPathDir(t, base, d)

	err := rec.Delete(ctx, d)
	if err == nil {
		t.Fatal("Delete succeeded although the record cannot be removed")
	}
	if !strings.Contains(err.Error(), "delete record") {
		t.Errorf("Delete error = %v, want it to name the failed record removal", err)
	}
	// The object is deleted first, so a failed record removal leaves the object
	// gone and the record still present.
	exists, err := backend.Exists(ctx, d)
	if err != nil || exists {
		t.Errorf("Exists after Delete = %v, %v; want false, nil", exists, err)
	}
	if _, err := os.Stat(recordPath(base, d)); err != nil {
		t.Errorf("the record path changed although its removal failed: %v", err)
	}
}

func TestPutReportsAnInnerPutFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	data := []byte("the object path is a directory")
	d := sha256.Of(data)
	objectPathDir(t, base, d)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Put succeeded although the backend refused the object")
	}
	if !strings.Contains(err.Error(), "publish object") {
		t.Errorf("Put error = %v, want the backend's own failure", err)
	}
	// A write the backend refused records nothing: a checksum for bytes nobody
	// stored would be worse than none.
	if _, err := os.Stat(recordPath(base, d)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a record was written although the backend refused the object: %v", err)
	}
}

func TestDeleteReportsAnInnerDeleteFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	data := []byte("the object cannot be deleted")
	d := sha256.Of(data)
	objectPathDir(t, base, d)
	writeRecord(t, base, d, recordBody(d))

	err := rec.Delete(ctx, d)
	if err == nil {
		t.Fatal("Delete succeeded although the backend could not remove the object")
	}
	if !strings.Contains(err.Error(), "delete object") {
		t.Errorf("Delete error = %v, want the backend's own failure", err)
	}
	// The record is removed only after the object was: a failed object delete
	// leaves the record in place.
	if _, err := rec.Load(ctx, d); err != nil {
		t.Errorf("Delete dropped the record although the object delete failed: %v", err)
	}
}

func TestReconcileReportsAnExistsFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("exists cannot be answered"))
	broken := mustSidecar(t, &existsErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})

	report, err := broken.Reconcile(ctx)
	if !errors.Is(err, errExistsFailed) {
		t.Fatalf("Reconcile with a failing Exists = %v, want %v", err, errExistsFailed)
	}
	if report == nil {
		t.Fatal("Reconcile returned no report with the error")
	}
	if report.Records != 1 {
		t.Errorf("Records = %d, want 1", report.Records)
	}
	if len(report.Removed) != 0 {
		t.Errorf("Removed = %v, want none: the pass could not decide", report.Removed)
	}
}

func TestReconcileReportsAListFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("list fails after the records pass"))
	broken := mustSidecar(t, &listErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})

	report, err := broken.Reconcile(ctx)
	if !errors.Is(err, errListFailed) {
		t.Fatalf("Reconcile with a failing List = %v, want %v", err, errListFailed)
	}
	if report == nil {
		t.Fatal("Reconcile returned no report with the error")
	}
	if report.Records != 1 {
		t.Errorf("Records = %d, want 1", report.Records)
	}
	if len(report.Unrecorded) != 0 {
		t.Errorf("Unrecorded = %v, want none: the sweep never ran", report.Unrecorded)
	}
}

func TestReconcileStopsWhenTheContextIsCancelledBetweenRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("first record"))
	put(t, rec, []byte("second record"))
	stopping := mustSidecar(t, &cancellingExistsBackend{
		baseReporting: baseReporting{Backend: backend, base: base},
		cancel:        cancel,
	})

	report, err := stopping.Reconcile(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile with a context cancelled mid-pass = %v, want context.Canceled", err)
	}
	if report == nil {
		t.Fatal("Reconcile returned no report with the error")
	}
	if report.Records != 2 {
		t.Errorf("Records = %d, want 2: the count is known before the sweep", report.Records)
	}
	if len(report.Removed) != 0 {
		t.Errorf("a cancelled pass removed %v, want none", report.Removed)
	}
}

func TestReconcileStopsWhenTheContextIsCancelledBeforeTheSweep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("recorded before the sweep"))
	stopping := mustSidecar(t, &cancellingListBackend{
		baseReporting: baseReporting{Backend: backend, base: base},
		cancel:        cancel,
	})

	report, err := stopping.Reconcile(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile with a context cancelled before the sweep = %v, want context.Canceled", err)
	}
	if report == nil {
		t.Fatal("Reconcile returned no report with the error")
	}
	if report.Records != 1 {
		t.Errorf("Records = %d, want 1", report.Records)
	}
	if len(report.Unrecorded) != 0 {
		t.Errorf("a cancelled sweep reported unrecorded objects: %v", report.Unrecorded)
	}
}

func TestKeysStopsWhenTheContextIsCancelledMidListing(t *testing.T) {
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("recorded, then the context is cancelled"))

	// The first Err() poll is Keys' entry check, the second is the loop's: the
	// stub answers nil and then Canceled, which is the mid-listing cancellation
	// the loop must report instead of swallowing.
	ctx := &errAfterContext{Context: context.Background(), allow: 1}
	keys, err := rec.Keys(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Keys with a context cancelled mid-listing = %v, want context.Canceled", err)
	}
	if keys != nil {
		t.Errorf("Keys = %v, want no partial result", keys)
	}
}

func TestPutWritesNoRecordWhenTheContextIsCancelledBeforePublishing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, base := mustFS(t)
	rec := mustSidecar(t, &cancellingGetBackend{
		baseReporting: baseReporting{Backend: backend, base: base},
		cancel:        cancel,
	}, sidecar.WithChecksum(crc32.Name, crc32.New()))
	data := []byte("cancelled before the record is published")
	d := sha256.Of(data)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put with a context cancelled before publishing = %v, want context.Canceled", err)
	}
	exists, err := backend.Exists(context.Background(), d)
	if err != nil || !exists {
		t.Errorf("Exists after the cancelled Put = %v, %v; want true, nil", exists, err)
	}
	if _, err := os.Stat(recordPath(base, d)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a record was published after cancellation: %v", err)
	}
}

func TestPutReportsAChecksumFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := mustSidecar(t, backend, sidecar.WithChecksum(crc32.Name, failDigestHasher{}))
	data := []byte("the checksum cannot be computed")
	d := sha256.Of(data)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if !errors.Is(err, errHasherFailed) {
		t.Fatalf("Put with a failing hasher = %v, want %v", err, errHasherFailed)
	}
	// The object reached the backend, but a checksum nobody could compute is not
	// recorded: the digest is left unchecked rather than described by a guess.
	exists, err := backend.Exists(ctx, d)
	if err != nil || !exists {
		t.Errorf("Exists after the failed checksum = %v, %v; want true, nil", exists, err)
	}
	if _, err := os.Stat(recordPath(base, d)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a record was written without a checksum: %v", err)
	}
}

func TestPutReportsAnUnwritableRecordDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	meta := filepath.Join(base, metaDir)
	if err := os.MkdirAll(meta, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(meta, 0o755) })
	data := []byte("the record directory cannot hold a temp file")
	d := sha256.Of(data)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Put succeeded although no temp file can be created in the record directory")
	}
	if !strings.Contains(err.Error(), "create record temp") {
		t.Errorf("Put error = %v, want it to name the failed temp file", err)
	}
	// The object is still stored: only the record write failed.
	exists, err := backend.Exists(ctx, d)
	if err != nil || !exists {
		t.Errorf("Exists after the failed record write = %v, %v; want true, nil", exists, err)
	}
}

func TestReconcileReportsAnUnremovableOrphanRecord(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("orphan whose record directory is not writable"))
	if err := backend.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(base, metaDir)
	if err := os.Chmod(meta, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(meta, 0o755) })

	report, err := rec.Reconcile(ctx)
	if err == nil {
		t.Fatal("Reconcile succeeded although the orphan record cannot be removed")
	}
	if !strings.Contains(err.Error(), "remove record") {
		t.Errorf("Reconcile error = %v, want it to name the failed removal", err)
	}
	if report == nil || report.Records != 1 {
		t.Fatalf("Reconcile = %+v, want a report naming the one record examined", report)
	}
	if len(report.Removed) != 0 {
		t.Errorf("a record that could not be removed was reported as removed: %v", report.Removed)
	}
	if _, err := os.Stat(recordPath(base, d)); err != nil {
		t.Errorf("the orphan record is gone although its removal failed: %v", err)
	}
}

func TestDirSyncReportsAnUnreadableRecordDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := mustSidecar(t, backend,
		sidecar.WithChecksum(crc32.Name, crc32.New()),
		sidecar.WithDirSync())
	// Write and execute but not read: the record can be created and renamed into
	// place, while the directory itself cannot be opened for the fsync.
	meta := filepath.Join(base, metaDir)
	if err := os.MkdirAll(meta, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(meta, 0o755) })
	data := []byte("published, but the directory cannot be opened again")
	d := sha256.Of(data)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Put succeeded although the record directory cannot be opened for fsync")
	}
	if !strings.Contains(err.Error(), "open record directory") {
		t.Errorf("Put error = %v, want it to name the failed directory open", err)
	}
	// The rename happened before the fsync, so the record is on disk.
	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load after the failed directory sync: %v", err)
	}
	if record.ChecksumAlgo != crc32.Name {
		t.Errorf("ChecksumAlgo = %q, want %q", record.ChecksumAlgo, crc32.Name)
	}
}

func TestNilVerifierReportsNoBackend(t *testing.T) {
	var v *sidecar.Verifier
	d := sha256.Of([]byte("no backend behind the verifier"))

	if err := v.Verify(context.Background(), d); err == nil || !strings.Contains(err.Error(), "nil backend") {
		t.Errorf("Verify on a nil Verifier = %v, want a nil-backend error", err)
	}
	report, err := v.VerifyAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nil backend") {
		t.Errorf("VerifyAll on a nil Verifier = %v, want a nil-backend error", err)
	}
	if report != nil {
		t.Errorf("VerifyAll = %+v, want a nil report", report)
	}
}

func TestVerifyAllReportsAMissingAlgorithm(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("the pass names no algorithm"))

	report, err := rec.Verifier("", crc32.New()).VerifyAll(ctx)
	if err == nil || !strings.Contains(err.Error(), "no checksum algorithm") {
		t.Errorf("VerifyAll without an algorithm = %v, want a missing-algorithm error", err)
	}
	if report != nil {
		t.Errorf("VerifyAll = %+v, want a nil report", report)
	}
}

func TestVerifyAllReportsAListFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("the store cannot be enumerated"))
	broken := mustSidecar(t, &listErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})

	report, err := broken.Verifier(crc32.Name, crc32.New()).VerifyAll(ctx)
	if !errors.Is(err, errListFailed) {
		t.Fatalf("VerifyAll with a failing List = %v, want %v", err, errListFailed)
	}
	if report != nil {
		t.Errorf("VerifyAll = %+v, want a nil report when nothing was checked", report)
	}
}

func TestVerifyAllStopsWhenTheContextIsCancelledMidPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	put(t, rec, []byte("the pass is cancelled after the listing"))
	stopping := mustSidecar(t, &cancellingListBackend{
		baseReporting: baseReporting{Backend: backend, base: base},
		cancel:        cancel,
	})

	report, err := stopping.Verifier(crc32.Name, crc32.New()).VerifyAll(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyAll with a context cancelled mid-pass = %v, want context.Canceled", err)
	}
	if report == nil {
		t.Fatal("VerifyAll returned no report with the error")
	}
	if report.Checked != 0 || len(report.Bad) != 0 || len(report.Unrecorded) != 0 {
		t.Errorf("VerifyAll = %+v, want an empty report", report)
	}
}

func TestVerifyAllReportsAWrongAlgorithmInsteadOfBadRecords(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("recorded with crc32, read as adler32"))

	report, err := rec.Verifier(adler32.Name, adler32.New()).VerifyAll(ctx)
	if !errors.Is(err, sidecar.ErrChecksumAlgorithm) {
		t.Fatalf("VerifyAll under another algorithm = %v, want ErrChecksumAlgorithm", err)
	}
	if errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("a wrong-algorithm pass was reported as corruption: %v", err)
	}
	if report == nil {
		t.Fatal("VerifyAll returned no report with the error")
	}
	if report.Checked != 1 {
		t.Errorf("Checked = %d, want 1", report.Checked)
	}
	if len(report.Bad) != 0 {
		t.Errorf("Bad = %v, want none: a reader change is not damage", report.Bad)
	}
	if !strings.Contains(err.Error(), d.String()) {
		t.Errorf("VerifyAll error = %v, want it to name %s", err, d)
	}
}

func TestVerifyReportsAnInnerGetFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("the record is readable, the object is not"))
	broken := mustSidecar(t, &getErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})

	err := broken.Verifier(crc32.Name, crc32.New()).Verify(ctx, d)
	if !errors.Is(err, errGetFailed) {
		t.Fatalf("Verify with a failing Get = %v, want %v", err, errGetFailed)
	}
	if errors.Is(err, cas.ErrCorrupt) {
		t.Errorf("an unreadable object was reported as corruption: %v", err)
	}
}

func TestVerifyReportsAChecksumFailure(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("the checksum cannot be recomputed"))

	err := rec.Verifier(crc32.Name, failDigestHasher{}).Verify(ctx, d)
	if !errors.Is(err, errHasherFailed) {
		t.Fatalf("Verify with a failing hasher = %v, want %v", err, errHasherFailed)
	}
	if errors.Is(err, cas.ErrCorrupt) {
		t.Errorf("a checksum the reader could not compute was reported as corruption: %v", err)
	}
}

func TestVerifyReportsACloseFailure(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("close fails after the bytes were read"))
	broken := mustSidecar(t, &closeErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})

	err := broken.Verifier(crc32.Name, crc32.New()).Verify(ctx, d)
	if !errors.Is(err, errCloseFailed) {
		t.Fatalf("Verify with a failing Close = %v, want %v", err, errCloseFailed)
	}
}

func TestRecordKeepsTypeWhenTheEnvelopeExceedsTheCapturedPrefix(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	store := cas.New(rec, jsoncodec.New[*note](), sha256.New())

	// The record captures a bounded prefix of the stored bytes, so a whole
	// envelope is only available for an object that fits it. The header still
	// yields the type; the codec tag needs the payload length field, which a
	// truncated prefix does not carry.
	d, err := store.Put(ctx, &note{Text: strings.Repeat("x", 8192)})
	if err != nil {
		t.Fatalf("Store.Put: %v", err)
	}
	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if record.Type != "note@1" {
		t.Errorf("Type = %q, want %q from the header alone", record.Type, "note@1")
	}
	if record.Codec != "" {
		t.Errorf("Codec = %q, want empty when the envelope exceeds the captured prefix", record.Codec)
	}
	if record.Size <= 4096 {
		t.Fatalf("record Size = %d; the prefix cap was not exercised", record.Size)
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// Deliberately uncovered branches (testing-strategy §5: a branch no test can
// reach deterministically is left uncovered and its reason written down here).
//
//   - backend.go, writeRecord's "return err" after rec.encode(): Record holds a
//     string, an int, a cas.Digest ([]byte), an int64 and a time.Time built from
//     time.Now() by Put itself, and encoding/json has no failure mode for any of
//     those. No caller can supply a Record that makes Marshal fail.
//   - record.go, encode()'s json.Marshal error: the same unreachable Marshal on
//     the same type; Put builds the Record, so no seam exposes it.
//   - backend.go, writeRecord's f.Chmod arm: fchmod on a file this process just
//     created succeeds for its owner, so only a failing or read-only mount — not
//     a filesystem fixture — can produce it.
//   - backend.go, writeRecord's f.Write arm: a fresh temp file inside a writable
//     directory accepts the record's few hundred bytes; only a full or failing
//     volume (ENOSPC/EIO) fails it, which no deterministic fixture produces.
//   - backend.go, writeRecord's f.Sync arm: an fsync of a fresh file on a
//     writable filesystem does not fail deterministically.
//   - backend.go, writeRecord's f.Close arm: closing a file this process opened
//     for writing has no filesystem-driven failure.
//   - backend.go, syncDir's "return nil" Windows arm: the directory-fsync guard
//     is selected by runtime.GOOS, and the coverage gate measures Linux only
//     (testing-strategy §5), so the arm cannot be executed in the gated run.
//   - backend.go, syncDir's dir.Sync arm: the open-record-directory arm above it
//     is covered through POSIX permissions, but a directory fsync on the
//     filesystems the gate runs on succeeds; making it fail needs an exotic or
//     failing mount, which is not a deterministic fixture.
