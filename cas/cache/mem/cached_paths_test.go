package memory_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	cachemem "github.com/dmundt/go-cask/cas/cache/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// memoPayload is a second cached object type, so these tests share no fixture
// with the package's existing cached_test.go helpers.
type memoPayload struct {
	ID string `json:"id"`
}

func (memoPayload) Type() string             { return "memopath@1" }
func (memoPayload) References() []cas.Digest { return nil }

// memoNode references other objects, so a recursive preload has edges to follow
// where memoPayload has none.
type memoNode struct {
	ID   string       `json:"id"`
	Refs []cas.Digest `json:"refs,omitempty"`
}

func (memoNode) Type() string { return "memonode@1" }

func (n memoNode) References() []cas.Digest { return n.Refs }

// failingBackend fails the operation a test names and serves everything else
// from an in-memory store. It is the seam that reaches Proxy's Exists error
// branch, which no real backend produces on demand.
type failingBackend struct {
	inner    cas.Backend
	existsFn func() (bool, error)
}

func (b *failingBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	return b.inner.Put(ctx, d, r)
}

func (b *failingBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return b.inner.Get(ctx, d)
}

func (b *failingBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if b.existsFn != nil {
		return b.existsFn()
	}
	return b.inner.Exists(ctx, d)
}

func (b *failingBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}

func (b *failingBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(ctx)
}

func (b *failingBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

// countingBackend counts how many object reads reach the byte layer, so a test
// can assert that a shared reference is walked once and that an absent one is
// not read at all.
type countingBackend struct {
	inner cas.Backend
	mu    sync.Mutex
	gets  int
}

func (b *countingBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	return b.inner.Put(ctx, d, r)
}

func (b *countingBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	b.mu.Lock()
	b.gets++
	b.mu.Unlock()
	return b.inner.Get(ctx, d)
}

func (b *countingBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	return b.inner.Exists(ctx, d)
}

func (b *countingBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}

func (b *countingBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(ctx)
}

func (b *countingBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

func (b *countingBackend) reads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gets
}

// cancellingBackend cancels its test's context while answering the first
// Exists, so the load that reaches the backend fails with the context error
// rather than reading bytes.
type cancellingBackend struct {
	inner  cas.Backend
	cancel context.CancelFunc

	mu      sync.Mutex
	exists  int
	onFirst bool
}

func (b *cancellingBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	return b.inner.Put(ctx, d, r)
}

func (b *cancellingBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return b.inner.Get(ctx, d)
}

func (b *cancellingBackend) Exists(context.Context, cas.Digest) (bool, error) {
	b.mu.Lock()
	b.exists++
	first := !b.onFirst
	b.onFirst = true
	b.mu.Unlock()
	if first {
		b.cancel()
	}
	return false, context.Canceled
}

func (b *cancellingBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}

func (b *cancellingBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(ctx)
}

func (b *cancellingBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

func (b *cancellingBackend) existsCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exists
}

// damagedNodeFrame is a memoNode frame whose payload is not valid JSON: the
// header still parses, so a reader reports damage rather than an unknown type.
var damagedNodeFrame = []byte("\x02\x04json\x08memonode@1\x09{\"id\":")

func newMemoStore(t *testing.T, backend cas.Backend) *cas.Store[memoPayload] {
	t.Helper()
	return cas.New(backend, jsoncodec.New[memoPayload](), sha256.New())
}

func newMemoNodeStore(t *testing.T, backend cas.Backend) *cas.Store[memoNode] {
	t.Helper()
	return cas.New(backend, jsoncodec.New[memoNode](), sha256.New())
}

// TestCachedObjectLoadMemoizesTheValue pins the memoization contract: the first
// Load reads the store, every later Load answers from the memo, and IsLoaded and
// Digest report that state without triggering another read. The error half of the
// same contract is pinned by the package's existing
// TestCachedObjectLoadErrorMemoized.
func TestCachedObjectLoadMemoizesTheValue(t *testing.T) {
	ctx := context.Background()
	store := newMemoStore(t, backmem.New())
	cached := cachemem.New(store)
	d, err := store.Put(ctx, memoPayload{ID: "memo"})
	if err != nil {
		t.Fatal(err)
	}
	co, err := cached.Proxy(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if co.IsLoaded() {
		t.Fatal("IsLoaded() = true before the first Load")
	}
	first, err := co.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !co.IsLoaded() {
		t.Fatal("IsLoaded() = false after Load")
	}
	if !co.Digest().Equal(d) {
		t.Fatalf("Digest() = %s, want %s", co.Digest(), d)
	}
	second, err := co.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || second.ID != "memo" {
		t.Fatalf("second Load = %+v, want the memoized %+v", second, first)
	}
	if st := cached.CacheStats(); st.Loads != 1 || st.Misses != 1 {
		t.Fatalf("CacheStats() = loads %d misses %d, want 1/1", st.Loads, st.Misses)
	}
}

// CachedObject.Load's inner "already loaded" branch (cached.go:82) is left
// uncovered on purpose (testing-strategy §5): it is the second half of the
// double-checked locking, and it has no deterministic trigger. A loader that
// observes loaded == false at cached.go:73 is already holding the read lock, so
// it cannot be preempted between that check and the write-lock acquisition by
// the loader that completes the load first — sync.RWMutex blocks a waiting
// writer until the reader releases. Only a race can enter it, which is why the
// package's own TestCachedObjectConcurrentLoad exercises it under -race rather
// than asserting it deterministically.

// TestCachedStoreProxySurfacesExistsFailure pins Proxy's backend failure
// branch: an Exists that cannot answer is reported as its error rather than
// being read as "the object is absent".
func TestCachedStoreProxySurfacesExistsFailure(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("exists exploded")
	store := newMemoStore(t, &failingBackend{
		inner:    backmem.New(),
		existsFn: func() (bool, error) { return false, boom },
	})
	cached := cachemem.New(store)
	d := sha256.Of([]byte("anything"))

	if _, err := cached.Proxy(ctx, d); !errors.Is(err, boom) {
		t.Fatalf("Proxy with a failing Exists = %v, want %v", err, boom)
	}
	if _, err := cached.Get(ctx, d); !errors.Is(err, boom) {
		t.Fatalf("Get with a failing Exists = %v, want %v", err, boom)
	}
	if st := cached.CacheStats(); st.Misses != 2 || st.Size != 0 {
		t.Fatalf("CacheStats() = misses %d size %d, want misses 2 size 0", st.Misses, st.Size)
	}
}

// TestWarmupSummarizesCancellationOnceInsteadOfPerDigest pins the record()
// branch that drops a per-digest context error: one worker's load fails because
// the store read canceled the context, and the warm-up reports that
// cancellation once from ctx.Err() after the workers stop instead of recording
// it per digest.
func TestWarmupSummarizesCancellationOnceInsteadOfPerDigest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancellingBackend{inner: backmem.New(), cancel: cancel}
	cached := cachemem.New(newMemoStore(t, backend))

	err := cached.Warmup(ctx, []cas.Digest{sha256.Of([]byte("a")), sha256.Of([]byte("b"))})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Warmup with a context canceled mid-load = %v, want context.Canceled", err)
	}
	// How many workers reach the backend is deliberately NOT asserted: the feed
	// has two idle workers and two digests, so it can dispatch both before the
	// cancellation raised inside the first Exists is observed, and one or two
	// calls happen depending on scheduling. What IS deterministic — and what
	// this test pins — is the record() branch: every worker's context error is
	// dropped and the cancellation is summarized exactly once from ctx.Err(),
	// instead of being recorded once per digest.
	if got := strings.Count(err.Error(), context.Canceled.Error()); got != 1 {
		t.Fatalf("Warmup reported the cancellation %d times (%q), want exactly once, not per digest", got, err)
	}
	if got := backend.existsCalls(); got == 0 {
		t.Fatal("no worker reached the backend, so the cancellation branch was never exercised")
	}
	if st := cached.CacheStats(); st.Size != 0 || st.Loads != 0 {
		t.Fatalf("CacheStats() = size %d loads %d, want 0/0 (the load never completed)", st.Size, st.Loads)
	}
}

// TestPreloadRecursiveWalksASharedReferenceOnce pins the walk's seen-set
// branch: a diamond — one child reached twice from one node — is warmed once,
// not once per reference, because warming the same subtree twice is duplicate
// work rather than extra safety.
func TestPreloadRecursiveWalksASharedReferenceOnce(t *testing.T) {
	ctx := context.Background()
	backend := &countingBackend{inner: backmem.New()}
	store := newMemoNodeStore(t, backend)
	cached := cachemem.New(store)

	shared, err := store.Put(ctx, memoNode{ID: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.Put(ctx, memoNode{ID: "root", Refs: []cas.Digest{shared, shared}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cached.PreloadRecursive(ctx, root, 2); err != nil {
		t.Fatalf("PreloadRecursive = %v, want nil", err)
	}
	if got := backend.reads(); got != 2 {
		t.Fatalf("object reads = %d, want 2 (the root and the shared child once)", got)
	}
	if st := cached.CacheStats(); st.Size != 2 {
		t.Fatalf("cache size = %d, want 2", st.Size)
	}
}

// TestPreloadRecursiveSkipsTheAbsentDigest pins the walk's first guard, which
// the existing depth/preload tests cannot reach: a node's References are only
// ever non-zero digests in those fixtures, so the zero digest — an absent
// reference, not an object to warm — must be pinned explicitly. It neither
// reads the store nor reports a missing object.
func TestPreloadRecursiveSkipsTheAbsentDigest(t *testing.T) {
	ctx := context.Background()
	backend := &countingBackend{inner: backmem.New()}
	cached := cachemem.New(newMemoNodeStore(t, backend))

	if err := cached.PreloadRecursive(ctx, cas.Digest{}, 3); err != nil {
		t.Fatalf("PreloadRecursive(zero digest) = %v, want nil", err)
	}
	if got := backend.reads(); got != 0 {
		t.Fatalf("object reads = %d, want 0: an absent reference is not an object to warm", got)
	}
	if st := cached.CacheStats(); st.Size != 0 {
		t.Fatalf("cache size = %d, want 0", st.Size)
	}
}

// TestPreloadRecursiveReportsADamagedReference pins the failure branch the walk
// deliberately does not tolerate, reached with bytes rather than an injected
// error: a reference whose stored frame parses but whose payload is damaged is
// reported as cas.ErrCorrupt, because "not my type" is an expected shape of a
// shared store while "cannot be read" is not.
func TestPreloadRecursiveReportsADamagedReference(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	damaged := sha256.Of(damagedNodeFrame)
	if err := backend.Put(ctx, damaged, bytes.NewReader(damagedNodeFrame)); err != nil {
		t.Fatal(err)
	}
	store := newMemoNodeStore(t, backend)
	root, err := store.Put(ctx, memoNode{ID: "root", Refs: []cas.Digest{damaged}})
	if err != nil {
		t.Fatal(err)
	}
	err = cachemem.New(store).PreloadRecursive(ctx, root, 2)
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("PreloadRecursive over a damaged reference = %v, want cas.ErrCorrupt", err)
	}
	if errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("PreloadRecursive over a damaged reference = %v, must not report an unknown type", err)
	}
}

// cached.go:238 (`break feed`) is left uncovered on purpose
// (testing-strategy §5): the select's ctx.Done() arm is only reachable when the
// context is canceled while the feed is blocked trying to hand a digest to a
// worker, and every deterministic cancel these tests stage is already observed
// by the `ctx.Err() != nil` guard at the top of the same iteration. Reaching it
// would need a race between that guard and the sender, so no test asserts it.
