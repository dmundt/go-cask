package packfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// openCountingBackend builds a pack backend whose read-opens are counted
// through the existing ops.open seam (no new global and no package-level
// state), so a test can assert how many pack files a batch opens.
func openCountingBackend(tb testing.TB, opts ...Option) (*Backend, *atomic.Int64) {
	tb.Helper()
	var opens atomic.Int64
	op := realOps()
	op.open = func(name string) (*os.File, error) {
		opens.Add(1)
		return os.Open(name)
	}
	backend, err := newWithOps(filepath.Join(tb.TempDir(), "store"), op, opts...)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = backend.Close() })
	return backend, &opens
}

// putMany stores count objects in one pack and returns their digests in order.
func putMany(tb testing.TB, backend *Backend, count int) []cas.Digest {
	tb.Helper()
	ctx := context.Background()
	digests := make([]cas.Digest, 0, count)
	for i := range count {
		payload := "object-" + strconv.Itoa(i)
		d := cas.NewDigest([]byte(payload))
		if err := backend.Put(ctx, d, bytesReader([]byte(payload))); err != nil {
			tb.Fatal(err)
		}
		digests = append(digests, d)
	}
	return digests
}

// TestPackfsGetManyOpensOnePackForAdjacentObjects is the acceptance test for the
// batched override: N objects that live in the same pack file cost exactly one
// read-open, where N sequential Gets would cost N. The open count comes from the
// backend's own file-open seam (ops.open), not from a new global.
func TestPackfsGetManyOpensOnePackForAdjacentObjects(t *testing.T) {
	ctx := context.Background()
	backend, opens := openCountingBackend(t, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	const objects = 8
	digests := putMany(t, backend, objects)

	// Every object is packed, so the batch must group them into one open. The
	// baseline is measured first and asserted too: Get per object opens the pack
	// once per object.
	before := opens.Load()
	for _, d := range digests {
		reader, err := backend.Get(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := opens.Load() - before; got != objects {
		t.Fatalf("sequential Get opened the pack %d times for %d objects, want %d", got, objects, objects)
	}

	before = opens.Load()
	seen := make(map[string]string, objects)
	err := cas.GetMany(ctx, backend, digests, func(d cas.Digest, reader io.ReadCloser) error {
		payload, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		seen[d.String()] = string(payload)
		return nil
	})
	if err != nil {
		t.Fatalf("GetMany() = %v, want nil", err)
	}
	if got := opens.Load() - before; got != 1 {
		t.Fatalf("GetMany opened the pack %d times for %d adjacent objects, want 1", got, objects)
	}
	if len(seen) != objects {
		t.Fatalf("GetMany served %d objects, want %d", len(seen), objects)
	}
	for i, d := range digests {
		if want, got := "object-"+strconv.Itoa(i), seen[d.String()]; got != want {
			t.Fatalf("object %s payload = %q, want %q", d, got, want)
		}
	}
}

// TestPackfsGetManyGroupsByPackFile pins that batching is per pack file: objects
// split across two packs cost two opens, not one per object.
func TestPackfsGetManyGroupsByPackFile(t *testing.T) {
	ctx := context.Background()
	backend, opens := openCountingBackend(t, WithEnabled(), WithPackMaxEntries(2), WithPackMaxBytes(0))
	digests := putMany(t, backend, 6) // rotates every 2 entries: 3 pack files

	before := opens.Load()
	count := 0
	err := cas.GetMany(ctx, backend, digests, func(_ cas.Digest, reader io.ReadCloser) error {
		count++
		_, err := io.Copy(io.Discard, reader)
		return err
	})
	if err != nil {
		t.Fatalf("GetMany() = %v, want nil", err)
	}
	if count != len(digests) {
		t.Fatalf("GetMany served %d objects, want %d", count, len(digests))
	}
	if got := opens.Load() - before; got != 3 {
		t.Fatalf("GetMany opened %d pack files, want one per pack (3)", got)
	}
}

// TestPackfsGetManyFallsBackToLooseObjects pins the loose path: an object with
// no pack record is served from the loose backend exactly as Get serves it,
// while the packed objects still share one open.
func TestPackfsGetManyFallsBackToLooseObjects(t *testing.T) {
	ctx := context.Background()
	backend, opens := openCountingBackend(t, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	digests := putMany(t, backend, 3)

	// Drop one index record: the object stays in the loose backend, so the batch
	// must fall back to it for that digest.
	looseDigest := digests[1]
	backend.mu.Lock()
	delete(backend.index, string(looseDigest))
	backend.mu.Unlock()

	before := opens.Load()
	seen := make(map[string]string, len(digests))
	err := cas.GetMany(ctx, backend, digests, func(d cas.Digest, reader io.ReadCloser) error {
		payload, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		seen[d.String()] = string(payload)
		return nil
	})
	if err != nil {
		t.Fatalf("GetMany() = %v, want nil", err)
	}
	for i, d := range digests {
		if want, got := "object-"+strconv.Itoa(i), seen[d.String()]; got != want {
			t.Fatalf("object %s payload = %q, want %q from the loose backend", d, got, want)
		}
	}
	if got := opens.Load() - before; got != 1 {
		t.Fatalf("GetMany opened the pack %d times around one loose object, want 1", got)
	}
}

// TestPackfsGetManyStopsOnErrorAndCancel pins the shared contract on the batched
// path: an error from fn stops the batch, a canceled context stops it, the pack
// file is still closed, and an absent digest is rejected.
func TestPackfsGetManyStopsOnErrorAndCancel(t *testing.T) {
	ctx := context.Background()
	backend, _ := openCountingBackend(t, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	digests := putMany(t, backend, 3)

	stop := errors.New("stop")
	calls := 0
	err := cas.GetMany(ctx, backend, digests, func(_ cas.Digest, reader io.ReadCloser) error {
		calls++
		if calls == 2 {
			return stop
		}
		_, readErr := io.Copy(io.Discard, reader)
		return readErr
	})
	if !errors.Is(err, stop) {
		t.Fatalf("GetMany() = %v, want the fn error", err)
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want 2", calls)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	err = cas.GetMany(canceled, backend, digests, func(_ cas.Digest, reader io.ReadCloser) error {
		calls++
		_, readErr := io.Copy(io.Discard, reader)
		return readErr
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMany(canceled) = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("fn called %d times for a canceled context, want 0", calls)
	}

	// The pack file must not stay open: a following batch still works.
	if err := cas.GetMany(ctx, backend, digests, func(_ cas.Digest, reader io.ReadCloser) error {
		_, readErr := io.Copy(io.Discard, reader)
		return readErr
	}); err != nil {
		t.Fatalf("GetMany after a stopped batch = %v, want nil", err)
	}

	if err := cas.GetMany(ctx, backend, []cas.Digest{digests[0], nil}, func(cas.Digest, io.ReadCloser) error {
		return nil
	}); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("GetMany(absent digest) = %v, want ErrInvalidDigest", err)
	}
	if err := backend.GetMany(ctx, digests, nil); err == nil {
		t.Fatal("GetMany(nil fn) = nil, want error")
	}
}

// TestPackfsGetManyPrunesStalePackRecords pins recovery inside the batch: a
// record whose pack file is gone is dropped (and the pruned index persisted)
// and its object is served from the loose backend, exactly as Get does.
func TestPackfsGetManyPrunesStalePackRecords(t *testing.T) {
	ctx := context.Background()
	backend, _ := openCountingBackend(t, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	digests := putMany(t, backend, 2)

	stale := digests[0]
	backend.mu.Lock()
	backend.index[string(stale)] = packRecord{Pack: filepath.Join(backend.packDir, "missing.pack"), Offset: 0, Size: 7}
	backend.mu.Unlock()

	seen := make(map[string]string, len(digests))
	err := cas.GetMany(ctx, backend, digests, func(d cas.Digest, reader io.ReadCloser) error {
		payload, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		seen[d.String()] = string(payload)
		return nil
	})
	if err != nil {
		t.Fatalf("GetMany() = %v, want nil", err)
	}
	if got := seen[stale.String()]; got != "object-0" {
		t.Fatalf("stale pack record payload = %q, want %q from the loose object", got, "object-0")
	}
	backend.mu.Lock()
	_, stillIndexed := backend.index[string(stale)]
	backend.mu.Unlock()
	if stillIndexed {
		t.Fatal("GetMany left the stale pack record in the index")
	}
}

// TestPackfsGetManyReportsOpenFailure pins the open error path: when the pack
// file cannot be opened the batch fails with a wrapped error.
func TestPackfsGetManyReportsOpenFailure(t *testing.T) {
	ctx := context.Background()
	backend, _ := openCountingBackend(t, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	digests := putMany(t, backend, 2)

	openErr := errors.New("open failed")
	backend.op.open = func(string) (*os.File, error) { return nil, openErr }
	if err := cas.GetMany(ctx, backend, digests, func(cas.Digest, io.ReadCloser) error {
		return nil
	}); !errors.Is(err, openErr) {
		t.Fatalf("GetMany() = %v, want an error wrapping the open failure", err)
	}
}

// BenchmarkPackfsGetManyVersusSequentialGet measures the wall-time difference
// between cas.GetMany and a plain sequential Get loop over a multi-thousand
// object store whose objects all live in one pack file. The only work that
// differs is the number of opens — one for the batch, one per object for the
// loop — so opens/op makes the two runs comparable across machines.
func BenchmarkPackfsGetManyVersusSequentialGet(b *testing.B) {
	ctx := context.Background()
	const objects = 4000

	var opens atomic.Int64
	op := realOps()
	op.open = func(name string) (*os.File, error) {
		opens.Add(1)
		return os.Open(name)
	}
	backend, err := newWithOps(filepath.Join(b.TempDir(), "store"), op, WithEnabled(), WithPackMaxEntries(0), WithPackMaxBytes(0))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	digests := putMany(b, backend, objects)

	drain := func(_ cas.Digest, reader io.ReadCloser) error {
		_, err := io.Copy(io.Discard, reader)
		return err
	}

	b.Run("GetMany", func(b *testing.B) {
		b.ReportAllocs()
		opens.Store(0)
		for range b.N {
			if err := cas.GetMany(ctx, backend, digests, drain); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(opens.Load())/float64(b.N), "opens/op")
	})

	b.Run("SequentialGet", func(b *testing.B) {
		b.ReportAllocs()
		opens.Store(0)
		for range b.N {
			for _, d := range digests {
				reader, err := backend.Get(ctx, d)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, reader); err != nil {
					_ = reader.Close()
					b.Fatal(err)
				}
				if err := reader.Close(); err != nil {
					b.Fatal(err)
				}
			}
		}
		b.ReportMetric(float64(opens.Load())/float64(b.N), "opens/op")
	})
}
