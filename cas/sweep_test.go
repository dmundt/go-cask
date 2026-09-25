package cas_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// statMemBackend wraps backmem.Backend and adds a Statter implementation backed
// by caller-assigned timestamps, so age-based Sweep can be exercised
// deterministically without a real filesystem clock.
type statMemBackend struct {
	*backmem.Backend
	mu       sync.Mutex
	modTimes map[string]time.Time
}

func newStatMemBackend() *statMemBackend {
	return &statMemBackend{Backend: backmem.New(), modTimes: make(map[string]time.Time)}
}

func (b *statMemBackend) putAt(ctx context.Context, d cas.Digest, data []byte, at time.Time) error {
	if err := b.Put(ctx, d, bytes.NewReader(data)); err != nil {
		return err
	}
	b.mu.Lock()
	b.modTimes[d.String()] = at
	b.mu.Unlock()
	return nil
}

func (b *statMemBackend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	rc, err := b.Get(ctx, d)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	return n, err
}

func (b *statMemBackend) ModTime(ctx context.Context, d cas.Digest) (time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	mt, ok := b.modTimes[d.String()]
	if !ok {
		return time.Time{}, cas.ErrNotFound
	}
	return mt, nil
}

func TestSweepUnconditionalOverMinimalBackend(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New() // implements neither Cleaner nor Statter
	live := sha256.Of([]byte("live"))
	dead := sha256.Of([]byte("dead"))
	if err := backend.Put(ctx, live, bytes.NewReader([]byte("live"))); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(ctx, dead, bytes.NewReader([]byte("dead"))); err != nil {
		t.Fatal(err)
	}

	reachable := map[string]bool{live.String(): true}
	doomed, err := cas.Sweep(ctx, backend, reachable, cas.SweepOptions{})
	if err != nil {
		t.Fatalf("Sweep() = %v, want nil", err)
	}
	if len(doomed) != 1 || !doomed[0].Equal(dead) {
		t.Fatalf("Sweep() doomed = %v, want [%s]", doomed, dead)
	}
	if ok, _ := backend.Exists(ctx, dead); ok {
		t.Fatal("dead object still present after Sweep")
	}
	if ok, _ := backend.Exists(ctx, live); !ok {
		t.Fatal("live object removed by Sweep")
	}
}

func TestSweepDryRunDoesNotDelete(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	dead := sha256.Of([]byte("dead"))
	if err := backend.Put(ctx, dead, bytes.NewReader([]byte("dead"))); err != nil {
		t.Fatal(err)
	}
	doomed, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 1 {
		t.Fatalf("Sweep(dryRun) doomed = %v, want 1 entry", doomed)
	}
	if ok, _ := backend.Exists(ctx, dead); !ok {
		t.Fatal("Sweep(dryRun) deleted an object")
	}
}

func TestSweepRejectsAgeWithoutStatter(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{MinAge: time.Hour}); !errors.Is(err, cas.ErrUnsupported) {
		t.Fatalf("Sweep(MinAge, non-Statter) = %v, want ErrUnsupported", err)
	}
}

func TestSweepAgeBasedRetentionOverStatter(t *testing.T) {
	ctx := context.Background()
	backend := newStatMemBackend()
	old := sha256.Of([]byte("old"))
	fresh := sha256.Of([]byte("fresh"))
	if err := backend.putAt(ctx, old, []byte("old"), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := backend.putAt(ctx, fresh, []byte("fresh"), time.Now()); err != nil {
		t.Fatal(err)
	}

	doomed, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{MinAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(doomed) != 1 || !doomed[0].Equal(old) {
		t.Fatalf("Sweep(MinAge) doomed = %v, want [%s]", doomed, old)
	}
	if ok, _ := backend.Exists(ctx, fresh); !ok {
		t.Fatal("fresh object removed by age-based Sweep")
	}
}

func TestSweepRejectsNilBackend(t *testing.T) {
	if _, err := cas.Sweep(context.Background(), nil, nil, cas.SweepOptions{}); err == nil {
		t.Fatal("Sweep(nil backend) = nil, want error")
	}
}

func TestSweepRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := backmem.New()
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sweep(canceled ctx) = %v, want context.Canceled", err)
	}
}

// deleteErrorBackend wraps backmem.Backend and fails every Delete, so Sweep's
// delete-propagation path can be exercised deterministically.
type deleteErrorBackend struct {
	*backmem.Backend
	err error
}

func (b deleteErrorBackend) Delete(context.Context, cas.Digest) error { return b.err }

func TestSweepPropagatesDeleteError(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	dead := sha256.Of([]byte("dead"))
	if err := inner.Put(ctx, dead, bytes.NewReader([]byte("dead"))); err != nil {
		t.Fatal(err)
	}
	want := errors.New("delete failed")
	backend := deleteErrorBackend{Backend: inner, err: want}
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{}); !errors.Is(err, want) {
		t.Fatalf("Sweep(delete error) = %v, want %v", err, want)
	}
}

// TestSweepSkipsUnaddressableDigestName is the regression guard for #258: an fs
// store with a wider fan-out reports any lowercase-hex file name from List,
// including one too short for its layout. The portable sweep must skip it the
// way fs.GC/Prune do; it used to hand it to Delete, whose ErrInvalidDigest
// aborted the sweep after part of the store had already been reclaimed.
func TestSweepSkipsUnaddressableDigestName(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	// A fan-out of 4 needs four hex characters, while the stray file below has
	// two — the default layout would address it, which is why this needs a
	// non-default one.
	backend, err := fsbackend.New(base, fsbackend.WithFanOut(4))
	if err != nil {
		t.Fatal(err)
	}
	kept := sha256.Of([]byte("kept"))
	dead := sha256.Of([]byte("dead"))
	for _, d := range []cas.Digest{kept, dead} {
		if err := backend.Put(ctx, d, bytes.NewReader([]byte(d.String()))); err != nil {
			t.Fatal(err)
		}
	}
	stray := filepath.Join(base, "ab")
	if err := os.WriteFile(stray, []byte("not an object"), 0o644); err != nil {
		t.Fatal(err)
	}
	reachable := map[string]bool{kept.String(): true}

	// A dry run reports exactly what a real run reclaims — the stray file is not
	// one of them — and the real run then applies it.
	for _, opts := range []cas.SweepOptions{{DryRun: true}, {}} {
		doomed, err := cas.Sweep(ctx, backend, reachable, opts)
		if err != nil {
			t.Fatalf("Sweep(%+v) = %v, want nil: a stray digest-named file must not abort the sweep", opts, err)
		}
		if len(doomed) != 1 || !doomed[0].Equal(dead) {
			t.Fatalf("Sweep(%+v) doomed = %v, want [%s]", opts, doomed, dead)
		}
		if _, err := os.Stat(stray); err != nil {
			t.Fatalf("Sweep(%+v) touched the file its layout cannot address: %v", opts, err)
		}
	}

	// The age path takes the same route (Exists is asked before ModTime).
	if doomed, err := cas.Sweep(ctx, backend, reachable, cas.SweepOptions{MinAge: time.Hour}); err != nil || len(doomed) != 0 {
		t.Fatalf("Sweep(min-age) = (%v, %v), want no error and no doomed objects", doomed, err)
	}

	if ok, err := backend.Exists(ctx, dead); err != nil || ok {
		t.Fatalf("Exists(dead) = (%v, %v), want false after the sweep", ok, err)
	}
	if ok, err := backend.Exists(ctx, kept); err != nil || !ok {
		t.Fatalf("Exists(kept) = (%v, %v), want true", ok, err)
	}
	if content, err := os.ReadFile(stray); err != nil || string(content) != "not an object" {
		t.Fatalf("stray file = (%q, %v), want it left untouched", content, err)
	}
}

func TestSweepSkipsConcurrentlyDeletedDuringAgeCheck(t *testing.T) {
	ctx := context.Background()
	backend := newStatMemBackend()
	// Put directly on the underlying backmem.Backend so List reports the digest,
	// but never register a mod time for it: ModTime then reports
	// cas.ErrNotFound, simulating a concurrent delete racing the age check.
	gone := sha256.Of([]byte("gone"))
	if err := backend.Backend.Put(ctx, gone, bytes.NewReader([]byte("gone"))); err != nil {
		t.Fatal(err)
	}
	doomed, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{MinAge: time.Hour})
	if err != nil {
		t.Fatalf("Sweep() = %v, want nil", err)
	}
	if len(doomed) != 0 {
		t.Fatalf("Sweep() doomed = %v, want none (ModTime ErrNotFound is skipped, not doomed)", doomed)
	}
}

// cancelOnListBackend cancels the context while List runs, so the caller's next
// per-item context check is the one that fires — the only deterministic way to
// reach the loop's cancellation arm rather than the entry guard's.
type cancelOnListBackend struct {
	*backmem.Backend
	cancel context.CancelFunc
}

func (b cancelOnListBackend) List(ctx context.Context) ([]cas.Digest, error) {
	digests, err := b.Backend.List(ctx)
	b.cancel()
	return digests, err
}

// cancelOnExistsBackend cancels the context during the existence probe, so a
// sweep finishes marking and then stops in its delete pass.
type cancelOnExistsBackend struct {
	*backmem.Backend
	cancel context.CancelFunc
}

func (b cancelOnExistsBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	ok, err := b.Backend.Exists(ctx, d)
	b.cancel()
	return ok, err
}

// vanishedBackend reports every digest that List returned as already gone,
// standing in for a concurrent sweep that deleted it between the two calls.
type vanishedBackend struct{ *backmem.Backend }

func (vanishedBackend) Exists(context.Context, cas.Digest) (bool, error) { return false, nil }

// modTimeErrorBackend is a Statter whose ModTime always fails, so the age path's
// error arms can be selected without touching the filesystem clock. Size is
// satisfied honestly, because Statter requires both methods and only ModTime is
// under test.
type modTimeErrorBackend struct {
	*backmem.Backend
	err error
}

func (b modTimeErrorBackend) Size(ctx context.Context, d cas.Digest) (int64, error) {
	rc, err := b.Backend.Get(ctx, d)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return io.Copy(io.Discard, rc)
}

func (b modTimeErrorBackend) ModTime(context.Context, cas.Digest) (time.Time, error) {
	return time.Time{}, b.err
}

// TestSweepStopsWhenContextIsCancelledDuringTheScan pins the per-object
// cancellation check inside the marking loop.
func TestSweepStopsWhenContextIsCancelledDuringTheScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := backmem.New()
	d := sha256.Of([]byte("doomed"))
	if err := inner.Put(ctx, d, bytes.NewReader([]byte("doomed"))); err != nil {
		t.Fatal(err)
	}
	backend := cancelOnListBackend{Backend: inner, cancel: cancel}
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sweep(ctx cancelled during List) = %v, want context.Canceled", err)
	}
}

// TestSweepStopsWhenContextIsCancelledBeforeDeleting pins the second
// cancellation check: marking finished, and the delete pass must not start.
func TestSweepStopsWhenContextIsCancelledBeforeDeleting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := backmem.New()
	d := sha256.Of([]byte("doomed"))
	if err := inner.Put(ctx, d, bytes.NewReader([]byte("doomed"))); err != nil {
		t.Fatal(err)
	}
	backend := cancelOnExistsBackend{Backend: inner, cancel: cancel}
	doomed, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sweep(ctx cancelled before deleting) = %v, want context.Canceled", err)
	}
	if doomed != nil {
		t.Fatalf("Sweep(cancelled) doomed = %v, want nil alongside the error", doomed)
	}
	if ok, _ := inner.Exists(context.Background(), d); !ok {
		t.Fatal("Sweep deleted an object after its context was cancelled")
	}
}

// TestSweepReportsExistenceProbeFailure pins the arm where the backend cannot
// answer "does this object exist?" for a reason other than an unusable key.
func TestSweepReportsExistenceProbeFailure(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	d := sha256.Of([]byte("doomed"))
	if err := inner.Put(ctx, d, bytes.NewReader([]byte("doomed"))); err != nil {
		t.Fatal(err)
	}
	want := errors.New("exists exploded")
	backend := existsErrorBackend{Backend: inner, err: want}
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{}); !errors.Is(err, want) {
		t.Fatalf("Sweep(Exists error) = %v, want %v", err, want)
	}
}

// TestSweepSkipsObjectThatVanishedAfterList pins the concurrent-delete arm: the
// object was listed, then a racing sweep removed it, so this run skips it
// instead of failing and instead of counting it as reclaimed.
func TestSweepSkipsObjectThatVanishedAfterList(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	d := sha256.Of([]byte("doomed"))
	if err := inner.Put(ctx, d, bytes.NewReader([]byte("doomed"))); err != nil {
		t.Fatal(err)
	}
	doomed, err := cas.Sweep(ctx, vanishedBackend{Backend: inner}, nil, cas.SweepOptions{})
	if err != nil {
		t.Fatalf("Sweep(vanished object) = %v, want nil", err)
	}
	if len(doomed) != 0 {
		t.Fatalf("Sweep(vanished object) doomed = %v, want none", doomed)
	}
}

// TestSweepSkipsUnaddressableNameDuringAgeCheck pins the second ErrInvalidDigest
// arm: a name the layout cannot address is skipped by the age path too, not just
// by the existence probe.
func TestSweepSkipsUnaddressableNameDuringAgeCheck(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	d := sha256.Of([]byte("doomed"))
	if err := inner.Put(ctx, d, bytes.NewReader([]byte("doomed"))); err != nil {
		t.Fatal(err)
	}
	backend := modTimeErrorBackend{Backend: inner, err: cas.ErrInvalidDigest}
	doomed, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{MinAge: time.Hour})
	if err != nil {
		t.Fatalf("Sweep(ModTime ErrInvalidDigest) = %v, want nil", err)
	}
	if len(doomed) != 0 {
		t.Fatalf("Sweep(ModTime ErrInvalidDigest) doomed = %v, want none", doomed)
	}
}

// TestSweepReportsModTimeFailure pins the age path's "anything else aborts" arm.
func TestSweepReportsModTimeFailure(t *testing.T) {
	ctx := context.Background()
	inner := backmem.New()
	d := sha256.Of([]byte("doomed"))
	if err := inner.Put(ctx, d, bytes.NewReader([]byte("doomed"))); err != nil {
		t.Fatal(err)
	}
	want := errors.New("stat exploded")
	backend := modTimeErrorBackend{Backend: inner, err: want}
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{MinAge: time.Hour}); !errors.Is(err, want) {
		t.Fatalf("Sweep(ModTime error) = %v, want %v", err, want)
	}
}
