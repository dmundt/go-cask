package packfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
)

func TestPackBackendRoundTripAndList(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(10), WithPackMaxBytes(1024))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	d := cas.NewDigest([]byte("hello world"))
	if err := b.Put(ctx, d, bytesReader([]byte("hello world"))); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Exists(ctx, d); err != nil || !ok {
		t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	reader, err := b.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("Get() = %q, want %q", string(got), "hello world")
	}
	list, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1", len(list))
	}
	stats, err := b.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ObjectCount != 1 {
		t.Fatalf("Stats().ObjectCount = %d, want 1", stats.ObjectCount)
	}
}

func TestPackBackendDisabledMatchesLoose(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "plain")
	b, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	d := cas.NewDigest([]byte("absent"))
	if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	ok, err := b.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	if err := b.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	ok, err = b.Exists(ctx, d)
	if err != nil || ok {
		t.Fatalf("Exists() after Delete = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestPackBackendPersistsIndexAcrossRestart(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "persist")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	d := cas.NewDigest([]byte("persist me"))
	if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	ok, err := reopened.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("reopened Exists() = (%v, %v), want (true, nil)", ok, err)
	}
	reader, err := reopened.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("reopened payload = %q, want %q", string(payload), "payload")
	}
}

func TestPackBackendRotatesByEntryLimit(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "rotate")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(1), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	first := cas.NewDigest([]byte("first"))
	second := cas.NewDigest([]byte("second"))
	if err := b.Put(ctx, first, bytesReader([]byte("one"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, second, bytesReader([]byte("two"))); err != nil {
		t.Fatal(err)
	}

	if b.packEntries == 0 {
		t.Fatal("packEntries should be > 0 after rotation")
	}
	if len(b.index) != 2 {
		t.Fatalf("index len = %d, want 2", len(b.index))
	}
	if _, ok := b.index[string(first)]; !ok {
		t.Fatal("first digest missing from index")
	}
	if _, ok := b.index[string(second)]; !ok {
		t.Fatal("second digest missing from index")
	}
}

func TestPackBackendRejectsInvalidDigestAndCloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "invalid")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Put(ctx, nil, bytesReader([]byte("x"))); err == nil {
		t.Fatal("Put(nil) = nil, want error")
	}
	if _, err := b.Get(ctx, nil); err == nil {
		t.Fatal("Get(nil) = nil, want error")
	}
	if _, err := b.Exists(ctx, nil); err == nil {
		t.Fatal("Exists(nil) = nil, want error")
	}
	if err := b.Delete(ctx, nil); err == nil {
		t.Fatal("Delete(nil) = nil, want error")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close() first call = %v, want nil", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close() second call = %v, want nil", err)
	}
}

func TestPackBackendRotatesByByteLimit(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "bytes")
	b, err := New(base, WithEnabled(), WithPackMaxBytes(64), WithPackMaxEntries(0))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	for _, d := range []cas.Digest{cas.NewDigest([]byte("one")), cas.NewDigest([]byte("two"))} {
		if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.index) != 2 {
		t.Fatalf("index len = %d, want 2", len(b.index))
	}
	if b.packEntries == 0 {
		t.Fatal("packEntries should be > 0 after byte-based rotation")
	}
}

func TestPackBackendManifestFallbackAndStatsReads(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "manifest-check")
	if err := os.MkdirAll(filepath.Join(base, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(base, WithEnabled()); err == nil {
		t.Fatal("corrupt manifest should return error")
	}

	b, err := New(filepath.Join(t.TempDir(), "stats-check"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	first := cas.NewDigest([]byte("alpha"))
	second := cas.NewDigest([]byte("beta"))
	if err := b.Put(ctx, first, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	b.index[string(second)] = packRecord{Pack: filepath.Join(b.packDir, "missing.pack"), Offset: 0, Size: 8}
	if _, err := b.Get(ctx, second); err == nil {
		t.Fatal("Get missing pack record should error")
	}
	if _, err := b.List(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := b.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ObjectCount == 0 {
		t.Fatal("Stats should count at least the loose object")
	}
}

func TestPackBackendPrunesStaleIndexEntriesOnLoad(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "stale-load")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	d := cas.NewDigest([]byte("stale-load-object"))
	if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(base, "packs", "current.pack")); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"entries": map[string]any{
			string(d): map[string]any{
				"pack": filepath.Join(base, "packs", "missing.pack"),
				"offset": 0,
				"size": 7,
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	if _, ok := reopened.index[string(d)]; ok {
		t.Fatal("stale pack index entries should be pruned during load")
	}
	reader, err := reopened.Get(ctx, d)
	if err != nil {
		t.Fatal("stale pack index should fall back to the loose object")
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("recovered payload = %q, want %q", string(payload), "payload")
	}
}

func TestPackBackendCorruptionRecoveryAfterIndexMismatch(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "corruption-recovery")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	d := cas.NewDigest([]byte("corrupt-pack-object"))
	if err := b.Put(ctx, d, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}

	b.index[string(d)] = packRecord{Pack: filepath.Join(b.packDir, "missing.pack"), Offset: 0, Size: 7}
	reader, err := b.Get(ctx, d)
	if err != nil {
		t.Fatal("stale entry should recover using the loose object")
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("payload = %q, want %q", string(payload), "payload")
	}
	if _, ok := b.index[string(d)]; ok {
		t.Fatal("stale index entry should be removed after recovery")
	}
	ok, err := b.Exists(ctx, d)
	if err != nil || !ok {
		t.Fatalf("Exists() = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestPackBackendAppendAndCloseEdgeCases(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "edges")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(1), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	first := cas.NewDigest([]byte("first"))
	second := cas.NewDigest([]byte("second"))
	if err := b.Put(ctx, first, bytesReader([]byte("one"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, second, bytesReader([]byte("two"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.persistIndex(); err != nil {
		t.Fatal(err)
	}
	if b.packFile != nil {
		t.Fatal("closed backend should have no active pack file")
	}
	if _, err := b.Get(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Exists(ctx, first); err != nil || !got {
		t.Fatalf("Exists(first) = (%v, %v), want (true, nil)", got, err)
	}
	if got, err := b.List(ctx); err != nil || len(got) != 2 {
		t.Fatalf("List() = (%v, %v), want len 2", got, err)
	}
	if stats, err := b.Stats(ctx); err != nil || stats.ObjectCount != 2 {
		t.Fatalf("Stats() = (%v, %v), want object count 2", stats, err)
	}
}

func TestPackBackendPersistIndexFailure(t *testing.T) {
	b, err := New(filepath.Join(t.TempDir(), "persist-fail"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	b.manifestPath = filepath.Join(t.TempDir(), "missing", "index.json")
	b.index["deadbeef"] = packRecord{Pack: "missing.pack", Offset: 1, Size: 2}
	if err := b.persistIndex(); err == nil {
		t.Fatal("persistIndex should fail when parent directory is missing")
	}
}

func TestPackBackendFaultInjectionAndCloseBranches(t *testing.T) {
	oldMkdirAll, oldReadFile, oldWriteFile, oldRename, oldOpenFile := mkdirAllFn, readFileFn, writeFileFn, renameFn, openFileFn
	defer func() {
		mkdirAllFn, readFileFn, writeFileFn, renameFn, openFileFn = oldMkdirAll, oldReadFile, oldWriteFile, oldRename, oldOpenFile
	}()

	mkdirAllFn = func(string, os.FileMode) error { return errors.New("mkdir fail") }
	if _, err := New(filepath.Join(t.TempDir(), "fault-new"), WithEnabled()); err == nil {
		t.Fatal("New should fail when mkdirAll fails")
	}
	mkdirAllFn = os.MkdirAll

	b := &Backend{manifestPath: filepath.Join(t.TempDir(), "manifest.json"), index: map[string]packRecord{"abc": {Pack: "pack.bin", Offset: 1, Size: 2}}}
	readFileFn = func(string) ([]byte, error) { return nil, errors.New("read fail") }
	if err := b.loadIndex(); err == nil {
		t.Fatal("loadIndex should fail on read error")
	}
	readFileFn = os.ReadFile

	writeFileFn = func(string, []byte, os.FileMode) error { return errors.New("write fail") }
	if err := b.persistIndex(); err == nil {
		t.Fatal("persistIndex should fail on write error")
	}
	writeFileFn = os.WriteFile
	renameFn = func(string, string) error { return errors.New("rename fail") }
	if err := b.persistIndex(); err == nil {
		t.Fatal("persistIndex should fail on rename error")
	}
	renameFn = os.Rename

	b = &Backend{packDir: t.TempDir()}
	openFileFn = func(string, int, os.FileMode) (*os.File, error) { return nil, errors.New("open fail") }
	if err := b.ensurePackFile(); err == nil {
		t.Fatal("ensurePackFile should fail on open error")
	}
	openFileFn = os.OpenFile

	file, err := os.CreateTemp(t.TempDir(), "pack-close-*.pack")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	b = &Backend{packFile: file}
	if err := b.Close(); err == nil {
		t.Fatal("Close should fail when closing an already-closed pack file")
	}
	if err := (&Backend{}).Close(); err != nil {
		t.Fatal("Close on an empty backend should be nil")
	}
}

func TestPackBackendFileOpenAndWriteFailureBranches(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "open-fail")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b.packFile = nil
	b.packDir = filepath.Join(base, "missing", "packs")
	if err := b.ensurePackFile(); err == nil {
		t.Fatal("ensurePackFile should fail when the pack directory cannot be opened")
	}
	if err := b.appendPackRecord(cas.NewDigest([]byte("abc")), []byte("payload")); err == nil {
		t.Fatal("appendPackRecord should fail when the pack file cannot be created")
	}
	if err := b.Put(ctx, cas.NewDigest([]byte("abc")), bytesReader([]byte("payload"))); err == nil {
		t.Fatal("Put should fail when pack-file creation fails")
	}
	if err := b.rotatePackFile(); err == nil {
		t.Fatal("rotatePackFile should fail when the target directory is invalid")
	}
}

func TestPackBackendDirectReadAndDeleteFailureBranches(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "read-fail")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	bad := cas.NewDigest([]byte("bad"))
	b.index[string(bad)] = packRecord{Pack: filepath.Join(base, "packs"), Offset: 0, Size: 4}
	if _, err := b.Get(ctx, bad); err == nil {
		t.Fatal("Get should fail when pack index points at a directory")
	}

	if _, err := b.Exists(ctx, cas.NewDigest([]byte("missing"))); err != nil {
		t.Fatal("Exists should return false, nil for an absent missing digest")
	}

	if err := b.Delete(ctx, cas.NewDigest([]byte("missing"))); err != nil {
		t.Fatal("Delete on a missing digest should be a no-op")
	}

	if err := b.Put(ctx, bad, errReader{err: io.ErrUnexpectedEOF}); err == nil {
		t.Fatal("Put should fail when the object reader returns an error")
	}
}

func TestPackBackendLooseOperationErrorBranches(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "loose-errors")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	oldPut, oldGet, oldExists, oldDelete, oldList, oldStats := loosePutFn, looseGetFn, looseExistsFn, looseDeleteFn, looseListFn, looseStatsFn
	defer func() {
		loosePutFn, looseGetFn, looseExistsFn, looseDeleteFn, looseListFn, looseStatsFn = oldPut, oldGet, oldExists, oldDelete, oldList, oldStats
	}()

	loosePutFn = func(context.Context, *fsbackend.Backend, cas.Digest, io.Reader) error { return io.ErrUnexpectedEOF }
	if err := b.Put(ctx, cas.NewDigest([]byte("put-fail")), bytesReader([]byte("x"))); err == nil {
		t.Fatal("loosePut error should surface from Put")
	}

	looseGetFn = func(context.Context, *fsbackend.Backend, cas.Digest) (io.ReadCloser, error) {
		return nil, io.ErrUnexpectedEOF
	}
	if _, err := b.Get(ctx, cas.NewDigest([]byte("get-fail"))); err == nil {
		t.Fatal("looseGet error should surface from Get")
	}

	looseExistsFn = func(context.Context, *fsbackend.Backend, cas.Digest) (bool, error) { return false, io.ErrUnexpectedEOF }
	if _, err := b.Exists(ctx, cas.NewDigest([]byte("exists-fail"))); err == nil {
		t.Fatal("looseExists error should surface from Exists")
	}

	looseDeleteFn = func(context.Context, *fsbackend.Backend, cas.Digest) error { return io.ErrUnexpectedEOF }
	if err := b.Delete(ctx, cas.NewDigest([]byte("delete-fail"))); err == nil {
		t.Fatal("looseDelete error should surface from Delete")
	}

	looseListFn = func(context.Context, *fsbackend.Backend) ([]cas.Digest, error) { return nil, io.ErrUnexpectedEOF }
	if _, err := b.List(ctx); err == nil {
		t.Fatal("looseList error should surface from List")
	}

	looseStatsFn = func(context.Context, *fsbackend.Backend) (*cas.Stats, error) { return nil, io.ErrUnexpectedEOF }
	if _, err := b.Stats(ctx); err == nil {
		t.Fatal("looseStats error should surface from Stats")
	}
}

func TestPackBackendGetAndStatsAfterIndexMismatch(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "index-mismatch")
	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	first := cas.NewDigest([]byte("alpha"))
	if err := b.Put(ctx, first, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	b.index[string(first)] = packRecord{Pack: filepath.Join(b.packDir, "missing.pack"), Offset: 0, Size: 0}
	reader, err := b.Get(ctx, first)
	if err != nil {
		t.Fatal("Get should recover from a stale pack index and use the loose object")
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	stats, err := b.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ObjectCount == 0 {
		t.Fatal("Stats should still see the loose object even when the pack index is stale")
	}
}

func TestPackBackendRotatesByEntryAndByteLimit(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "rotate-limits")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(1), WithPackMaxBytes(32))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	first := cas.NewDigest([]byte("first"))
	second := cas.NewDigest([]byte("second"))
	if err := b.Put(ctx, first, bytesReader([]byte("one"))); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, second, bytesReader([]byte("two"))); err != nil {
		t.Fatal(err)
	}
	if len(b.index) != 2 {
		t.Fatalf("index len = %d, want 2", len(b.index))
	}
	if b.packEntries == 0 {
		t.Fatal("packEntries should reflect file rotation")
	}
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func bytesReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}
