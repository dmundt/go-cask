package packfs

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
)

// TestPackfsSizeReportsPackPayloadAndLooseFallback pins the cas.Sizer contract
// on both storage shapes: a packed object reports the payload length recorded in
// its pack record (not the 4+len(digest)+8 header, and not the pack file's
// size), while an object that lives only in the loose backend reports the loose
// file size.
func TestPackfsSizeReportsPackPayloadAndLooseFallback(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "size"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	payload := []byte("packed payload")
	packed := cas.NewDigest([]byte("size-packed"))
	if err := backend.Put(ctx, packed, bytesReader(payload)); err != nil {
		t.Fatal(err)
	}

	// The same object stored while packing is disabled stays loose-only.
	loosePayload := []byte("loose payload")
	loose := cas.NewDigest([]byte("size-loose"))
	if err := backend.Put(ctx, loose, bytesReader(loosePayload)); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	delete(backend.index, string(loose))
	backend.mu.Unlock()

	got, err := backend.Size(ctx, packed)
	if err != nil {
		t.Fatalf("Size(packed) = %v, want nil", err)
	}
	if got != int64(len(payload)) {
		t.Fatalf("Size(packed) = %d, want the %d payload bytes", got, len(payload))
	}
	got, err = backend.Size(ctx, loose)
	if err != nil {
		t.Fatalf("Size(loose) = %v, want nil", err)
	}
	if got != int64(len(loosePayload)) {
		t.Fatalf("Size(loose) = %d, want the %d loose bytes", got, len(loosePayload))
	}

	if _, err := backend.Size(ctx, nil); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Size(nil) = %v, want ErrInvalidDigest", err)
	}
	if _, err := backend.Size(ctx, cas.NewDigest([]byte("never stored"))); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Size(absent) = %v, want ErrNotFound", err)
	}
}

// TestPackfsSizeRejectsStaleRecordAndPersistFailure pins Size's recovery path: a
// record whose pack file is gone is dropped from the index and the object falls
// back to the loose backend; when the pruned index cannot be persisted the
// failure surfaces instead of being swallowed.
func TestPackfsSizeRejectsStaleRecordAndPersistFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "size-stale"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("size-stale"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 7}
	backend.mu.Unlock()

	if got, err := backend.Size(ctx, d); err != nil || got != int64(len("payload")) {
		t.Fatalf("Size(stale record) = (%d, %v), want the loose size and nil", got, err)
	}

	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 7}
	backend.mu.Unlock()
	persistErr := errors.New("persist failed")
	backend.op.writeFile = func(string, []byte, os.FileMode) error { return persistErr }
	if _, err := backend.Size(ctx, d); !errors.Is(err, persistErr) {
		t.Fatalf("Size(prune not persisted) = %v, want the persist failure", err)
	}
}

// TestPackfsModTimeReportsPackFileTimeAndLooseFallback pins cas.ModTimer on both
// shapes: a packed object reports its pack file's modification time, and a loose
// object reports its own file's time.
func TestPackfsModTimeReportsPackFileTimeAndLooseFallback(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "modtime"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	packed := cas.NewDigest([]byte("modtime-packed"))
	if err := backend.Put(ctx, packed, bytesReader([]byte("packed payload"))); err != nil {
		t.Fatal(err)
	}
	loose := cas.NewDigest([]byte("modtime-loose"))
	if err := backend.Put(ctx, loose, bytesReader([]byte("loose payload"))); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.index[string(packed)] = packRecord{Pack: backend.packFilePath, Offset: 0, Size: 5}
	delete(backend.index, string(loose))
	backend.mu.Unlock()

	packInfo, err := os.Stat(backend.packFilePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := backend.ModTime(ctx, packed)
	if err != nil {
		t.Fatalf("ModTime(packed) = %v, want nil", err)
	}
	if !got.Equal(packInfo.ModTime()) {
		t.Fatalf("ModTime(packed) = %v, want the pack file's %v", got, packInfo.ModTime())
	}

	looseTime, err := backend.ModTime(ctx, loose)
	if err != nil {
		t.Fatalf("ModTime(loose) = %v, want nil", err)
	}
	if looseTime.IsZero() {
		t.Fatal("ModTime(loose) = zero time, want the loose file's timestamp")
	}

	if _, err := backend.ModTime(ctx, nil); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("ModTime(nil) = %v, want ErrInvalidDigest", err)
	}
	if _, err := backend.ModTime(ctx, cas.NewDigest([]byte("never stored"))); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ModTime(absent) = %v, want ErrNotFound", err)
	}
}

// TestPackfsModTimeRejectsStaleRecordAndMissingPackFile pins ModTime's recovery
// path: a record whose pack file is gone is pruned and the timestamp comes from
// the loose object, exactly as Get serves it. The pack file disappearing between
// the record's validation and the stat that follows it is a filesystem race this
// backend cannot provoke deterministically, so that defensive ErrNotFound branch
// stays uncovered rather than guarded by a test that fakes the race.
func TestPackfsModTimeRejectsStaleRecordAndMissingPackFile(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "modtime-stale"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	packed := cas.NewDigest([]byte("modtime-stale"))
	loose := cas.NewDigest([]byte("modtime-stale-loose"))
	for _, d := range []cas.Digest{packed, loose} {
		if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
			t.Fatal(err)
		}
	}
	looseTime, err := backend.loose.ModTime(ctx, loose)
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.index[string(packed)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 7}
	backend.mu.Unlock()

	got, err := backend.ModTime(ctx, packed)
	if err != nil {
		t.Fatalf("ModTime(stale record) = %v, want the loose timestamp", err)
	}
	if got.IsZero() {
		t.Fatal("ModTime(stale record) = zero time, want the loose object's timestamp")
	}
	if got.Before(looseTime.Add(-time.Minute)) {
		t.Fatalf("ModTime(stale record) = %v, want the loose object's %v", got, looseTime)
	}
	backend.mu.Lock()
	_, stillIndexed := backend.index[string(packed)]
	backend.mu.Unlock()
	if stillIndexed {
		t.Fatal("ModTime left the stale record in the index")
	}
}

// TestPackfsCleanRemovesOnlyStalePackScratchFiles pins the maintenance contract
// of Clean over the pack directory: a .tmp file older than the threshold is
// removed and counted, a fresh one is kept, a non-scratch file is untouched, and
// Clean with a zero threshold removes every scratch file regardless of age.
func TestPackfsCleanRemovesOnlyStalePackScratchFiles(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "clean")
	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	if err := backend.Put(ctx, cas.NewDigest([]byte("clean")), bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(backend.packDir, ".old-put-1.tmp")
	fresh := filepath.Join(backend.packDir, ".new-put-2.tmp")
	keep := filepath.Join(backend.packDir, "notes.txt")
	for _, path := range []string{stale, fresh, keep} {
		if err := os.WriteFile(path, []byte("scratch"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, path := range []string{stale, keep} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := backend.Clean(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Clean(1h) = %v, want nil", err)
	}
	if removed == 0 {
		t.Fatal("Clean(1h) removed nothing, want the stale scratch file counted")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("Clean left the stale scratch file %s", stale)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("Clean removed the fresh scratch file %s: %v", fresh, err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("Clean removed the non-scratch file %s: %v", keep, err)
	}

	// A zero threshold means "no age limit", so the fresh scratch file goes too.
	if _, err := backend.Clean(ctx, 0); err != nil {
		t.Fatalf("Clean(0) = %v, want nil", err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("Clean(0) left the scratch file %s", fresh)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("Clean(0) removed the non-scratch file %s: %v", keep, err)
	}
}

// TestPackfsCleanToleratesMissingPackDirAndCancellation pins Clean's two guarded
// paths: a pack directory that does not exist is not an error, and a cancelled
// context stops the walk with context.Canceled.
func TestPackfsCleanToleratesMissingPackDirAndCancellation(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "clean-edges"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	backend.packDir = filepath.Join(backend.base, "deleted-packs")
	if removed, err := backend.Clean(ctx, time.Hour); err != nil || removed != 0 {
		t.Fatalf("Clean(missing pack dir) = (%d, %v), want (0, nil)", removed, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Clean(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Clean(cancelled) = %v, want context.Canceled", err)
	}
}

// TestPackfsCleanFollowsTheLooseTempConvention pins the scratch-name scope Clean
// shares with the loose backend through fs.CleanTemp: the names packfs itself
// writes — the ".put-*.tmp" spool files and the "index.json.tmp" rename scratch —
// are reclaimed, a near-miss such as "name.tmp.extra" is not treated as scratch
// at all, and a plain foreign file is left alone. Sharing one predicate with fs
// is the point: the two trees under one base cannot disagree about what a
// leftover is.
func TestPackfsCleanFollowsTheLooseTempConvention(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "convention"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	cases := []struct {
		name string
		gone bool
	}{
		{".put-123.tmp", true},
		{"index.json.tmp", true},
		{"index.json.tmp.1", true},
		{"name.tmp.extra", false},
		{"notes.txt", false},
		{"tmpfile", false},
	}
	for _, tc := range cases {
		if err := os.WriteFile(filepath.Join(backend.packDir, tc.name), []byte("scratch"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := backend.Clean(ctx, 0)
	if err != nil {
		t.Fatalf("Clean(0) = %v, want nil", err)
	}
	if removed != 3 {
		t.Fatalf("Clean(0) removed %d entries, want 3 (the convention names only)", removed)
	}
	for _, tc := range cases {
		_, err := os.Stat(filepath.Join(backend.packDir, tc.name))
		if tc.gone && !os.IsNotExist(err) {
			t.Errorf("Clean left the scratch file %s: %v", tc.name, err)
		}
		if !tc.gone && err != nil {
			t.Errorf("Clean removed %s, which is not a scratch name: %v", tc.name, err)
		}
	}
}

// TestOpsFallBackToTheRealFilesystem pins the documented zero-value ops
// contract: an unset seam performs the real operation instead of panicking, so a
// Backend built directly by a test still behaves correctly.
func TestOpsFallBackToTheRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	var op ops

	if err := op.mkdirAllDo(filepath.Join(dir, "nested", "deep"), 0o755); err != nil {
		t.Fatalf("mkdirAllDo(zero ops) = %v, want the real MkdirAll", err)
	}
	file, err := op.createTempDo(dir, ".fallback-*.tmp")
	if err != nil {
		t.Fatalf("createTempDo(zero ops) = %v, want the real CreateTemp", err)
	}
	if err := op.writeFileDo(file.Name(), []byte("data"), 0o644); err != nil {
		t.Fatalf("writeFileDo(zero ops) = %v, want the real WriteFile", err)
	}
	if data, err := op.readFileDo(file.Name()); err != nil || string(data) != "data" {
		t.Fatalf("readFileDo(zero ops) = (%q, %v), want real contents", data, err)
	}
	if err := op.writeFileDo(file.Name(), []byte("rewritten"), 0o644); err != nil {
		t.Fatalf("writeFileDo(zero ops, existing file) = %v, want the real WriteFile", err)
	}
	// Release the handle before renaming: Windows refuses to rename a file that
	// is still open, which says nothing about the real ops fallback.
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	renamed := file.Name() + ".renamed"
	if err := op.renameDo(file.Name(), renamed); err != nil {
		t.Fatalf("renameDo(zero ops) = %v, want the real Rename", err)
	}
	opened, err := op.openDo(renamed)
	if err != nil {
		t.Fatalf("openDo(zero ops) = %v, want the real Open", err)
	}
	_ = opened.Close()
	reopened, err := op.openFileDo(renamed, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("openFileDo(zero ops) = %v, want the real OpenFile", err)
	}
	_ = reopened.Close()

	// The real operations report errors honestly when a path is unusable; which
	// paths a platform rejects differs, so this only pins the successful
	// fallbacks above rather than one OS's answer for an empty path.
}

// TestOpsLooseSeamsFallBackToTheLooseBackend pins the same zero-value contract
// for the six loose-backend seams: an unset seam calls the wrapped fs backend
// directly.
func TestOpsLooseSeamsFallBackToTheLooseBackend(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "loose-fallback"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	var op ops
	d := cas.NewDigest([]byte("loose-fallback"))
	if err := op.putDo(ctx, backend.loose, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatalf("putDo(zero ops) = %v, want the loose Put", err)
	}
	if ok, err := op.existsDo(ctx, backend.loose, d); err != nil || !ok {
		t.Fatalf("existsDo(zero ops) = (%v, %v), want (true, nil)", ok, err)
	}
	reader, err := op.getDo(ctx, backend.loose, d)
	if err != nil {
		t.Fatalf("getDo(zero ops) = %v, want the loose Get", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("getDo(zero ops) = %q, want %q", payload, "payload")
	}
	if list, err := op.listDo(ctx, backend.loose); err != nil || len(list) != 1 {
		t.Fatalf("listDo(zero ops) = (%v, %v), want the one loose digest", list, err)
	}
	if stats, err := op.statsDo(ctx, backend.loose); err != nil || stats.ObjectCount != 1 {
		t.Fatalf("statsDo(zero ops) = (%+v, %v), want one loose object", stats, err)
	}
	if err := op.deleteDo(ctx, backend.loose, d); err != nil {
		t.Fatalf("deleteDo(zero ops) = %v, want the loose Delete", err)
	}
	if ok, err := op.existsDo(ctx, backend.loose, d); err != nil || ok {
		t.Fatalf("existsDo(zero ops) after delete = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestNewWithOpsReportsConstructionFailures pins that each construction step
// fails loudly and wraps the underlying error: pack directory creation, loose
// backend creation, and opening the active pack file.
func TestNewWithOpsReportsConstructionFailures(t *testing.T) {
	mkdirErr := errors.New("mkdir failed")
	op := realOps()
	op.mkdirAll = func(string, os.FileMode) error { return mkdirErr }
	if _, err := newWithOps(filepath.Join(t.TempDir(), "nopackdir"), op, WithEnabled()); !errors.Is(err, mkdirErr) {
		t.Fatalf("newWithOps(pack dir failure) = %v, want the mkdir error", err)
	}

	op = realOps()
	op.openFile = func(string, int, os.FileMode) (*os.File, error) {
		return nil, errors.New("open failed")
	}
	if _, err := newWithOps(filepath.Join(t.TempDir(), "nopackfile"), op, WithEnabled()); err == nil {
		t.Fatal("newWithOps(pack file failure) = nil, want error")
	}
}

// TestNewWithOpsReturnsCorruptManifestError pins that a manifest the pack
// backend cannot decode stops construction rather than starting with a silently
// empty index.
func TestNewWithOpsReturnsCorruptManifestError(t *testing.T) {
	base := filepath.Join(t.TempDir(), "corrupt-manifest")
	if err := os.MkdirAll(filepath.Join(base, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(base, WithEnabled()); err == nil {
		t.Fatal("New(corrupt manifest) = nil, want a decode error")
	}
}

// TestLoadIndexDropsUnusableRecords pins the filter loadIndex applies to a
// persisted manifest: a key that is not a digest and a record with a negative
// offset are both dropped, because neither names an addressable object.
func TestLoadIndexDropsUnusableRecords(t *testing.T) {
	base := filepath.Join(t.TempDir(), "index-filter")
	if err := os.MkdirAll(filepath.Join(base, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	good := cas.NewDigest([]byte("good-record"))
	manifestData := []byte(`{"entries":{
	  "not-a-digest": {"pack":"current.pack","offset":0,"size":1},
	  "` + good.String() + `": {"pack":"current.pack","offset":-1,"size":1}
	}}`)
	if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), manifestData, 0o644); err != nil {
		t.Fatal(err)
	}

	backend, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.index) != 0 {
		t.Fatalf("loadIndex kept %d records, want every unusable record dropped: %v", len(backend.index), backend.index)
	}
}

// TestValidPackRecordRejectsMalformedRecords pins the record validator directly:
// a negative or overflowing range, a pack path outside the pack directory, and a
// pack file that is not a regular file are all rejected as invalid.
func TestValidPackRecordRejectsMalformedRecords(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "record-valid"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	inside := backend.packFilePath
	if inside == "" {
		inside = filepath.Join(backend.packDir, "current.pack")
	}
	outside := filepath.Join(t.TempDir(), "external.pack")
	if err := os.WriteFile(outside, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirRecord := filepath.Join(backend.packDir, "a-directory")
	if err := os.MkdirAll(dirRecord, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		rec  packRecord
	}{
		{"negative offset", packRecord{Pack: inside, Offset: -1, Size: 1}},
		{"negative size", packRecord{Pack: inside, Offset: 0, Size: -1}},
		{"offset overflow", packRecord{Pack: inside, Offset: math.MaxInt64 - 1, Size: 2}},
		{"pack outside the pack dir", packRecord{Pack: outside, Offset: 0, Size: 1}},
		{"pack is a directory", packRecord{Pack: dirRecord, Offset: 0, Size: 1}},
	} {
		valid, err := backend.validPackRecord(tc.rec)
		if valid {
			t.Errorf("validPackRecord(%s) = true, want rejection", tc.name)
		}
		if err == nil {
			t.Errorf("validPackRecord(%s) = nil error, want the record rejected with a reason", tc.name)
		}
	}

	// A missing pack file is invalid but not an error: it is a stale record to
	// prune, not a corrupt one to report.
	valid, err := backend.validPackRecord(packRecord{Pack: filepath.Join(backend.packDir, "absent.pack"), Offset: 0, Size: 1})
	if valid || err != nil {
		t.Fatalf("validPackRecord(missing pack) = (%v, %v), want (false, nil)", valid, err)
	}
}

// TestPruneMissingPackEntriesLockedDropsUnusableRecords pins the sweep Stats
// performs: records the validator rejects are removed from the index, and
// records that are still valid stay.
func TestPruneMissingPackEntriesLockedDropsUnusableRecords(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "prune-index"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	live := cas.NewDigest([]byte("live-record"))
	if err := backend.Put(context.Background(), live, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	stale := cas.NewDigest([]byte("stale-record"))
	backend.mu.Lock()
	backend.index[string(stale)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 1}
	backend.pruneMissingPackEntriesLocked()
	_, keptLive := backend.index[string(live)]
	_, keptStale := backend.index[string(stale)]
	backend.mu.Unlock()

	if !keptLive {
		t.Fatal("pruneMissingPackEntriesLocked dropped a valid record")
	}
	if keptStale {
		t.Fatal("pruneMissingPackEntriesLocked kept a record whose pack file is gone")
	}
}

// TestRotatePackFileReportsCloseFailure pins that rotation refuses to continue
// when the pack it is replacing cannot be closed, so a failed handoff cannot
// lose records.
func TestRotatePackFileReportsCloseFailure(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "rotate-close"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	current := backend.packFile
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.rotatePackFile(); err == nil {
		t.Fatal("rotatePackFile() = nil, want the close failure")
	}
}

// TestAppendPackRecordReportsPackFileFailure pins that appending reports the
// failure when the active pack file cannot be recreated, rather than recording
// an index entry for a record that was never written.
func TestAppendPackRecordReportsPackFileFailure(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "append-fail"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	// The real descriptor is closed before it is dropped from the backend, so
	// the temporary directory can be removed on Windows too.
	if err := backend.packFile.Close(); err != nil {
		t.Fatal(err)
	}
	backend.packFile = nil
	backend.packDir = filepath.Join(backend.base, "missing-packs")
	d := cas.NewDigest([]byte("append-fail"))
	if err := backend.appendPackRecord(context.Background(), d, bytesReader([]byte("payload")), 7); err == nil {
		t.Fatal("appendPackRecord(no pack file) = nil, want error")
	}
	if _, ok := backend.index[string(d)]; ok {
		t.Fatal("appendPackRecord recorded an index entry for a record it could not write")
	}
}

// TestPackfsReadsRejectCancelledContexts pins that every read path checks the
// context before doing any work, so a cancelled request costs no filesystem
// access.
func TestPackfsReadsRejectCancelledContexts(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "ctx"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	d := cas.NewDigest([]byte("ctx"))

	if err := backend.Put(canceled, d, bytesReader([]byte("payload"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := backend.Get(canceled, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := backend.Exists(canceled, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exists(cancelled) = %v, want context.Canceled", err)
	}
	if err := backend.Delete(canceled, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := backend.List(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := backend.Stats(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stats(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := backend.Size(canceled, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("Size(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := backend.ModTime(canceled, d); !errors.Is(err, context.Canceled) {
		t.Fatalf("ModTime(cancelled) = %v, want context.Canceled", err)
	}
	if err := backend.GetMany(canceled, []cas.Digest{d}, func(cas.Digest, io.ReadCloser) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMany(cancelled) = %v, want context.Canceled", err)
	}
}

// TestPackfsGetReportsRecordValidationFailure pins that Get surfaces a failure
// to open a record's pack file as an error, and keeps the record: only a record
// the validator calls stale is pruned.
func TestPackfsGetReportsRecordValidationFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "get-validate"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("get-validate"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	openErr := errors.New("pack file unreadable")
	backend.op.open = func(string) (*os.File, error) { return nil, openErr }
	if _, err := backend.Get(ctx, d); !errors.Is(err, openErr) {
		t.Fatalf("Get(unopenable pack file) = %v, want the open failure", err)
	}
	if _, ok := backend.index[string(d)]; !ok {
		t.Fatal("Get dropped a record it could not open; only stale records are pruned")
	}
}

// TestPackfsGetFallsBackWhenIndexHasNoRecord pins the documented fallback: a
// digest with no pack record is read from the loose backend.
func TestPackfsGetFallsBackWhenIndexHasNoRecord(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "get-fallback"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("get-fallback"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	delete(backend.index, string(d))
	backend.mu.Unlock()

	reader, err := backend.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get(no record) = %v, want the loose object", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("Get(no record) = %q, want %q", payload, "payload")
	}
}

// TestPackfsExistsAddsStaleRecordToLooseLookup pins the recovery contract of
// Exists for a record whose pack file is gone: the stale record is pruned and
// the answer comes from the loose backend.
func TestPackfsExistsAddsStaleRecordToLooseLookup(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "exists-stale"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("exists-stale"))
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 1}
	backend.mu.Unlock()

	ok, err := backend.Exists(ctx, d)
	if err != nil {
		t.Fatalf("Exists(stale record) = %v, want nil", err)
	}
	if ok {
		t.Fatal("Exists(stale record) = true for an object that was never stored")
	}
	backend.mu.Lock()
	_, stillIndexed := backend.index[string(d)]
	backend.mu.Unlock()
	if stillIndexed {
		t.Fatal("Exists left the stale record in the index")
	}
}

// TestPackfsExistsReportsPersistFailure pins that Exists surfaces the failure to
// persist a pruned index instead of silently returning an answer.
func TestPackfsExistsReportsPersistFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "exists-persist"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("exists-persist"))
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 1}
	backend.mu.Unlock()
	persistErr := errors.New("persist failed")
	backend.op.writeFile = func(string, []byte, os.FileMode) error { return persistErr }

	if _, err := backend.Exists(ctx, d); !errors.Is(err, persistErr) {
		t.Fatalf("Exists(prune not persisted) = %v, want the persist failure", err)
	}
}

// TestPackfsDeleteReportsPersistFailure pins that Delete surfaces the failure to
// persist the index after removing the loose object.
func TestPackfsDeleteReportsPersistFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "delete-persist"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("delete-persist"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("persist failed")
	backend.op.writeFile = func(string, []byte, os.FileMode) error { return persistErr }

	if err := backend.Delete(ctx, d); !errors.Is(err, persistErr) {
		t.Fatalf("Delete(persist failure) = %v, want the persist failure", err)
	}
}

// TestPackfsListDeduplicatesIndexAgainstLoose pins that List reports each digest
// exactly once even when both the loose tree and the pack index hold it, and
// that it reports an index-only digest too.
func TestPackfsListDeduplicatesIndexAgainstLoose(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "list-dedup"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	both := cas.NewDigest([]byte("list-both"))
	if err := backend.Put(ctx, both, bytesReader([]byte("both"))); err != nil {
		t.Fatal(err)
	}
	// Re-adding the same digest to the index creates the both-places case; the
	// extra key exists only in the index.
	indexOnly := cas.NewDigest([]byte("list-index-only"))
	backend.mu.Lock()
	backend.index[string(both)] = packRecord{Pack: backend.packFilePath, Offset: 0, Size: 4}
	backend.index[string(indexOnly)] = packRecord{Pack: backend.packFilePath, Offset: 0, Size: 0}
	backend.mu.Unlock()

	list, err := backend.List(ctx)
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	seen := map[string]int{}
	for _, d := range list {
		seen[d.String()]++
	}
	if seen[both.String()] != 1 {
		t.Fatalf("List reported the loose-and-packed digest %d times, want exactly once", seen[both.String()])
	}
	if seen[indexOnly.String()] != 1 {
		t.Fatalf("List reported the index-only digest %d times, want exactly once", seen[indexOnly.String()])
	}
}

// TestPackfsStatsPrunesStaleRecordsAndSkipsLooseDuplicates pins Stats' reported
// totals: stale records are swept first, an index record that describes no
// payload is not counted, and an index record whose digest the loose backend
// already counts is not double-counted.
func TestPackfsStatsPrunesStaleRecordsAndSkipsLooseDuplicates(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "stats-totals"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	stored := cas.NewDigest([]byte("stats-stored"))
	if err := backend.Put(ctx, stored, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	before, err := backend.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	stale := cas.NewDigest([]byte("stats-stale"))
	empty := cas.NewDigest([]byte("stats-empty"))
	backend.mu.Lock()
	// Stale: the pack file does not exist, so Stats sweeps it.
	backend.index[string(stale)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 5}
	// Zero size: describes no payload, so it must not add to the totals.
	backend.index[string(empty)] = packRecord{Pack: backend.packFilePath, Offset: 0, Size: 0}
	// Duplicate of the loose digest: already counted by the loose backend.
	backend.index[string(stored)] = packRecord{Pack: backend.packFilePath, Offset: 0, Size: int64(len("payload"))}
	backend.mu.Unlock()

	after, err := backend.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() = %v, want nil", err)
	}
	if after.ObjectCount != before.ObjectCount || after.TotalSize != before.TotalSize {
		t.Fatalf("Stats() = %+v, want the loose totals %+v: stale, empty and duplicate records must not change them", after, before)
	}
	backend.mu.Lock()
	_, keptStale := backend.index[string(stale)]
	backend.mu.Unlock()
	if keptStale {
		t.Fatal("Stats left a stale record in the index")
	}
}

// TestPackfsStatsCountsIndexOnlyPayloads pins the other half of the total: a
// valid index record whose payload the loose backend does not hold is added to
// the object count and byte total.
func TestPackfsStatsCountsIndexOnlyPayloads(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "stats-index-only"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	// Store a real payload so the pack file is long enough for the record to
	// validate, then index it under a digest the loose backend does not hold.
	if err := backend.Put(ctx, cas.NewDigest([]byte("holder")), bytesReader([]byte("0123456789"))); err != nil {
		t.Fatal(err)
	}
	indexOnly := cas.NewDigest([]byte("stats-index-only"))
	backend.mu.Lock()
	backend.index[string(indexOnly)] = packRecord{Pack: backend.packFilePath, Offset: 0, Size: 4}
	backend.mu.Unlock()

	stats, err := backend.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() = %v, want nil", err)
	}
	if stats.ObjectCount < 2 {
		t.Fatalf("Stats().ObjectCount = %d, want the loose object plus the index-only record", stats.ObjectCount)
	}
	loose, err := backend.loose.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSize != loose.TotalSize+4 {
		t.Fatalf("Stats().TotalSize = %d, want the loose %d bytes plus the 4 indexed payload bytes", stats.TotalSize, loose.TotalSize)
	}
}

// TestPackfsGetManyReportsLooseReadFailureAndCallbackError pins the two failures
// of the loose half of a batch: a loose read that fails and a callback that
// reports an error after reading its object.
func TestPackfsGetManyReportsLooseReadFailureAndCallbackError(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-loose"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("getmany-loose"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	delete(backend.index, string(d))
	backend.mu.Unlock()

	readErr := errors.New("loose read failed")
	backend.op.looseGet = func(context.Context, *fsbackend.Backend, cas.Digest) (io.ReadCloser, error) {
		return nil, readErr
	}
	if err := backend.GetMany(ctx, []cas.Digest{d}, func(cas.Digest, io.ReadCloser) error { return nil }); !errors.Is(err, readErr) {
		t.Fatalf("GetMany(loose read failure) = %v, want the read failure", err)
	}

	backend.op.looseGet = nil
	callbackErr := errors.New("callback failed")
	err = backend.GetMany(ctx, []cas.Digest{d}, func(_ cas.Digest, reader io.ReadCloser) error {
		if _, readErr := io.Copy(io.Discard, reader); readErr != nil {
			return readErr
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("GetMany(callback error) = %v, want the callback's error", err)
	}
}

// TestPackfsGetManyReportsRecordValidationFailure pins that the batch planner
// refuses an unvalidatable record instead of silently treating it as loose.
func TestPackfsGetManyReportsRecordValidationFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-validate"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("getmany-validate"))
	blocker := filepath.Join(backend.packDir, "blocked")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(blocker, "child.pack"), Offset: 0, Size: 1}
	backend.mu.Unlock()

	err = backend.GetMany(ctx, []cas.Digest{d}, func(cas.Digest, io.ReadCloser) error { return nil })
	if err == nil {
		t.Fatal("GetMany(unvalidatable record) = nil, want the validation failure")
	}
}

// TestPackfsGetManyReportsPersistFailureWhenPruning pins that a batch which
// prunes a stale record fails loudly when the pruned index cannot be persisted.
func TestPackfsGetManyReportsPersistFailureWhenPruning(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-persist"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("getmany-persist"))
	backend.mu.Lock()
	backend.index[string(d)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 1}
	backend.mu.Unlock()
	persistErr := errors.New("persist failed")
	backend.op.writeFile = func(string, []byte, os.FileMode) error { return persistErr }

	if err := backend.GetMany(ctx, []cas.Digest{d}, func(cas.Digest, io.ReadCloser) error { return nil }); !errors.Is(err, persistErr) {
		t.Fatalf("GetMany(prune not persisted) = %v, want the persist failure", err)
	}
}

// TestPackfsGetManyStopsMidGroupOnContextCancellation pins that a context
// cancelled between two objects of one pack stops the batch before serving the
// next object, and that the shared pack file is still closed.
func TestPackfsGetManyStopsMidGroupOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-midcancel"), WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	digests := putMany(t, backend, 3)
	served := 0
	err = backend.GetMany(ctx, digests, func(_ cas.Digest, reader io.ReadCloser) error {
		served++
		if served == 1 {
			cancel()
		}
		_, readErr := io.Copy(io.Discard, reader)
		return readErr
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMany(mid-group cancellation) = %v, want context.Canceled", err)
	}
	if served != 1 {
		t.Fatalf("GetMany served %d objects after cancellation, want only the one already in flight", served)
	}
}

// TestPackfsGetManyReportsCloseFailure pins that a pack file that cannot be
// closed is reported, because leaving it open is a leak the batch must not hide.
func TestPackfsGetManyReportsCloseFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "getmany-close"), WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	digests := putMany(t, backend, 2)
	// Close the pack file behind the backend's back: the batch's close of its
	// own open then fails, which must surface instead of being ignored.
	backend.op.open = func(name string) (*os.File, error) {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return f, nil
	}
	if err := backend.GetMany(ctx, digests, func(cas.Digest, io.ReadCloser) error { return nil }); err == nil {
		t.Fatal("GetMany(close failure) = nil, want the close error")
	}
}

// TestPackfsPutReportsSpoolFailure pins that a Put whose reader fails mid-stream
// is reported and leaves no scratch file behind.
func TestPackfsPutReportsSpoolFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "put-spool"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	spoolErr := errors.New("spool source failed")
	if err := backend.Put(ctx, cas.NewDigest([]byte("put-spool")), errReader{err: spoolErr}); !errors.Is(err, spoolErr) {
		t.Fatalf("Put(failing reader) = %v, want the reader's error", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(backend.packDir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("Put left scratch files behind after a failed spool: %v", leftovers)
	}
}

// TestPackfsLooseBackendCreationFailureIsWrapped pins that a failed loose backend
// creation aborts construction with the underlying reason attached.
func TestPackfsLooseBackendCreationFailureIsWrapped(t *testing.T) {
	base := filepath.Join(t.TempDir(), "loose-fail")
	// <base>/loose exists as a regular file, so the loose backend cannot create
	// its base directory there.
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "loose"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pack directory creation uses the seam, so let it succeed on disk.
	if _, err := New(base, WithEnabled()); err == nil {
		t.Fatal("New(loose blocked by a file) = nil, want an error")
	} else if !strings.Contains(err.Error(), "cas: ") {
		t.Fatalf("New(loose blocked by a file) = %v, want a wrapped cas error", err)
	}
}

// failNth wraps realOps so the nth call to mkdirAll fails, which lets a test
// reach a specific construction step without a package-level seam.
func failNth(t *testing.T, op ops, nth int, err error) ops {
	t.Helper()
	real := op.mkdirAll
	calls := 0
	op.mkdirAll = func(dir string, perm os.FileMode) error {
		calls++
		if calls == nth {
			return err
		}
		return real(dir, perm)
	}
	return op
}

// TestNewWithOpsReportsSecondMkdirAllFailure pins that a failure to create the
// second directory New needs (the pack directory under the base) is reported
// rather than ignored: os.MkdirAll treats some paths as already usable, so the
// seam is what makes this step deterministic on every platform.
func TestNewWithOpsReportsSecondMkdirAllFailure(t *testing.T) {
	packDirErr := errors.New("pack dir failed")
	op := failNth(t, realOps(), 2, packDirErr)
	if _, err := newWithOps(filepath.Join(t.TempDir(), "nopackdir"), op, WithEnabled()); !errors.Is(err, packDirErr) {
		t.Fatalf("newWithOps(second mkdirAll failure) = %v, want the pack dir error", err)
	}
}

// TestPackfsAppendReportsPackFileRecreationFailure pins that appending reports
// the failure when it cannot (re)open the active pack file, rather than
// recording an index entry for a record that was never written.
func TestPackfsAppendReportsPackFileRecreationFailure(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "append-open"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	// Drop the active handle and make the reopen fail through the seam, so the
	// failure is driven without depending on one platform's path semantics. The
	// real descriptor is closed first: Windows will not delete a directory that
	// still holds an open handle.
	closeErr := errors.New("pack file unavailable")
	backend.op.openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, closeErr }
	if err := backend.packFile.Close(); err != nil {
		t.Fatal(err)
	}
	backend.packFile = nil
	d := cas.NewDigest([]byte("append-open"))
	if err := backend.appendPackRecord(context.Background(), d, bytesReader([]byte("payload")), 7); !errors.Is(err, closeErr) {
		t.Fatalf("appendPackRecord(reopen failure) = %v, want the open failure", err)
	}
	if _, ok := backend.index[string(d)]; ok {
		t.Fatal("appendPackRecord recorded an index entry for a record it could not write")
	}
}

// TestPackfsAppendReportsPackFileWriteFailure pins that a pack file which cannot
// be written aborts the append before the index records the object.
func TestPackfsAppendReportsPackFileWriteFailure(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "append-write"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	// A pipe's read end is a real handle that rejects writes, so the failure is
	// genuine rather than an injected seam.
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writeEnd.Close()
	if err := backend.packFile.Close(); err != nil {
		t.Fatal(err)
	}
	backend.packFile = readEnd
	// Backend.Close releases the active handle, which is what lets the
	// temporary directory be removed on Windows.
	defer backend.Close()

	d := cas.NewDigest([]byte("append-write"))
	if err := backend.appendPackRecord(context.Background(), d, bytesReader([]byte("payload")), 7); err == nil {
		t.Fatal("appendPackRecord(unwritable pack file) = nil, want an error")
	}
	if _, ok := backend.index[string(d)]; ok {
		t.Fatal("appendPackRecord recorded an index entry for a record it could not write")
	}
}

// TestPackfsDeleteReportsIndexPersistFailure pins that Delete surfaces the
// failure to persist the index after the loose object is removed, so a caller
// never believes an index it cannot write is up to date.
func TestPackfsDeleteReportsIndexPersistFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "delete-persist"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	d := cas.NewDigest([]byte("delete-persist"))
	if err := backend.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("persist failed")
	backend.op.writeFile = func(string, []byte, os.FileMode) error { return persistErr }

	if err := backend.Delete(ctx, d); !errors.Is(err, persistErr) {
		t.Fatalf("Delete(persist failure) = %v, want the persist failure", err)
	}
}

// TestPackfsCloseReportsUnderlyingCloseFailure pins that Close propagates a
// failure to close the active pack file instead of reporting a clean shutdown
// while the descriptor may still be open.
func TestPackfsCloseReportsUnderlyingCloseFailure(t *testing.T) {
	backend, err := New(filepath.Join(t.TempDir(), "close-failure"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.packFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err == nil {
		t.Fatal("Close(already closed pack file) = nil, want the close failure")
	}
	if backend.packFile != nil {
		t.Fatal("Close left the pack file set after reporting a failure")
	}
}

// TestPackfsCleanRemovesScratchNamedNonRegularEntries pins that Clean's sweep is
// name-based, not type-based: a scratch-named entry that is not a pack or an
// index (a leftover symlink, say) is removed and counted, while a non-scratch
// entry of the same shape is left alone.
func TestPackfsCleanRemovesScratchNamedNonRegularEntries(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "clean-symlink"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	swept := filepath.Join(backend.packDir, "linked.tmp")
	kept := filepath.Join(backend.packDir, "linked.keep")
	for _, link := range []string{swept, kept} {
		if err := os.Symlink(filepath.Join(backend.packDir, "absent-target"), link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	removed, err := backend.Clean(ctx, 0)
	if err != nil {
		t.Fatalf("Clean(0) = %v, want nil", err)
	}
	if removed != 1 {
		t.Fatalf("Clean(0) removed %d entries, want exactly the scratch-named one", removed)
	}
	if _, err := os.Lstat(swept); !os.IsNotExist(err) {
		t.Fatalf("Clean left the scratch-named entry %s", swept)
	}
	if _, err := os.Lstat(kept); err != nil {
		t.Fatalf("Clean removed the non-scratch entry %s: %v", kept, err)
	}
}

// TestPackfsStatsReportsLooseListingFailure pins that Stats reports a loose
// listing failure rather than returning a partial total.
func TestPackfsStatsReportsLooseListingFailure(t *testing.T) {
	ctx := context.Background()
	backend, err := New(filepath.Join(t.TempDir(), "stats-listfail"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	listErr := errors.New("list failed")
	backend.op.looseList = func(context.Context, *fsbackend.Backend) ([]cas.Digest, error) {
		return nil, listErr
	}
	if _, err := backend.Stats(ctx); !errors.Is(err, listErr) {
		t.Fatalf("Stats(loose list failure) = %v, want the listing failure", err)
	}

	backend.op.looseList = nil
	statsErr := errors.New("stats failed")
	backend.op.looseStats = func(context.Context, *fsbackend.Backend) (*cas.Stats, error) {
		return nil, statsErr
	}
	if _, err := backend.Stats(ctx); !errors.Is(err, statsErr) {
		t.Fatalf("Stats(loose stats failure) = %v, want the stats failure", err)
	}
}
