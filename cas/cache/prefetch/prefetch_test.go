package prefetch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	mem "github.com/dmundt/go-cask/cas/cache/mem"
	"github.com/dmundt/go-cask/cas/cache/prefetch"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

type testObject struct {
	Name string       `json:"Name"`
	Refs []cas.Digest `json:"Refs"`
}

func (testObject) Type() string { return "test@1" }

// References returns the non-absent references, or nil for a leaf (a cas.Digest
// renders itself as one hex string, so this type needs no JSON code).
func (o testObject) References() []cas.Digest {
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

func newStore(t *testing.T) (*cas.Store[testObject], *mem.CachedStore[testObject]) {
	t.Helper()
	s := cas.New(backmem.New(), jsoncodec.New[testObject](), sha256.New())
	return s, mem.New(s)
}

func TestSmartCache(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	sc := prefetch.NewSmartCache(cs, 2)
	h, _ := s.Put(ctx, testObject{Name: "root"})
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "root" {
		t.Fatalf("got %q", obj.Name)
	}
}

func TestSmartCacheZeroDepth(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	sc := prefetch.NewSmartCache(cs, 0)
	h, _ := s.Put(ctx, testObject{Name: "only"})
	obj, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "only" {
		t.Fatalf("got %q", obj.Name)
	}
}

func TestSmartCacheMissing(t *testing.T) {
	ctx := context.Background()
	_, cs := newStore(t)
	sc := prefetch.NewSmartCache(cs, 2)
	m := sha256.Of([]byte("never stored"))
	if _, err := sc.GetWithPrefetch(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}

// waitCached polls until the given hash is loaded into the store's cache or
// the deadline passes. It lets the asynchronous prefetch goroutine finish.
func waitCached(t *testing.T, cs *mem.CachedStore[testObject], d cas.Digest) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if co := cs.Lookup(d.String()); co != nil && co.IsLoaded() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("digest %s never became cached", d)
}

// TestSmartCachePrefetchReference warms a directly referenced object: the
// parent references the leaf, so GetWithPrefetch on the parent must
// asynchronously load the leaf into the cache.
func TestSmartCachePrefetchReference(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	leaf, _ := s.Put(ctx, testObject{Name: "leaf"})
	parent, _ := s.Put(ctx, testObject{Name: "parent", Refs: []cas.Digest{leaf}})
	sc := prefetch.NewSmartCache(cs, 2)

	obj, err := sc.GetWithPrefetch(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "parent" {
		t.Fatalf("got %q", obj.Name)
	}
	waitCached(t, cs, leaf)
}

// TestSmartCachePrefetchChainRecursion builds a chain leaf -> parent -> root
// and prefetches the root. The recursion must descend through each level,
// exercising the depth decrement, until every referenced object is cached.
func TestSmartCachePrefetchChainRecursion(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	leaf, _ := s.Put(ctx, testObject{Name: "leaf"})
	parent, _ := s.Put(ctx, testObject{Name: "parent", Refs: []cas.Digest{leaf}})
	root, _ := s.Put(ctx, testObject{Name: "root", Refs: []cas.Digest{parent}})
	sc := prefetch.NewSmartCache(cs, 3)

	if _, err := sc.GetWithPrefetch(ctx, root); err != nil {
		t.Fatal(err)
	}
	// parent should be warmed at depth 1, then leaf at depth 2.
	waitCached(t, cs, parent)
	waitCached(t, cs, leaf)
}

// TestSmartCachePrefetchSkipsMissing has the fetched object reference an
// existing child and a missing hash; the prefetch loop must load the valid
// child and skip (not fail on) the missing reference.
func TestSmartCachePrefetchSkipsMissing(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	child, _ := s.Put(ctx, testObject{Name: "child"})
	missing := sha256.Of([]byte("never stored either"))
	parent, _ := s.Put(ctx, testObject{Name: "parent", Refs: []cas.Digest{child, missing}})
	sc := prefetch.NewSmartCache(cs, 2)

	if _, err := sc.GetWithPrefetch(ctx, parent); err != nil {
		t.Fatal(err)
	}
	// The valid child is warmed; the missing ref is skipped without error.
	waitCached(t, cs, child)
}

// TestSmartCachePrefetchEmptyRefs confirms a referenced-less object with
// prefetching enabled runs prefetchRecursive without iterating any reference.
func TestSmartCachePrefetchEmptyRefs(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	h, _ := s.Put(ctx, testObject{Name: "solo"})
	sc := prefetch.NewSmartCache(cs, 1)

	if _, err := sc.GetWithPrefetch(ctx, h); err != nil {
		t.Fatal(err)
	}
	// Give the spawned goroutine a moment to run without references.
	time.Sleep(10 * time.Millisecond)
	if st := cs.CacheStats(); st.Size != 1 {
		t.Fatalf("cache size = %d, want 1", st.Size)
	}
}
