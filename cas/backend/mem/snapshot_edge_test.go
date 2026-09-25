package memory

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas/hash/sha256"
)

// writeFailure is a writer that rejects its nth Write call. The snapshot format
// is a header followed by one lengths/digest/payload write per object, so the
// call number selects which of Snapshot's write branches fails.
type writeFailure struct {
	failAt int
	err    error
	calls  int
}

func (w *writeFailure) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

// cancelOnWrite cancels the context while accepting the first write, so the
// snapshot's per-record context check — not an already-canceled entry check —
// is what stops the loop.
type cancelOnWrite struct {
	cancel   context.CancelFunc
	buf      bytes.Buffer
	canceled bool
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	if !w.canceled {
		w.canceled = true
		w.cancel()
	}
	return w.buf.Write(p)
}

// TestMemoryBackendSnapshotSkipsAStrayEmptyKey pins the guard Snapshot's key
// scan keeps: the absent digest can never be a key through Put or Restore
// (cas.CheckDigest rejects it), so the entry is injected directly, exactly as
// the corruption-recovery test installs tampered bytes. The header must describe
// only the real objects — count and total bytes included — and the archive must
// restore without the stray key.
func TestMemoryBackendSnapshotSkipsAStrayEmptyKey(t *testing.T) {
	ctx := context.Background()
	source := New()
	payload := []byte("real payload")
	d := sha256.Of(payload)
	if err := source.Put(ctx, d, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	source.objects[""] = []byte("stray bytes under the absent digest")
	source.mu.Unlock()

	var snapshot bytes.Buffer
	if err := source.Snapshot(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}
	raw := snapshot.Bytes()
	if got := binary.BigEndian.Uint64(raw[10:18]); got != 1 {
		t.Fatalf("snapshot object count = %d, want 1 (the stray key must not be written)", got)
	}
	if got := binary.BigEndian.Uint64(raw[18:26]); got != uint64(len(payload)) {
		t.Fatalf("snapshot declared total = %d, want %d", got, len(payload))
	}
	if want := snapshotHeaderSize + 16 + len(d) + len(payload); len(raw) != want {
		t.Fatalf("snapshot length = %d, want %d (header, one record, no stray entry)", len(raw), want)
	}

	restored := New()
	if err := restored.Restore(ctx, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	list, err := restored.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Equal(d) {
		t.Fatalf("restored List() = %v, want exactly [%s]", list, d)
	}
}

// TestMemoryBackendSnapshotStopsOnMidWriteCancellation pins the per-record
// context check: a context canceled after the header was written stops the
// snapshot before the first record, reports the cancellation unwrapped, and
// leaves the writer holding nothing but the header.
func TestMemoryBackendSnapshotStopsOnMidWriteCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := New()
	for _, payload := range []string{"first", "second"} {
		if err := source.Put(ctx, sha256.Of([]byte(payload)), strings.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
	}

	w := &cancelOnWrite{cancel: cancel}
	err := source.Snapshot(ctx, w)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot with a mid-write cancellation = %v, want context.Canceled", err)
	}
	if w.buf.Len() != snapshotHeaderSize {
		t.Fatalf("canceled Snapshot wrote %d bytes, want only the %d-byte header", w.buf.Len(), snapshotHeaderSize)
	}
}

// TestMemoryBackendSnapshotReportsRecordWriteFailures pins each of the three
// per-record write branches: the failing write is named in the wrapped error
// (so an operator can tell a header failure from a payload failure) and the
// writer's own error is preserved for errors.Is.
func TestMemoryBackendSnapshotReportsRecordWriteFailures(t *testing.T) {
	ctx := context.Background()
	source := New()
	for _, payload := range []string{"first", "second"} {
		if err := source.Put(ctx, sha256.Of([]byte(payload)), strings.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
	}

	writeErr := errors.New("snapshot writer failed")
	for _, tc := range []struct {
		name   string
		failAt int
		want   string
	}{
		{"record lengths", 2, "mem: write snapshot record lengths"},
		{"record digest", 3, "mem: write snapshot digest"},
		{"record payload", 4, "mem: write snapshot payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &writeFailure{failAt: tc.failAt, err: writeErr}
			err := source.Snapshot(ctx, w)
			if !errors.Is(err, writeErr) {
				t.Fatalf("Snapshot() = %v, want the writer's error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Snapshot() = %v, want the %q branch named", err, tc.want)
			}
			if w.calls != tc.failAt {
				t.Fatalf("Snapshot made %d writes, want %d (it must stop at the failure)", w.calls, tc.failAt)
			}
		})
	}
}

// cancelDuringRead cancels the context once its first Read has served the whole
// header, so Restore's per-record check is what reports the cancellation.
type cancelDuringRead struct {
	data     []byte
	cancel   context.CancelFunc
	canceled bool
}

func (r *cancelDuringRead) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if !r.canceled {
		r.canceled = true
		r.cancel()
	}
	return n, nil
}

// TestMemoryBackendRestoreStopsOnMidRecordCancellation pins Restore's per-record
// context check: the context is canceled after the header (and its validation)
// has been read, so the loop itself reports the cancellation, the target backend
// keeps its previous contents, and nothing is assigned from a partial archive.
func TestMemoryBackendRestoreStopsOnMidRecordCancellation(t *testing.T) {
	ctx := context.Background()
	source := New()
	payload := []byte("existing")
	d := sha256.Of(payload)
	if err := source.Put(ctx, d, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := source.Snapshot(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	target := New()
	reader := &cancelDuringRead{data: slices.Clone(snapshot.Bytes()), cancel: cancel}
	if err := target.Restore(cctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("Restore with a mid-record cancellation = %v, want context.Canceled", err)
	}
	list, err := target.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("a canceled Restore left %v in the target", list)
	}
}

// snapshotWithRecords builds the fixed header of a two-record snapshot followed
// by the raw record bytes the caller supplies, without going through Snapshot:
// the malformed shapes below cannot be produced by a working writer.
func snapshotWithRecords(count, declaredTotal uint64, records ...[]byte) []byte {
	raw := make([]byte, snapshotHeaderSize)
	copy(raw[0:8], snapshotMagic[:])
	binary.BigEndian.PutUint16(raw[8:10], snapshotVersion)
	binary.BigEndian.PutUint64(raw[10:18], count)
	binary.BigEndian.PutUint64(raw[18:26], declaredTotal)
	for _, record := range records {
		raw = append(raw, record...)
	}
	return raw
}

// recordLayout renders one record's 16-byte lengths header.
func recordLayout(digestSize, payloadSize uint64) []byte {
	lengths := make([]byte, 16)
	binary.BigEndian.PutUint64(lengths[:8], digestSize)
	binary.BigEndian.PutUint64(lengths[8:], payloadSize)
	return lengths
}

// TestMemoryBackendRestoreRejectsSnapshotSizeOverflow pins the overflow guard: a
// second record whose declared payload cannot be added to the running total is
// rejected as an overflow, not as a total mismatch and not by wrapping the
// arithmetic. The declared totals are otherwise legal (both are at most
// math.MaxInt), so the overflow check is the only thing standing between the
// archive and a wrapped byte count.
func TestMemoryBackendRestoreRejectsSnapshotSizeOverflow(t *testing.T) {
	ctx := context.Background()
	digest := bytes.Repeat([]byte{0xab}, 32)
	first := append(recordLayout(uint64(len(digest)), 1), digest...)
	first = append(first, 0x01)
	second := recordLayout(uint64(len(digest)), uint64(math.MaxInt))
	raw := snapshotWithRecords(2, 1, first, second)

	err := New().Restore(ctx, bytes.NewReader(raw))
	if err == nil {
		t.Fatal("Restore with an overflowing payload total = nil, want an error")
	}
	if !strings.Contains(err.Error(), "snapshot size overflows") {
		t.Fatalf("Restore = %v, want the overflow branch to report it", err)
	}
}

// TestMemoryBackendRestoreRejectsTruncatedRecordDigest pins the digest read
// branch: a record header that declares a digest longer than the bytes that
// follow is a truncated archive, reported with the digest read named rather than
// as a short payload or a total mismatch.
func TestMemoryBackendRestoreRejectsTruncatedRecordDigest(t *testing.T) {
	ctx := context.Background()
	truncated := append(recordLayout(32, 0), bytes.Repeat([]byte{0xab}, 10)...)
	raw := snapshotWithRecords(1, 0, truncated)

	err := New().Restore(ctx, bytes.NewReader(raw))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Restore with a truncated digest = %v, want io.ErrUnexpectedEOF", err)
	}
	if !strings.Contains(err.Error(), "mem: read snapshot digest") {
		t.Fatalf("Restore = %v, want the digest read named", err)
	}
}

// stallThenEOF serves data and then reports no progress and no error. That is
// legal but discouraged for an io.Reader, and it is exactly what the trailing
// check must not mistake for a clean end of stream: the archive is complete, and
// bytes the reader cannot prove absent are still trailing data.
type stallThenEOF struct{ data []byte }

func (r *stallThenEOF) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, nil
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// TestMemoryBackendRestoreRejectsAReaderThatStallsWithoutError pins the
// no-progress branch of the trailing check: a reader that returns (0, nil)
// instead of io.EOF must be reported as trailing data — never mistaken for a
// clean end (the archive's own length already accounted for every byte) and
// never spun on.
func TestMemoryBackendRestoreRejectsAReaderThatStallsWithoutError(t *testing.T) {
	ctx := context.Background()
	source := New()
	payload := []byte("value")
	if err := source.Put(ctx, sha256.Of(payload), bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := source.Snapshot(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}

	raw := slices.Clone(snapshot.Bytes())
	err := New().Restore(ctx, &stallThenEOF{data: raw})
	if err == nil {
		t.Fatal("Restore from a reader that reports no progress = nil, want trailing data reported")
	}
	if !strings.Contains(err.Error(), "mem: snapshot trailing data") {
		t.Fatalf("Restore = %v, want the trailing-data branch to report it", err)
	}
}

// tailError serves data and then replaces io.EOF with a real failure, so the
// trailing check sees a broken reader rather than a clean end of stream.
type tailError struct {
	data []byte
	err  error
}

func (r *tailError) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// TestMemoryBackendRestoreReportsATrailingReadFailure pins the other half of the
// trailing check: a reader that fails after the last record is a broken stream,
// so the failure is reported (wrapped, so errors.Is still finds it) instead of
// being read as end of stream or silently accepted as trailing data.
func TestMemoryBackendRestoreReportsATrailingReadFailure(t *testing.T) {
	ctx := context.Background()
	source := New()
	payload := []byte("value")
	if err := source.Put(ctx, sha256.Of(payload), bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := source.Snapshot(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}

	readErr := errors.New("snapshot source failed")
	raw := slices.Clone(snapshot.Bytes())
	err := New().Restore(ctx, &tailError{data: raw, err: readErr})
	if !errors.Is(err, readErr) {
		t.Fatalf("Restore with a failing trailing read = %v, want the reader's error", err)
	}
	if !strings.Contains(err.Error(), "mem: check snapshot trailing data") {
		t.Fatalf("Restore = %v, want the trailing check named", err)
	}
}

// Branches in this package's snapshot code that no deterministic test reaches,
// with the reason recorded (testing-strategy.md §5):
//
//   - Restore's per-record `total > m.maxBytes` check: the same limit is already
//     enforced on the archive's declared total before the loop starts, and every
//     record is rejected as soon as its running total exceeds that declared
//     total. Since declaredTotal <= maxBytes and total <= declaredTotal, the
//     per-record check can never be the first to fire; maxBytes cannot change
//     while a Restore runs.
//
//   - Restore's "snapshot contains an empty digest" check: a record with
//     digestSize == 0 is rejected earlier as an invalid digest size, and
//     cas.NewDigest only returns an absent digest for an empty slice
//     (Digest.IsZero is len(d) == 0), so the decoded digest is never absent.
//     The check is defence in depth against a future reader that builds digests
//     differently.
