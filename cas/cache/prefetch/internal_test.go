package prefetch

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	mem "github.com/dmundt/go-cask/cas/cache/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// internalObject is the object type for the tests that need the unexported
// prefetch semaphore — the concurrency bound is only observable from inside the
// package.
type internalObject struct {
	Name string
	Refs []cas.Digest
}

func (internalObject) Type() string { return "internal@1" }

func (o internalObject) References() []cas.Digest {
	if len(o.Refs) == 0 {
		return nil
	}
	refs := make([]cas.Digest, 0, len(o.Refs))
	for _, d := range o.Refs {
		if !d.IsZero() {
			refs = append(refs, d)
		}
	}
	return refs
}

// zeroRefObject reports an absent digest next to a real reference, which a
// store's reference list may legitimately contain.
type zeroRefObject struct {
	Child cas.Digest
}

func (zeroRefObject) Type() string { return "zeroref@1" }

func (o zeroRefObject) References() []cas.Digest { return []cas.Digest{nil, o.Child} }

func internalStore(t *testing.T) (*cas.Store[internalObject], *mem.CachedStore[internalObject]) {
	t.Helper()
	s := cas.New(backmem.New(), jsoncodec.New[internalObject](), sha256.New())
	return s, mem.New(s)
}

// waitIdle waits until every prefetch slot is free, which means the walk started
// by GetWithPrefetch has finished.
func waitIdle(t *testing.T, sc *SmartCache[internalObject]) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(sc.sem) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("prefetch did not release its slot")
}

// TestPrefetchIsDroppedWhenSaturated pins the concurrency bound: with every
// slot busy, a read returns its object immediately and skips the prefetch
// instead of blocking or starting one goroutine per read.
func TestPrefetchIsDroppedWhenSaturated(t *testing.T) {
	ctx := context.Background()
	s, cs := internalStore(t)
	leaf, _ := s.Put(ctx, internalObject{Name: "leaf"})
	parent, _ := s.Put(ctx, internalObject{Name: "parent", Refs: []cas.Digest{leaf}})

	sc := NewSmartCache(cs, 2)
	for i := 0; i < prefetchConcurrency; i++ {
		sc.sem <- struct{}{} // occupy every prefetch slot
	}

	obj, err := sc.GetWithPrefetch(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "parent" {
		t.Fatalf("got %q", obj.Name)
	}
	if co := cs.Lookup(leaf.String()); co != nil {
		t.Fatal("prefetch must be dropped while every slot is busy")
	}
	if got := len(sc.sem); got != prefetchConcurrency {
		t.Fatalf("busy slots = %d, want %d: a prefetch started despite saturation", got, prefetchConcurrency)
	}
}

// TestPrefetchDiamondVisitsSharedSubgraphOnce builds root -> {left, right} with
// both pointing at one shared leaf. The visited set must walk the shared
// subgraph once: without it, the second path would look the leaf up again and
// register a cache hit.
func TestPrefetchDiamondVisitsSharedSubgraphOnce(t *testing.T) {
	ctx := context.Background()
	s, cs := internalStore(t)
	leaf, _ := s.Put(ctx, internalObject{Name: "leaf"})
	left, _ := s.Put(ctx, internalObject{Name: "left", Refs: []cas.Digest{leaf}})
	right, _ := s.Put(ctx, internalObject{Name: "right", Refs: []cas.Digest{leaf}})
	root, _ := s.Put(ctx, internalObject{Name: "root", Refs: []cas.Digest{left, right}})

	sc := NewSmartCache(cs, 3)
	if _, err := sc.GetWithPrefetch(ctx, root); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, sc)

	for _, d := range []cas.Digest{left, right, leaf} {
		co := cs.Lookup(d.String())
		if co == nil || !co.IsLoaded() {
			t.Fatalf("%s must be prefetched", d)
		}
	}
	st := cs.CacheStats()
	if st.Misses != 4 {
		t.Fatalf("misses = %d, want 4 (root, left, right, leaf)", st.Misses)
	}
	if st.Hits != 0 {
		t.Fatalf("hits = %d, want 0: the shared leaf was walked twice", st.Hits)
	}
}

// TestPrefetchSelfReferenceTerminates serves an object under the very digest it
// references — a cycle a backend can produce even though content addressing
// normally makes one impossible — and pins that the visited set ends the walk.
func TestPrefetchSelfReferenceTerminates(t *testing.T) {
	ctx := context.Background()
	raw := backmem.New()
	s := cas.New(raw, jsoncodec.New[internalObject](), sha256.New())
	cs := mem.New(s)

	cyclic := sha256.Of([]byte("cyclic root"))
	h, err := s.Put(ctx, internalObject{Name: "self", Refs: []cas.Digest{cyclic}})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := raw.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Put(ctx, cyclic, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}

	sc := NewSmartCache(cs, 8)
	if _, err := sc.GetWithPrefetch(ctx, cyclic); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, sc) // a walk that failed to terminate would never free its slot

	if co := cs.Lookup(cyclic.String()); co == nil || !co.IsLoaded() {
		t.Fatal("the root must remain cached")
	}
}

// TestPrefetchStopsAtZeroDepth pins that a depth-1 prefetch loads the direct
// references and then stops, exercising the depth == 0 base case.
func TestPrefetchStopsAtZeroDepth(t *testing.T) {
	ctx := context.Background()
	s, cs := internalStore(t)
	leaf, _ := s.Put(ctx, internalObject{Name: "leaf"})
	parent, _ := s.Put(ctx, internalObject{Name: "parent", Refs: []cas.Digest{leaf}})

	sc := NewSmartCache(cs, 1)
	if _, err := sc.GetWithPrefetch(ctx, parent); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, sc)
	if co := cs.Lookup(leaf.String()); co == nil || !co.IsLoaded() {
		t.Fatal("depth 1 must prefetch the direct reference")
	}
}

// TestPrefetchSkipsAbsentReferences pins that an absent digest in a reference
// list is skipped without stopping the walk at the real reference beside it.
func TestPrefetchSkipsAbsentReferences(t *testing.T) {
	ctx := context.Background()
	raw := backmem.New()
	zs := cas.New(raw, jsoncodec.New[zeroRefObject](), sha256.New())
	cs := mem.New(zs)

	child, err := zs.Put(ctx, zeroRefObject{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := zs.Put(ctx, zeroRefObject{Child: child})
	if err != nil {
		t.Fatal(err)
	}

	sc := NewSmartCache(cs, 2)
	if _, err := sc.GetWithPrefetch(ctx, root); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if co := cs.Lookup(child.String()); co != nil && co.IsLoaded() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the real reference must be prefetched despite the absent one beside it")
}

// TestPrefetchRecursiveHonorsCanceledContext pins that the walk observes
// cancellation instead of fetching for up to the prefetch timeout.
func TestPrefetchRecursiveHonorsCanceledContext(t *testing.T) {
	ctx := context.Background()
	s, cs := internalStore(t)
	leaf, _ := s.Put(ctx, internalObject{Name: "leaf"})
	parent, _ := s.Put(ctx, internalObject{Name: "parent", Refs: []cas.Digest{leaf}})
	obj, err := cs.Get(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	sc := NewSmartCache(cs, 2)
	sc.prefetchRecursive(cctx, obj, 2, map[string]struct{}{parent.String(): {}})
	if co := cs.Lookup(leaf.String()); co != nil {
		t.Fatal("a canceled context must stop the walk before it fetches references")
	}
}

// TestPrefetchDoesNotDieWhenTheCallerCancelsAfterStart pins that the caller's
// cancellation does not kill the already-launched background walk; a request
// scope ends, but the prefetch's lifetime should outlive it.
func TestPrefetchDoesNotDieWhenTheCallerCancelsAfterStart(t *testing.T) {
	ctx := context.Background()
	s, cs := internalStore(t)
	leaf, _ := s.Put(ctx, internalObject{Name: "leaf"})
	parent, _ := s.Put(ctx, internalObject{Name: "parent", Refs: []cas.Digest{leaf}})

	cctx, cancel := context.WithCancel(ctx)
	sc := NewSmartCache(cs, 2)
	if _, err := sc.GetWithPrefetch(cctx, parent); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitIdle(t, sc)
	if co := cs.Lookup(leaf.String()); co == nil || !co.IsLoaded() {
		t.Fatal("a cancel on the caller's context must not stop a started prefetch")
	}
}

// TestPrefetchSkipsAlreadyCanceledContext pins that a request already canceled
// before the read never starts a background warm-up.
func TestPrefetchSkipsAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, cs := internalStore(t)
	leaf, _ := s.Put(context.Background(), internalObject{Name: "leaf"})
	parent, _ := s.Put(context.Background(), internalObject{Name: "parent", Refs: []cas.Digest{leaf}})

	sc := NewSmartCache(cs, 2)
	if _, err := sc.GetWithPrefetch(ctx, parent); err == nil {
		t.Fatal("a canceled ctx must fail the read before it can warm the cache")
	}
	if co := cs.Lookup(leaf.String()); co != nil {
		t.Fatal("a canceled ctx must skip background prefetch before the first load")
	}
}
