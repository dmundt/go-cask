package sidecar_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	packfs "github.com/dmundt/go-cask/cas/backend/packfs"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/verify/adler32"
	"github.com/dmundt/go-cask/cas/verify/crc32"
	"github.com/dmundt/go-cask/cas/verify/crc64"
	"github.com/dmundt/go-cask/cas/verify/sidecar"
	"github.com/dmundt/go-cask/internal/test"
)

const metaDir = ".meta"

// note is a minimal typed object, so a test can put a real envelope through the
// record path and check the optional type/codec fields.
type note struct {
	Text string `json:"text"`
}

func (n *note) Type() string             { return "note@1" }
func (n *note) References() []cas.Digest { return nil }

// mustFS returns a filesystem backend in a fresh temporary directory.
func mustFS(t *testing.T) (*fsbackend.Backend, string) {
	t.Helper()
	base := t.TempDir()
	backend, err := fsbackend.New(base)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	return backend, base
}

// mustSidecar wraps backend with a crc32 record store.
func mustSidecar(t *testing.T, backend cas.Backend, options ...sidecar.Option) *sidecar.Backend {
	t.Helper()
	rec, err := sidecar.New(backend, options...)
	if err != nil {
		t.Fatalf("sidecar.New: %v", err)
	}
	return rec
}

// crc32Recorder wraps backend so that Put records a crc32 checksum.
func crc32Recorder(t *testing.T, backend cas.Backend) *sidecar.Backend {
	t.Helper()
	return mustSidecar(t, backend, sidecar.WithChecksum(crc32.Name, crc32.New()))
}

// put stores data under its sha256 address and returns that address.
func put(t *testing.T, backend cas.Backend, data []byte) cas.Digest {
	t.Helper()
	d := sha256.Of(data)
	if err := backend.Put(context.Background(), d, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return d
}

// recordPath is the file a record for d lives in, reached the way an operator
// would: <base>/.meta/<hex>.json.
func recordPath(base string, d cas.Digest) string {
	return filepath.Join(base, metaDir, d.String()+".json")
}

// objectPath is the fs backend's default 2x1 fan-out path for d.
func objectPath(base string, d cas.Digest) string {
	return filepath.Join(base, d.String()[:2], d.String())
}

// writeRecord writes raw record bytes, so a test can produce damage the package
// itself would never create.
func writeRecord(t *testing.T, base string, d cas.Digest, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, metaDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath(base, d), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPutRecordsChecksumAndVerify(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	data := []byte("record me")
	d := put(t, rec, data)

	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if record.Version != sidecar.RecordVersion {
		t.Errorf("Version = %d, want %d", record.Version, sidecar.RecordVersion)
	}
	if !record.Digest.Equal(d) {
		t.Errorf("Digest = %s, want %s", record.Digest, d)
	}
	if record.ChecksumAlgo != crc32.Name {
		t.Errorf("ChecksumAlgo = %q, want %q", record.ChecksumAlgo, crc32.Name)
	}
	if !record.Checksum.Equal(crc32.Of(data)) {
		t.Errorf("Checksum = %s, want %s", record.Checksum, crc32.Of(data))
	}
	if record.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", record.Size, len(data))
	}
	if record.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if record.Type != "" || record.Codec != "" {
		t.Errorf("Type/Codec = %q/%q, want empty for non-envelope bytes", record.Type, record.Codec)
	}
	if _, err := os.Stat(recordPath(base, d)); err != nil {
		t.Errorf("record file: %v", err)
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestPutRecordsEnvelopeTypeAndCodec(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	store := cas.New(rec, jsoncodec.New[*note](), sha256.New())
	d, err := store.Put(ctx, &note{Text: "hello"})
	if err != nil {
		t.Fatalf("Store.Put: %v", err)
	}
	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if record.Type != "note@1" {
		t.Errorf("Type = %q, want %q", record.Type, "note@1")
	}
	if record.Codec != "json" {
		t.Errorf("Codec = %q, want %q", record.Codec, "json")
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The typed read is unaffected by the decorator.
	got, err := store.Get(ctx, d)
	if err != nil {
		t.Fatalf("Store.Get: %v", err)
	}
	if got.Text != "hello" {
		t.Errorf("Text = %q, want %q", got.Text, "hello")
	}
}

func TestRecordFilesAreInvisibleToListAndStats(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	first := put(t, rec, []byte("first object"))
	second := put(t, rec, []byte("second object"))

	digests, err := backend.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(digests) != 2 {
		t.Fatalf("List returned %d digests, want 2: %v", len(digests), digests)
	}
	for _, d := range digests {
		if strings.HasSuffix(d.String(), ".json") {
			t.Fatalf("List returned a record file as an object: %s", d)
		}
	}
	if !test.ContainsDigest(digests, first) || !test.ContainsDigest(digests, second) {
		t.Fatalf("List = %v, want both stored objects", digests)
	}
	stats, err := backend.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	wantBytes := int64(len("first object") + len("second object"))
	if stats.ObjectCount != 2 || stats.TotalSize != wantBytes {
		t.Errorf("Stats = %d objects / %d bytes, want 2 / %d", stats.ObjectCount, stats.TotalSize, wantBytes)
	}
}

func TestCleanReclaimsSidecarScratch(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("scratch owner"))

	scratch := filepath.Join(base, metaDir, d.String()+".1234.tmp")
	if err := os.WriteFile(scratch, []byte("half a record"), 0o644); err != nil {
		t.Fatal(err)
	}
	// fs.Clean's temp rule matches "<name>.tmp" and "<name>.tmp.<n>", which is
	// exactly the shape a crashed record write leaves behind.
	if err := os.WriteFile(scratch+".7", []byte("another"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := backend.Clean(ctx, 0)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if removed != 2 {
		t.Errorf("Clean removed %d files, want 2", removed)
	}
	for _, p := range []string{scratch, scratch + ".7"} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("scratch %s survived Clean: %v", p, err)
		}
	}
	if _, err := rec.Load(ctx, d); err != nil {
		t.Errorf("Clean removed the record itself: %v", err)
	}
}

func TestObjectWithoutRecordIsNotFoundNotCorruption(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	verifier := rec.Verifier(crc32.Name, crc32.New())

	// An object stored through the plain backend never gets a record.
	d := put(t, backend, []byte("unrecorded"))
	err := verifier.Verify(ctx, d)
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Verify without a record = %v, want cas.ErrNotFound", err)
	}
	if errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("a missing record was reported as corruption: %v", err)
	}
	if !errors.Is(err, sidecar.ErrUnrecorded) {
		t.Fatalf("Verify without a record = %v, want sidecar.ErrUnrecorded", err)
	}
	if _, err := rec.Load(ctx, d); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Load without a record = %v, want cas.ErrNotFound", err)
	}
	if _, err := rec.Load(ctx, d); !errors.Is(err, sidecar.ErrUnrecorded) {
		t.Fatalf("Load without a record = %v, want sidecar.ErrUnrecorded", err)
	}
	// A missing object is not a missing record: the record stays absent, but the
	// error names the object the caller asked for.
	if _, err := rec.Load(ctx, sha256.Of([]byte("never stored"))); !errors.Is(err, sidecar.ErrUnrecorded) {
		t.Fatalf("Load of an unknown digest = %v, want sidecar.ErrUnrecorded", err)
	}

	// A record that is removed after the fact is the same state: unchecked,
	// not damaged.
	recorded := put(t, rec, []byte("recorded then deleted"))
	if err := os.Remove(recordPath(base, recorded)); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(ctx, recorded); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Verify after record removal = %v, want cas.ErrNotFound", err)
	}
}

func TestTamperedBytesFailTheRecordedChecksum(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	verifier := rec.Verifier(crc32.Name, crc32.New())
	data := []byte("tamper with me")
	d := put(t, rec, data)

	// Same length: the size check passes and the checksum is what catches it.
	tampered := bytes.Repeat([]byte("x"), len(data))
	if err := os.WriteFile(objectPath(base, d), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifier.Verify(ctx, d)
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Verify(tampered) = %v, want cas.ErrCorrupt", err)
	}
	if errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("the cheap check reported an address mismatch: %v", err)
	}

	// A different length is caught by the recorded size.
	if err := os.WriteFile(objectPath(base, d), data[:4], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Verify(truncated) = %v, want cas.ErrCorrupt", err)
	}
}

func TestWrongAlgorithmIsNotCorruption(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("crc32 addressed, adler32 read"))

	// crc32 and adler32 are both four bytes wide, so only the recorded name can
	// tell this apart from corruption.
	err := rec.Verifier(adler32.Name, adler32.New()).Verify(ctx, d)
	if !errors.Is(err, sidecar.ErrChecksumAlgorithm) {
		t.Fatalf("Verify with another algorithm = %v, want ErrChecksumAlgorithm", err)
	}
	if errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("a wrong-algorithm read was reported as corruption: %v", err)
	}
}

func TestDeleteDropsRecordAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("delete me"))
	if _, err := rec.Load(ctx, d); err != nil {
		t.Fatalf("Load before Delete: %v", err)
	}
	if err := rec.Delete(ctx, d); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(recordPath(base, d)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("record survived Delete: %v", err)
	}
	if exists, err := rec.Exists(ctx, d); err != nil || exists {
		t.Errorf("Exists after Delete = %v, %v; want false, nil", exists, err)
	}
	if err := rec.Delete(ctx, d); err != nil {
		t.Errorf("second Delete = %v, want nil", err)
	}
}

func TestRepeatPutDoesNotRefreshRecord(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	data := []byte("deterministic record")
	d := put(t, rec, data)
	before, err := os.ReadFile(recordPath(base, d))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := rec.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("repeat Put: %v", err)
	}
	after, err := os.ReadFile(recordPath(base, d))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("repeat Put rewrote the record:\n before %s\n after  %s", before, after)
	}
}

func TestReconcileRemovesOrphansAndListsUnrecorded(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	orphaned := put(t, rec, []byte("record will be orphaned"))
	kept := put(t, rec, []byte("record stays"))
	unrecorded := put(t, backend, []byte("never recorded"))

	// Delete the object behind the decorator's back: its record is now an
	// orphan, exactly what a crash between the two writes leaves behind.
	if err := backend.Delete(ctx, orphaned); err != nil {
		t.Fatal(err)
	}
	report, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Records != 2 {
		t.Errorf("Records = %d, want 2", report.Records)
	}
	if len(report.Removed) != 1 || !report.Removed[0].Equal(orphaned) {
		t.Errorf("Removed = %v, want [%s]", report.Removed, orphaned)
	}
	if _, err := os.Stat(recordPath(base, orphaned)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("orphan record survived Reconcile: %v", err)
	}
	if len(report.Unrecorded) != 1 || !report.Unrecorded[0].Equal(unrecorded) {
		t.Errorf("Unrecorded = %v, want [%s]", report.Unrecorded, unrecorded)
	}
	if _, err := rec.Load(ctx, kept); err != nil {
		t.Errorf("Reconcile removed a live record: %v", err)
	}
	// Reconcile is idempotent.
	again, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(again.Removed) != 0 {
		t.Errorf("second Reconcile removed %v, want none", again.Removed)
	}
}

func TestVerifyAllSeparatesBadFromUnrecorded(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	intact := put(t, rec, []byte("intact object"))
	tampered := put(t, rec, []byte("tampered object"))
	unrecorded := put(t, backend, []byte("unrecorded object"))
	tamper := bytes.Repeat([]byte("x"), len("tampered object"))
	if err := os.WriteFile(objectPath(base, tampered), tamper, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := rec.Verifier(crc32.Name, crc32.New()).VerifyAll(ctx)
	if err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}
	if report.Checked != 2 {
		t.Errorf("Checked = %d, want 2", report.Checked)
	}
	if len(report.Bad) != 1 || !report.Bad[0].Equal(tampered) {
		t.Errorf("Bad = %v, want [%s]", report.Bad, tampered)
	}
	if len(report.Unrecorded) != 1 || !report.Unrecorded[0].Equal(unrecorded) {
		t.Errorf("Unrecorded = %v, want [%s]", report.Unrecorded, unrecorded)
	}
	if !test.ContainsDigest([]cas.Digest{intact}, intact) {
		t.Error("intact object missing from the checked set")
	}
}

func TestVerifyAllContinuesPastAnUnreadableRecord(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	intact := put(t, rec, []byte("intact object"))
	unparsable := put(t, rec, []byte("unparsable record"))
	oversized := put(t, rec, []byte("oversized record"))
	writeRecord(t, base, unparsable, "{ not json")
	writeRecord(t, base, oversized, strings.Repeat("x", sidecar.DefaultMaxRecordBytes+1))

	// One unusable record is damage to that record, not a reason to abandon the
	// integrity sweep: the pass reports it and still checks the rest of the store
	// (go-cask#362).
	report, err := rec.Verifier(crc32.Name, crc32.New()).VerifyAll(ctx)
	if err != nil {
		t.Fatalf("VerifyAll with an unreadable record = %v, want the pass to continue", err)
	}
	if report.Checked != 1 {
		t.Errorf("Checked = %d, want 1: only the intact record is readable", report.Checked)
	}
	if len(report.Bad) != 0 {
		t.Errorf("Bad = %v, want none: an unreadable record is not a checksum mismatch", report.Bad)
	}
	for _, d := range []cas.Digest{unparsable, oversized} {
		if !test.ContainsDigest(report.Unreadable, d) {
			t.Errorf("Unreadable = %v, want it to name %s", report.Unreadable, d)
		}
	}
	if test.ContainsDigest(report.Unreadable, intact) || test.ContainsDigest(report.Bad, intact) {
		t.Errorf("the intact object %s was reported as damaged", intact)
	}
	// The single-object read stays loud about the same damage.
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, unparsable); !errors.Is(err, cas.ErrCorrupt) {
		t.Errorf("Verify on an unreadable record = %v, want cas.ErrCorrupt", err)
	}
}

func TestWriterNeverPublishesARecordItsReaderRefuses(t *testing.T) {
	// The write path owes the read path this property: whatever Put publishes,
	// readRecord accepts back with the same configuration (go-cask#362). A legal
	// envelope type name may run to maxPeekNameLen, which on its own pushes the
	// record past DefaultMaxRecordBytes, so the writer has to bound the record it
	// writes.
	ctx := context.Background()
	for _, nameLen := range []int{0, 32, 2048, 4000, 4096} {
		t.Run(fmt.Sprintf("type-name-%d", nameLen), func(t *testing.T) {
			backend, base := mustFS(t)
			rec := crc32Recorder(t, backend)
			typ := strings.Repeat("t", nameLen) + "@1"
			data, err := cas.EncodeEnvelope("json", typ, []byte("payload"))
			if err != nil {
				t.Fatalf("EncodeEnvelope: %v", err)
			}
			d := sha256.Of(data)

			// A record the reader refuses must not survive a Put either: the
			// write rewrites what it cannot read.
			writeRecord(t, base, d, strings.Repeat("x", sidecar.DefaultMaxRecordBytes+1))

			if err := rec.Put(ctx, d, bytes.NewReader(data)); err != nil {
				t.Fatalf("Put with a %d-byte type name = %v, want a readable record", nameLen, err)
			}
			info, err := os.Stat(recordPath(base, d))
			if err != nil {
				t.Fatalf("record file: %v", err)
			}
			if info.Size() > sidecar.DefaultMaxRecordBytes {
				t.Fatalf("record is %d bytes, over the %d-byte read cap", info.Size(), sidecar.DefaultMaxRecordBytes)
			}
			if _, err := rec.Load(ctx, d); err != nil {
				t.Fatalf("Load of the record Put wrote = %v", err)
			}
			if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
				t.Fatalf("Verify of the record Put wrote = %v", err)
			}
			keys, err := rec.Keys(ctx)
			if err != nil {
				t.Fatalf("Keys: %v", err)
			}
			if !test.ContainsDigest(keys, d) {
				t.Errorf("Keys = %v, want the record Put wrote for %s", keys, d)
			}
		})
	}
}

func TestRecordWriteRefusesACapTooSmallForTheRecord(t *testing.T) {
	// A cap that cannot hold even the record's required fields is the one case
	// the writer cannot satisfy: the write fails with a named error and publishes
	// nothing, rather than writing a file its own reader would refuse
	// (go-cask#362).
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := mustSidecar(t, backend,
		sidecar.WithChecksum(crc32.Name, crc32.New()),
		sidecar.WithMaxRecordBytes(16))
	data := []byte("a cap no record fits in")
	d := sha256.Of(data)

	err := rec.Put(ctx, d, bytes.NewReader(data))
	if !errors.Is(err, sidecar.ErrRecordTooLarge) {
		t.Fatalf("Put under a 16-byte cap = %v, want sidecar.ErrRecordTooLarge", err)
	}
	if _, err := os.Stat(recordPath(base, d)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused record write left a record behind: %v", err)
	}
	// The object is what Put stores first; only the derived record is refused.
	exists, err := rec.Exists(ctx, d)
	if err != nil || !exists {
		t.Errorf("Exists after a refused record write = %v, %v; want true, nil", exists, err)
	}
	if _, err := rec.Load(ctx, d); !errors.Is(err, sidecar.ErrUnrecorded) {
		t.Errorf("Load after a refused record write = %v, want sidecar.ErrUnrecorded", err)
	}
	// Nothing was created in the record directory, not even write scratch.
	if _, err := os.ReadDir(filepath.Join(base, metaDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the record directory exists after a refused write: %v", err)
	}
}

func TestForeignRecordNamesAreSkippedAndReported(t *testing.T) {
	// A `.json` name in the record directory that is not a digest is not a
	// record: it must not abort the enumeration, and reconciliation reports it
	// instead of deleting a file this layer cannot interpret (go-cask#362).
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	recorded := put(t, rec, []byte("recorded object"))
	foreign := filepath.Join(base, metaDir, "notes.json")
	if err := os.WriteFile(foreign, []byte("not a record"), 0o644); err != nil {
		t.Fatal(err)
	}

	keys, err := rec.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys with a foreign .json name = %v, want the enumeration to skip it", err)
	}
	if len(keys) != 1 || !keys[0].Equal(recorded) {
		t.Errorf("Keys = %v, want only %s", keys, recorded)
	}

	report, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile with a foreign .json name = %v, want the pass to continue", err)
	}
	if report.Records != 1 {
		t.Errorf("Records = %d, want 1", report.Records)
	}
	if len(report.Foreign) != 1 || report.Foreign[0] != "notes.json" {
		t.Errorf("Foreign = %v, want [notes.json]", report.Foreign)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("Reconcile removed a foreign file: %v", err)
	}
}

func TestDamagedAndOversizedRecordsAreCorrupt(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)

	cases := []struct {
		name string
		body string
	}{
		{"not json", "{ not json"},
		{"wrong version", `{"version":9,"digest":"aabb","checksum_algo":"crc32","checksum":"00112233","size":1,"created_at":"2026-01-01T00:00:00Z"}`},
		{"no checksum", `{"version":1,"digest":"aabb","checksum_algo":"crc32","size":1,"created_at":"2026-01-01T00:00:00Z"}`},
		{"no algorithm", `{"version":1,"digest":"aabb","checksum":"00112233","size":1,"created_at":"2026-01-01T00:00:00Z"}`},
		{"no digest", `{"version":1,"checksum_algo":"crc32","checksum":"00112233","size":1,"created_at":"2026-01-01T00:00:00Z"}`},
		{"negative size", `{"version":1,"digest":"aabb","checksum_algo":"crc32","checksum":"00112233","size":-1,"created_at":"2026-01-01T00:00:00Z"}`},
		{"no time", `{"version":1,"digest":"aabb","checksum_algo":"crc32","checksum":"00112233","size":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := sha256.Of([]byte(tc.name))
			writeRecord(t, base, d, tc.body)
			if _, err := rec.Load(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
				t.Fatalf("Load = %v, want cas.ErrCorrupt", err)
			}
			if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
				t.Fatalf("Verify = %v, want cas.ErrCorrupt", err)
			}
		})
	}
}

func TestRecordNamingAnotherDigestIsCorrupt(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := sha256.Of([]byte("the object this record should describe"))
	other := sha256.Of([]byte("some other object"))
	body := `{"version":1,"digest":"` + other.String() + `","checksum_algo":"crc32","checksum":"00112233","size":1,"created_at":"2026-01-01T00:00:00Z"}`
	writeRecord(t, base, d, body)
	if _, err := rec.Load(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Load with a mismatched digest = %v, want cas.ErrCorrupt", err)
	}
}

func TestMaxRecordBytesCapsReads(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := mustSidecar(t, backend,
		sidecar.WithChecksum(crc32.Name, crc32.New()),
		sidecar.WithMaxRecordBytes(64))
	d := sha256.Of([]byte("oversized record"))
	body := `{"version":1,"digest":"` + d.String() + `","checksum_algo":"crc32","checksum":"00112233","size":1,"created_at":"2026-01-01T00:00:00Z"}`
	if len(body) <= 64 {
		t.Fatalf("test record is only %d bytes; the cap would not be exercised", len(body))
	}
	writeRecord(t, base, d, body)
	if _, err := rec.Load(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Load = %v, want cas.ErrCorrupt", err)
	}
}

func TestKeysListsSortedRecordedDigests(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, backend)
	first := put(t, rec, []byte("one"))
	second := put(t, rec, []byte("two"))
	put(t, backend, []byte("no record"))

	if err := os.WriteFile(filepath.Join(base, metaDir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := rec.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("Keys = %v, want the two recorded digests", keys)
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1].String() >= keys[i].String() {
			t.Fatalf("Keys not sorted: %v", keys)
		}
	}
	if !test.ContainsDigest(keys, first) || !test.ContainsDigest(keys, second) {
		t.Errorf("Keys = %v, want %s and %s", keys, first, second)
	}

	if err := os.WriteFile(filepath.Join(base, metaDir, "notahex.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A `.json` name that is not a digest is not a record, so Keys skips it
	// rather than refusing the whole enumeration (go-cask#362; Reconcile reports
	// it).
	keys, err = rec.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys with a foreign .json file = %v, want it skipped", err)
	}
	if len(keys) != 2 {
		t.Fatalf("Keys = %v, want the two recorded digests", keys)
	}
}

func TestReadOnlyDecoratorVerifiesExistingRecords(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	data := []byte("written with a checksum, read without one")
	d := put(t, crc32Recorder(t, backend), data)

	readOnly := mustSidecar(t, backend)
	if _, err := readOnly.Load(ctx, d); err != nil {
		t.Fatalf("Load through a read-only decorator: %v", err)
	}
	if err := readOnly.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify through a read-only decorator: %v", err)
	}
	// Without a checksum there is nothing to record, so Put stays a pure
	// delegation.
	fresh := put(t, readOnly, []byte("unrecorded by a read-only decorator"))
	if _, err := readOnly.Load(ctx, fresh); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("read-only Put wrote a record: %v", err)
	}
}

func TestNewRefusesUnresolvableOrMismatchedBase(t *testing.T) {
	base := t.TempDir()
	backend, err := fsbackend.New(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sidecar.New(backmem.New()); err == nil {
		t.Error("New with a base-less backend and no WithBase succeeded, want an error")
	}
	if _, err := sidecar.New(backend, sidecar.WithBase(filepath.Join(base, "elsewhere"))); err == nil {
		t.Error("New accepted a base that disagrees with the backend's BasePath")
	}
	if _, err := sidecar.New(backend, sidecar.WithBase(base)); err != nil {
		t.Errorf("New with the backend's own base = %v, want nil", err)
	}
	if _, err := sidecar.New(backend, sidecar.WithChecksum(crc32.Name, nil)); err == nil {
		t.Error("New accepted an algorithm name without a hasher")
	}
	if _, err := sidecar.New(backend, sidecar.WithChecksum("", crc32.New())); err == nil {
		t.Error("New accepted a hasher without an algorithm name")
	}
	rec := mustSidecar(t, backend)
	if rec.BasePath() != base {
		t.Errorf("BasePath = %q, want %q", rec.BasePath(), base)
	}
	if _, err := sidecar.New(nil); err == nil {
		t.Error("New(nil) succeeded, want an error")
	}
}

func TestPackedStoreRecordsBesideTheLooseTree(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	backend, err := packfs.New(base, packfs.WithEnabled())
	if err != nil {
		t.Fatalf("packfs.New: %v", err)
	}
	// The packfile backend holds its active pack open, which Windows refuses to
	// unlink at temp-directory cleanup.
	t.Cleanup(func() { _ = backend.Close() })
	rec := crc32Recorder(t, backend)
	data := []byte("packed object")
	d := put(t, rec, data)

	// packfs reports the loose tree as its byte root, because that is the
	// directory its List/Stats/Clean operate on.
	want := filepath.Join(base, "loose")
	if rec.BasePath() != want {
		t.Errorf("BasePath = %q, want %q", rec.BasePath(), want)
	}
	if _, err := os.Stat(filepath.Join(want, metaDir, d.String()+".json")); err != nil {
		t.Errorf("record file: %v", err)
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify over packfs: %v", err)
	}
	digests, err := backend.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(digests) != 1 || !digests[0].Equal(d) {
		t.Errorf("List = %v, want [%s]", digests, d)
	}
	stats, err := backend.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.ObjectCount != 1 || stats.TotalSize != int64(len(data)) {
		t.Errorf("Stats = %d objects / %d bytes, want 1 / %d", stats.ObjectCount, stats.TotalSize, len(data))
	}

	// packfs.Clean delegates to the loose backend, so it reclaims a record
	// write that crashed before its rename.
	scratch := filepath.Join(want, metaDir, d.String()+".9.tmp")
	if err := os.WriteFile(scratch, []byte("half a record"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Clean(ctx, 0); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(scratch); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("packfs.Clean left a record scratch file behind: %v", err)
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Errorf("Verify after Clean: %v", err)
	}
}

func TestPartialReadWritesNoRecord(t *testing.T) {
	ctx := context.Background()
	backend, base := mustFS(t)
	rec := crc32Recorder(t, &partialPutBackend{baseReporting: baseReporting{Backend: backend, base: base}, limit: 4})
	data := []byte("this write will be cut short")
	d := sha256.Of(data)
	err := rec.Put(ctx, d, bytes.NewReader(data))
	if err == nil {
		t.Fatal("Put succeeded although the backend read only part of the object")
	}
	if !strings.Contains(err.Error(), "EOF") {
		t.Errorf("Put error = %v, want it to name the truncated read", err)
	}
	if _, err := os.Stat(recordPath(base, d)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a record was written for a partial read: %v", err)
	}
}

func TestInnerFailuresAreReported(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)

	if err := rec.Put(ctx, cas.Digest{}, bytes.NewReader(nil)); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Errorf("Put with an absent digest = %v, want cas.ErrInvalidDigest", err)
	}
	if err := rec.Put(ctx, sha256.Of([]byte("x")), nil); err == nil {
		t.Error("Put with a nil reader succeeded, want an error")
	}

	// A failing Get after a successful inner Put is loud: the record cannot be
	// computed from bytes nobody can read.
	base := rec.BasePath()
	d := sha256.Of([]byte("get fails"))
	broken := crc32Recorder(t, &getErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})
	if err := broken.Put(ctx, d, bytes.NewReader([]byte("get fails"))); !errors.Is(err, errGetFailed) {
		t.Errorf("Put with a failing Get = %v, want %v", err, errGetFailed)
	}
	if _, err := broken.Load(ctx, d); !errors.Is(err, cas.ErrNotFound) {
		t.Errorf("Load after a failed record write = %v, want cas.ErrNotFound", err)
	}

	// A failing Close is reported too.
	closing := crc32Recorder(t, &closeErrorBackend{baseReporting: baseReporting{Backend: backend, base: base}})
	d2 := sha256.Of([]byte("close fails"))
	if err := closing.Put(ctx, d2, bytes.NewReader([]byte("close fails"))); !errors.Is(err, errCloseFailed) {
		t.Errorf("Put with a failing Close = %v, want %v", err, errCloseFailed)
	}
}

func TestContextCancellation(t *testing.T) {
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("cancelled"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	verifier := rec.Verifier(crc32.Name, crc32.New())
	_, errGet := rec.Get(ctx, d)
	_, errExists := rec.Exists(ctx, d)
	errDelete := rec.Delete(ctx, d)
	_, errList := rec.List(ctx)
	_, errStats := rec.Stats(ctx)
	_, errLoad := rec.Load(ctx, d)
	_, errKeys := rec.Keys(ctx)
	_, errReconcile := rec.Reconcile(ctx)
	errVerify := verifier.Verify(ctx, d)
	_, errVerifyAll := verifier.VerifyAll(ctx)
	errPut := rec.Put(ctx, d, bytes.NewReader(nil))
	cases := []struct {
		name string
		err  error
	}{
		{"Get", errGet},
		{"Exists", errExists},
		{"Delete", errDelete},
		{"List", errList},
		{"Stats", errStats},
		{"Load", errLoad},
		{"Keys", errKeys},
		{"Reconcile", errReconcile},
		{"Verify", errVerify},
		{"VerifyAll", errVerifyAll},
		{"Put", errPut},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, context.Canceled) {
			t.Errorf("%s with a cancelled context = %v, want context.Canceled", tc.name, tc.err)
		}
	}
}

func TestStatsAndExistsDelegate(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	d := put(t, rec, []byte("delegated"))

	exists, err := rec.Exists(ctx, d)
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true, nil", exists, err)
	}
	rc, err := rec.Get(ctx, d)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "delegated" {
		t.Errorf("Get returned %q, want %q", got, "delegated")
	}
	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	// The record's JSON is a plain object an operator can read.
	var decoded map[string]json.RawMessage
	raw, err := os.ReadFile(recordPath(rec.BasePath(), d))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	for _, field := range []string{"version", "digest", "checksum_algo", "checksum", "size", "created_at"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("record JSON has no %q field", field)
		}
	}
	if record.ChecksumAlgo != crc32.Name {
		t.Errorf("ChecksumAlgo = %q, want %q", record.ChecksumAlgo, crc32.Name)
	}
}

func TestCrc64RecordsWiderChecksum(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := mustSidecar(t, backend, sidecar.WithChecksum(crc64.Name, crc64.New()))
	data := []byte("wide checksum")
	d := put(t, rec, data)

	record, err := rec.Load(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Checksum.Equal(crc64.Of(data)) {
		t.Errorf("Checksum = %s, want %s", record.Checksum, crc64.Of(data))
	}
	if err := rec.Verifier(crc64.Name, crc64.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); !errors.Is(err, sidecar.ErrChecksumAlgorithm) {
		t.Errorf("Verify with crc32 = %v, want ErrChecksumAlgorithm", err)
	}
}

func TestDirSyncRecordsSurvive(t *testing.T) {
	ctx := context.Background()
	backend, _ := mustFS(t)
	rec := mustSidecar(t, backend,
		sidecar.WithChecksum(crc32.Name, crc32.New()),
		sidecar.WithDirSync())
	d := put(t, rec, []byte("durable record"))
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifierRejectsBadArguments(t *testing.T) {
	backend, _ := mustFS(t)
	rec := crc32Recorder(t, backend)
	ctx := context.Background()

	if err := rec.Verifier(crc32.Name, nil).Verify(ctx, sha256.Of([]byte("x"))); err == nil {
		t.Error("Verify with a nil hasher succeeded, want an error")
	}
	if err := rec.Verifier("", crc32.New()).Verify(ctx, sha256.Of([]byte("x"))); err == nil {
		t.Error("Verify with no algorithm succeeded, want an error")
	}
	if err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, cas.Digest{}); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Error("Verify with an absent digest did not report ErrInvalidDigest")
	}
	if _, err := rec.Verifier(crc32.Name, nil).VerifyAll(ctx); err == nil {
		t.Error("VerifyAll with a nil hasher succeeded, want an error")
	}
}

var (
	errGetFailed   = errors.New("get failed")
	errCloseFailed = errors.New("close failed")
)

// baseReporting wraps a backend for a test while still reporting the base path,
// the way a decorator above fs would: the sidecar resolves its record directory
// from BasePath and refuses a WithBase that disagrees with it.
type baseReporting struct {
	cas.Backend
	base string
}

func (b baseReporting) BasePath() string { return b.base }

// partialPutBackend reads only limit bytes of whatever it is asked to store,
// which is the shape of a backend that returns without draining its reader.
type partialPutBackend struct {
	baseReporting
	limit int
}

func (b *partialPutBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	buf := make([]byte, b.limit)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	return b.Backend.Put(ctx, d, bytes.NewReader(buf[:n]))
}

// getErrorBackend fails every read, so a record write cannot complete.
type getErrorBackend struct {
	baseReporting
}

func (b *getErrorBackend) Get(context.Context, cas.Digest) (io.ReadCloser, error) {
	return nil, errGetFailed
}

// closeErrorBackend returns a reader whose Close fails, so the record path
// checks the one error a caller cannot see from the bytes alone.
type closeErrorBackend struct {
	baseReporting
}

func (b *closeErrorBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	rc, err := b.Backend.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	return closeErrorReader{ReadCloser: rc}, nil
}

type closeErrorReader struct {
	io.ReadCloser
}

// Close closes the underlying reader and then reports the failure, the way a
// reader whose close genuinely fails behaves: leaking the handle would fail the
// temp-directory cleanup on Windows instead of exercising the error path.
func (r closeErrorReader) Close() error {
	_ = r.ReadCloser.Close()
	return errCloseFailed
}
