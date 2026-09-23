package packfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	defer reader.Close()
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

func TestPackBackendSupportsLargeDigests(t *testing.T) {
	ctx := context.Background()
	b, err := New(filepath.Join(t.TempDir(), "large-digest"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	digest := cas.NewDigest(bytes.Repeat([]byte{0xab}, 64))
	if err := b.Put(ctx, digest, bytesReader([]byte("payload"))); err != nil {
		t.Fatalf("Put() = %v, want nil", err)
	}
	reader, err := b.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get() = %v, want nil", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "payload" {
		t.Fatalf("Get() = %q, want %q", payload, "payload")
	}
}

// TestPackBackendPutStreamsLargeObject covers the streaming Put path: the
// object is spooled to a scratch file and copied into the length-prefixed pack
// record, so an object larger than any single buffer round-trips with the right
// record length and no scratch file left behind.
func TestPackBackendPutStreamsLargeObject(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "stream-large")
	b, err := New(base, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	d := cas.NewDigest([]byte("large-object"))
	if err := b.Put(ctx, d, bytesReader(payload)); err != nil {
		t.Fatalf("Put() = %v, want nil", err)
	}
	rec, ok := b.index[string(d)]
	if !ok {
		t.Fatal("Put did not record the object in the pack index")
	}
	if rec.Size != int64(len(payload)) {
		t.Fatalf("pack record size = %d, want %d", rec.Size, len(payload))
	}
	reader, err := b.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get() = %v, want nil", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get() returned %d bytes, want the %d stored bytes", len(got), len(payload))
	}
	spool, err := filepath.Glob(filepath.Join(base, "packs", "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(spool) != 0 {
		t.Fatalf("Put left scratch files behind: %v", spool)
	}
}

func TestPackBackendRejectsTruncatedPackPayload(t *testing.T) {
	ctx := context.Background()
	b, err := New(filepath.Join(t.TempDir(), "truncated"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	digest := cas.NewDigest([]byte("truncated-payload"))
	if err := b.Put(ctx, digest, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	rec := b.index[string(digest)]
	if err := os.Truncate(rec.Pack, rec.Offset+rec.Size-1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, digest); err == nil {
		t.Fatal("Get() = nil, want error for truncated pack payload")
	}
}

func TestPackBackendIgnoresUntrustedManifestRecords(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "untrusted-manifest")
	external := filepath.Join(t.TempDir(), "external.pack")
	if err := os.WriteFile(external, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := cas.NewDigest([]byte("untrusted-manifest"))
	manifestData, err := json.Marshal(manifest{Entries: map[string]packRecord{
		string(digest): {Pack: external, Offset: 0, Size: 6},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "packs", "index.json"), manifestData, 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := New(base, WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, ok := b.index[string(digest)]; ok {
		t.Fatal("manifest record outside pack directory must be ignored")
	}
	if _, err := b.Get(ctx, digest); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get() = %v, want ErrNotFound", err)
	}
}

func TestPackBackendRejectsInvalidIndexRecord(t *testing.T) {
	ctx := context.Background()
	b, err := New(filepath.Join(t.TempDir(), "invalid-index"), WithEnabled())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	digest := cas.NewDigest([]byte("invalid-index"))
	if err := b.Put(ctx, digest, bytesReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	b.index[string(digest)] = packRecord{
		Pack:   filepath.Join(b.packDir, "current.pack"),
		Offset: -1,
		Size:   7,
	}
	if _, err := b.Get(ctx, digest); err == nil {
		t.Fatal("Get() = nil, want invalid pack record error")
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
	defer reader.Close()
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
				"pack":   filepath.Join(base, "packs", "missing.pack"),
				"offset": 0,
				"size":   7,
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
	firstReader, err := b.Get(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstReader.Close(); err != nil {
		t.Fatal(err)
	}
	secondReader, err := b.Get(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondReader.Close(); err != nil {
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

// TestNewRejectsUnusableBase pins that the pack backend validates its base like
// the loose backend it wraps: the base exclusively owns <base>/loose, <base>/packs
// and <base>/packs/index.json, so the shapes fs.ValidateBase rejects are refused
// before anything is created.
func TestNewRejectsUnusableBase(t *testing.T) {
	for _, base := range []string{"", ".", "..", "../sibling"} {
		if _, err := New(base, WithEnabled()); err == nil {
			t.Errorf("New(%q) = nil error, want rejection", base)
		} else if !strings.Contains(err.Error(), "cas: ") {
			t.Errorf("New(%q) = %v, want the fs base error", base, err)
		}
	}
}

func TestPackBackendFaultInjectionAndCloseBranches(t *testing.T) {
	op := realOps()
	op.mkdirAll = func(string, os.FileMode) error { return errors.New("mkdir fail") }
	if _, err := newWithOps(filepath.Join(t.TempDir(), "fault-new"), op, WithEnabled()); err == nil {
		t.Fatal("New should fail when mkdirAll fails")
	}

	b := &Backend{manifestPath: filepath.Join(t.TempDir(), "manifest.json"), index: map[string]packRecord{"abc": {Pack: "pack.bin", Offset: 1, Size: 2}}}
	b.op.readFile = func(string) ([]byte, error) { return nil, errors.New("read fail") }
	if err := b.loadIndex(); err == nil {
		t.Fatal("loadIndex should fail on read error")
	}

	b.op.writeFile = func(string, []byte, os.FileMode) error { return errors.New("write fail") }
	if err := b.persistIndex(); err == nil {
		t.Fatal("persistIndex should fail on write error")
	}
	b.op.writeFile = nil
	b.op.rename = func(string, string) error { return errors.New("rename fail") }
	if err := b.persistIndex(); err == nil {
		t.Fatal("persistIndex should fail on rename error")
	}
	b.op.rename = nil

	b = &Backend{packDir: t.TempDir()}
	b.op.openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errors.New("open fail") }
	if err := b.ensurePackFile(); err == nil {
		t.Fatal("ensurePackFile should fail on open error")
	}
	b.op.openFile = nil

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
	if err := b.appendPackRecord(ctx, cas.NewDigest([]byte("abc")), bytesReader([]byte("payload")), int64(len("payload"))); err == nil {
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

	oldPut, oldGet, oldExists, oldDelete, oldList, oldStats := b.op.loosePut, b.op.looseGet, b.op.looseExists, b.op.looseDelete, b.op.looseList, b.op.looseStats
	defer func() {
		b.op.loosePut, b.op.looseGet, b.op.looseExists, b.op.looseDelete, b.op.looseList, b.op.looseStats = oldPut, oldGet, oldExists, oldDelete, oldList, oldStats
	}()

	b.op.loosePut = func(context.Context, *fsbackend.Backend, cas.Digest, io.Reader) error { return io.ErrUnexpectedEOF }
	if err := b.Put(ctx, cas.NewDigest([]byte("put-fail")), bytesReader([]byte("x"))); err == nil {
		t.Fatal("loosePut error should surface from Put")
	}

	b.op.looseGet = func(context.Context, *fsbackend.Backend, cas.Digest) (io.ReadCloser, error) {
		return nil, io.ErrUnexpectedEOF
	}
	if _, err := b.Get(ctx, cas.NewDigest([]byte("get-fail"))); err == nil {
		t.Fatal("looseGet error should surface from Get")
	}

	b.op.looseExists = func(context.Context, *fsbackend.Backend, cas.Digest) (bool, error) {
		return false, io.ErrUnexpectedEOF
	}
	if _, err := b.Exists(ctx, cas.NewDigest([]byte("exists-fail"))); err == nil {
		t.Fatal("looseExists error should surface from Exists")
	}

	b.op.looseDelete = func(context.Context, *fsbackend.Backend, cas.Digest) error { return io.ErrUnexpectedEOF }
	if err := b.Delete(ctx, cas.NewDigest([]byte("delete-fail"))); err == nil {
		t.Fatal("looseDelete error should surface from Delete")
	}

	b.op.looseList = func(context.Context, *fsbackend.Backend) ([]cas.Digest, error) { return nil, io.ErrUnexpectedEOF }
	if _, err := b.List(ctx); err == nil {
		t.Fatal("looseList error should surface from List")
	}

	b.op.looseStats = func(context.Context, *fsbackend.Backend) (*cas.Stats, error) { return nil, io.ErrUnexpectedEOF }
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
