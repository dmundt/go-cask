package cas_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	packfs "github.com/dmundt/go-cask/cas/backend/packfs"
	memory "github.com/dmundt/go-cask/cas/cache/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// batchReader is one object reader a batchTestBackend handed out. It records
// how often it was closed, so a test can assert the ownership contract
// (GetMany closes each reader exactly once, after fn returns).
type batchReader struct {
	io.Reader
	closeErr error
	closes   int
}

func (r *batchReader) Close() error {
	r.closes++
	return r.closeErr
}

// batchTestBackend is a minimal in-memory Backend that records every Get it
// serves and every reader it hands out. It implements no optional capability,
// so it exercises the default GetMany path.
type batchTestBackend struct {
	objects  map[string][]byte
	getErr   map[string]error
	gets     []cas.Digest
	readers  []*batchReader
	closeErr error
}

func newBatchTestBackend() *batchTestBackend {
	return &batchTestBackend{objects: map[string][]byte{}, getErr: map[string]error{}}
}

// put stores data under its own digest-like key (the tests only need a stable
// address, not a real hash).
func (b *batchTestBackend) put(key, data string) cas.Digest {
	d := cas.NewDigest([]byte(key))
	b.objects[d.String()] = []byte(data)
	return d
}

func (b *batchTestBackend) Put(_ context.Context, d cas.Digest, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.objects[d.String()] = data
	return nil
}

func (b *batchTestBackend) Get(_ context.Context, d cas.Digest) (io.ReadCloser, error) {
	if err := b.getErr[d.String()]; err != nil {
		return nil, err
	}
	data, ok := b.objects[d.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", cas.ErrNotFound, d)
	}
	b.gets = append(b.gets, d)
	reader := &batchReader{Reader: bytes.NewReader(data), closeErr: b.closeErr}
	b.readers = append(b.readers, reader)
	return reader, nil
}

func (b *batchTestBackend) Exists(_ context.Context, d cas.Digest) (bool, error) {
	_, ok := b.objects[d.String()]
	return ok, nil
}

func (b *batchTestBackend) Delete(_ context.Context, d cas.Digest) error {
	delete(b.objects, d.String())
	return nil
}

func (b *batchTestBackend) List(context.Context) ([]cas.Digest, error) {
	out := make([]cas.Digest, 0, len(b.objects))
	for key := range b.objects {
		out = append(out, cas.NewDigest([]byte(key)))
	}
	return out, nil
}

func (b *batchTestBackend) Stats(context.Context) (*cas.Stats, error) {
	return &cas.Stats{ObjectCount: int64(len(b.objects))}, nil
}

// collector returns a GetMany fn that records the served digests in call order
// and asserts the reader is still open when fn runs — GetMany owns the Close
// and must perform it only after fn returns.
func collector(t *testing.T, seen *[]string) func(cas.Digest, io.ReadCloser) error {
	t.Helper()
	return func(d cas.Digest, reader io.ReadCloser) error {
		tracked, ok := reader.(*batchReader)
		if !ok {
			t.Fatalf("GetMany handed fn a %T, want the backend's own reader", reader)
		}
		if tracked.closes != 0 {
			t.Fatalf("reader for %s was closed before fn returned", d)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		*seen = append(*seen, string(data))
		return nil
	}
}

// TestGetManyStreamsInOrderAndClosesEveryReader pins the default contract: the
// requested order is preserved, every object is streamed whole, and each reader
// is closed exactly once after fn returns.
func TestGetManyStreamsInOrderAndClosesEveryReader(t *testing.T) {
	ctx := context.Background()
	backend := newBatchTestBackend()
	first := backend.put("first", "one")
	second := backend.put("second", "two")
	third := backend.put("third", "three")

	var seen []string
	if err := cas.GetMany(ctx, backend, []cas.Digest{first, second, third}, collector(t, &seen)); err != nil {
		t.Fatalf("GetMany() = %v, want nil", err)
	}
	if want := []string{"one", "two", "three"}; !equalStrings(seen, want) {
		t.Fatalf("GetMany served %v, want %v", seen, want)
	}
	if len(backend.gets) != 3 {
		t.Fatalf("backend served %d objects, want 3", len(backend.gets))
	}
	for i, reader := range backend.readers {
		if reader.closes != 1 {
			t.Fatalf("reader %d closed %d times, want exactly 1", i, reader.closes)
		}
	}
}

// TestGetManyStopsOnFnError pins error propagation from fn: the loop stops, the
// fn error is returned unwrapped, the offending reader is still closed, and no
// later object is read.
func TestGetManyStopsOnFnError(t *testing.T) {
	ctx := context.Background()
	backend := newBatchTestBackend()
	first := backend.put("first", "one")
	second := backend.put("second", "two")
	third := backend.put("third", "three")

	stop := errors.New("stop here")
	calls := 0
	err := cas.GetMany(ctx, backend, []cas.Digest{first, second, third}, func(d cas.Digest, reader io.ReadCloser) error {
		calls++
		if calls == 2 {
			return stop
		}
		_, readErr := io.ReadAll(reader)
		return readErr
	})
	if !errors.Is(err, stop) {
		t.Fatalf("GetMany() = %v, want the fn error", err)
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want 2", calls)
	}
	if len(backend.gets) != 2 {
		t.Fatalf("backend served %d objects, want 2 (the third must not be read)", len(backend.gets))
	}
	for i, reader := range backend.readers {
		if reader.closes != 1 {
			t.Fatalf("reader %d closed %d times, want exactly 1", i, reader.closes)
		}
	}
}

// TestGetManyStopsOnGetError pins error propagation from the backend: the error
// is wrapped with %w (so errors.Is still finds it), the loop stops, and the
// reader already handed out is closed.
func TestGetManyStopsOnGetError(t *testing.T) {
	ctx := context.Background()
	backend := newBatchTestBackend()
	first := backend.put("first", "one")
	second := backend.put("second", "two")
	third := backend.put("third", "three")
	backend.getErr[second.String()] = cas.ErrNotFound

	err := cas.GetMany(ctx, backend, []cas.Digest{first, second, third}, func(cas.Digest, io.ReadCloser) error {
		return nil
	})
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetMany() = %v, want an error wrapping ErrNotFound", err)
	}
	if len(backend.gets) != 1 {
		t.Fatalf("backend served %d objects, want 1 (the third must not be read)", len(backend.gets))
	}
	if backend.readers[0].closes != 1 {
		t.Fatalf("reader closed %d times, want exactly 1", backend.readers[0].closes)
	}
}

// TestGetManyRejectsAbsentDigest pins the CheckDigest habit: an absent digest
// fails the batch with ErrInvalidDigest instead of reaching the backend.
func TestGetManyRejectsAbsentDigest(t *testing.T) {
	ctx := context.Background()
	backend := newBatchTestBackend()
	first := backend.put("first", "one")

	err := cas.GetMany(ctx, backend, []cas.Digest{first, nil}, func(cas.Digest, io.ReadCloser) error {
		return nil
	})
	if !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("GetMany() = %v, want ErrInvalidDigest", err)
	}
	if len(backend.gets) != 1 {
		t.Fatalf("backend served %d objects, want 1", len(backend.gets))
	}
	if backend.readers[0].closes != 1 {
		t.Fatalf("reader closed %d times, want exactly 1", backend.readers[0].closes)
	}
}

// TestGetManyHonorsContextCancellation pins both cancellation points: a context
// already canceled serves nothing, and a cancellation raised by fn stops the
// batch and is returned as context.Canceled.
func TestGetManyHonorsContextCancellation(t *testing.T) {
	backend := newBatchTestBackend()
	first := backend.put("first", "one")
	second := backend.put("second", "two")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err := cas.GetMany(canceled, backend, []cas.Digest{first, second}, func(cas.Digest, io.ReadCloser) error {
		t.Fatal("fn must not run for a canceled context")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMany(canceled) = %v, want context.Canceled", err)
	}
	if len(backend.gets) != 0 {
		t.Fatalf("backend served %d objects before cancellation, want 0", len(backend.gets))
	}

	ctx, cancelMid := context.WithCancel(context.Background())
	defer cancelMid()
	err = cas.GetMany(ctx, backend, []cas.Digest{first, second}, func(_ cas.Digest, reader io.ReadCloser) error {
		cancelMid()
		_, readErr := io.ReadAll(reader)
		return readErr
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMany after mid-batch cancel = %v, want context.Canceled", err)
	}
	if len(backend.gets) != 1 {
		t.Fatalf("backend served %d objects after mid-batch cancel, want 1", len(backend.gets))
	}
	for i, reader := range backend.readers {
		if reader.closes != 1 {
			t.Fatalf("reader %d closed %d times, want exactly 1", i, reader.closes)
		}
	}
}

// TestGetManyReportsCloseError pins close reporting: when fn succeeds but the
// reader's Close fails, GetMany returns the wrapped close error; when fn fails,
// fn's error wins and the close failure does not mask it.
func TestGetManyReportsCloseError(t *testing.T) {
	ctx := context.Background()
	backend := newBatchTestBackend()
	first := backend.put("first", "one")
	backend.closeErr = errors.New("close failed")

	err := cas.GetMany(ctx, backend, []cas.Digest{first}, func(cas.Digest, io.ReadCloser) error {
		return nil
	})
	if err == nil || !errors.Is(err, backend.closeErr) {
		t.Fatalf("GetMany() = %v, want an error wrapping the close failure", err)
	}

	fnErr := errors.New("fn failed")
	err = cas.GetMany(ctx, backend, []cas.Digest{first}, func(cas.Digest, io.ReadCloser) error {
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Fatalf("GetMany() = %v, want fn's error to win over the close failure", err)
	}
}

// TestGetManyRejectsNilBackendAndNilFn pins the guards: a nil backend and a nil
// fn are reported instead of panicking inside library code.
func TestGetManyRejectsNilBackendAndNilFn(t *testing.T) {
	ctx := context.Background()
	fn := func(cas.Digest, io.ReadCloser) error { return nil }
	if err := cas.GetMany(ctx, nil, nil, fn); err == nil {
		t.Fatal("GetMany(nil backend) = nil, want error")
	}
	if err := cas.GetMany(ctx, newBatchTestBackend(), nil, nil); err == nil {
		t.Fatal("GetMany(nil fn) = nil, want error")
	}
}

// batchGetterBackend is a Backend that opts into BatchGetter, so the dispatch
// in GetMany is observable: it serves every request from its own field and
// records that it was asked.
type batchGetterBackend struct {
	batchTestBackend
	called   bool
	requests []cas.Digest
}

func (b *batchGetterBackend) GetMany(_ context.Context, digests []cas.Digest, fn func(cas.Digest, io.ReadCloser) error) error {
	b.called = true
	b.requests = digests
	// Deliberately serve in reverse: GetMany documents that the order is the
	// backend's choice, and this proves the documented dispatch happened.
	for _, digest := range slices.Backward(digests) {
		reader, err := b.Get(context.Background(), digest)
		if err != nil {
			return err
		}
		if err := fn(digest, reader); err != nil {
			_ = reader.Close()
			return err
		}
		if err := reader.Close(); err != nil {
			return err
		}
	}
	return nil
}

// TestGetManyDispatchesToBatchGetter pins the optional-interface dispatch: a
// backend implementing BatchGetter serves the batch, and its order is honored.
func TestGetManyDispatchesToBatchGetter(t *testing.T) {
	ctx := context.Background()
	backend := &batchGetterBackend{batchTestBackend: *newBatchTestBackend()}
	first := backend.put("first", "one")
	second := backend.put("second", "two")

	var seen []string
	if err := cas.GetMany(ctx, backend, []cas.Digest{first, second}, collector(t, &seen)); err != nil {
		t.Fatalf("GetMany() = %v, want nil", err)
	}
	if !backend.called {
		t.Fatal("GetMany did not dispatch to the backend's BatchGetter implementation")
	}
	if want := []string{"two", "one"}; !equalStrings(seen, want) {
		t.Fatalf("BatchGetter served %v, want the backend's own order %v", seen, want)
	}
}

// TestGetManyWorksWithShippedBackends pins the default path against the
// backends this repo ships: they implement BatchGetter only where they can
// batch, and the plain Get loop is correct for the rest.
func TestGetManyWorksWithShippedBackends(t *testing.T) {
	ctx := context.Background()
	fsBackend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backends := map[string]cas.Backend{
		"mem": mem.New(),
		"fs":  fsBackend,
	}
	for name, backend := range backends {
		t.Run(name, func(t *testing.T) {
			digests := make([]cas.Digest, 0, 3)
			want := make([]string, 0, 3)
			for i := range 3 {
				payload := "payload-" + strconv.Itoa(i)
				d := cas.NewDigest([]byte(name + "-" + payload))
				if err := backend.Put(ctx, d, bytes.NewReader([]byte(payload))); err != nil {
					t.Fatal(err)
				}
				digests = append(digests, d)
				want = append(want, payload)
			}
			var seen []string
			err := cas.GetMany(ctx, backend, digests, func(_ cas.Digest, reader io.ReadCloser) error {
				data, err := io.ReadAll(reader)
				if err != nil {
					return err
				}
				seen = append(seen, string(data))
				return nil
			})
			if err != nil {
				t.Fatalf("GetMany() = %v, want nil", err)
			}
			if !equalStrings(seen, want) {
				t.Fatalf("GetMany served %v, want %v", seen, want)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// BenchmarkGetManyVersusSequentialGet measures the default GetMany path against
// a hand-written Get loop over the same multi-thousand-object store. It is the
// baseline half of the comparison: with an in-memory backend there is no open
// to save, so the two should match — GetMany adds no per-object overhead — and
// the batching win is measured where it exists, in
// cas/backend/packfs/batch_test.go.
func BenchmarkGetManyVersusSequentialGet(b *testing.B) {
	ctx := context.Background()
	const objects = 4000
	backend := mem.New()
	digests := make([]cas.Digest, 0, objects)
	for i := range objects {
		d := cas.NewDigest([]byte("benchmark-object-" + strconv.Itoa(i)))
		if err := backend.Put(ctx, d, bytes.NewReader([]byte("payload"))); err != nil {
			b.Fatal(err)
		}
		digests = append(digests, d)
	}

	b.Run("GetMany", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := cas.GetMany(ctx, backend, digests, func(_ cas.Digest, reader io.ReadCloser) error {
				_, err := io.Copy(io.Discard, reader)
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("SequentialGet", func(b *testing.B) {
		b.ReportAllocs()
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
	})
}

// prefetchObject is the typed object the prefetch benchmark loads: a flat
// revision member with no references.
type prefetchObject struct {
	Payload string `json:"payload"`
}

// Type returns a versioned type name, which a store requires.
func (o *prefetchObject) Type() string { return "prefetch-bench@1" }

// References reports no references: the benchmark store is flat.
func (o *prefetchObject) References() []cas.Digest { return nil }

// prefetchCodec is a trivial in-test codec, so this benchmark depends on no
// cas/codec choice.
type prefetchCodec struct{}

func (prefetchCodec) Encode(o *prefetchObject) ([]byte, error) { return []byte(o.Payload), nil }

func (prefetchCodec) Decode(data []byte) (*prefetchObject, error) {
	return &prefetchObject{Payload: string(data)}, nil
}

// BenchmarkPrefetchVersusSequentialLoad measures the other half of the load
// recipe in cas-core §4.13 over the same multi-thousand-object packed store:
// the revision is loaded sequentially through a cache (one open at a time) and
// loaded with Preload, whose worker pool overlaps the same opens. Both arms do
// exactly the same per-object work — the cache's proxy lookup, the existence
// check and the object load — so the difference is the concurrency of the
// backend's opens, not the amount of work. On a local filesystem that
// concurrency buys nothing (an open costs a couple of microseconds, so the arm
// is in fact slower); the benchmark is here to keep that claim measurable
// rather than assumed.
func BenchmarkPrefetchVersusSequentialLoad(b *testing.B) {
	ctx := context.Background()
	const objects = 4000
	backend, err := packfs.New(filepath.Join(b.TempDir(), "store"),
		packfs.WithEnabled(), packfs.WithPackMaxEntries(0), packfs.WithPackMaxBytes(0))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = backend.Close() }()

	store := cas.New[*prefetchObject](backend, prefetchCodec{}, sha256.New())
	digests := make([]cas.Digest, 0, objects)
	for i := range objects {
		d, err := store.Put(ctx, &prefetchObject{Payload: "payload-" + strconv.Itoa(i)})
		if err != nil {
			b.Fatal(err)
		}
		digests = append(digests, d)
	}
	cached := memory.New(store)

	b.Run("SequentialLoad", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			cached.Clear()
			for _, d := range digests {
				if _, err := cached.Get(ctx, d); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("PreloadThenTraverse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			cached.Clear()
			if err := cached.Preload(ctx, digests); err != nil {
				b.Fatal(err)
			}
			for _, d := range digests {
				if _, err := cached.Get(ctx, d); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
