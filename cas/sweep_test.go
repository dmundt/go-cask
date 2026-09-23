package cas_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// statMemBackend wraps mem.Backend and adds a Statter implementation backed
// by caller-assigned timestamps, so age-based Sweep can be exercised
// deterministically without a real filesystem clock.
type statMemBackend struct {
	*mem.Backend
	mu       sync.Mutex
	modTimes map[string]time.Time
}

func newStatMemBackend() *statMemBackend {
	return &statMemBackend{Backend: mem.New(), modTimes: make(map[string]time.Time)}
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
	backend := mem.New() // implements neither Cleaner nor Statter
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
	backend := mem.New()
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
	backend := mem.New()
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
	backend := mem.New()
	if _, err := cas.Sweep(ctx, backend, nil, cas.SweepOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sweep(canceled ctx) = %v, want context.Canceled", err)
	}
}

// deleteErrorBackend wraps mem.Backend and fails every Delete, so Sweep's
// delete-propagation path can be exercised deterministically.
type deleteErrorBackend struct {
	*mem.Backend
	err error
}

func (b deleteErrorBackend) Delete(context.Context, cas.Digest) error { return b.err }

func TestSweepPropagatesDeleteError(t *testing.T) {
	ctx := context.Background()
	inner := mem.New()
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

func TestSweepSkipsConcurrentlyDeletedDuringAgeCheck(t *testing.T) {
	ctx := context.Background()
	backend := newStatMemBackend()
	// Put directly on the underlying mem.Backend so List reports the digest,
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
