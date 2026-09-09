package prefetch_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	mem "github.com/dmundt/go-cask/cas/cache/mem"
	"github.com/dmundt/go-cask/cas/cache/prefetch"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

type testObject struct {
	Name string
	Refs []cas.Hash
}

func (testObject) Type() string             { return "test@1" }
func (o testObject) References() []cas.Hash { return o.Refs }

// MarshalJSON renders references as "algo:hex" strings so they survive
// serialization (cas.Hash is an interface over unexported fields).
func (o testObject) MarshalJSON() ([]byte, error) {
	refs := make([]string, len(o.Refs))
	for i, h := range o.Refs {
		refs[i] = h.String()
	}
	return json.Marshal(struct {
		Name string
		Refs []string
	}{o.Name, refs})
}

// UnmarshalJSON parses the string references back into Hash values.
func (o *testObject) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name string
		Refs []string
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.Name = raw.Name
	o.Refs = make([]cas.Hash, 0, len(raw.Refs))
	for _, s := range raw.Refs {
		h, err := cas.ParseHash(s)
		if err != nil {
			return err
		}
		o.Refs = append(o.Refs, h)
	}
	return nil
}

func newStore(t *testing.T) (*cas.Store[testObject], *mem.CachedStore[testObject]) {
	t.Helper()
	s, err := cas.New(backmem.New(), jsoncodec.New[testObject](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
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
	m, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := sc.GetWithPrefetch(ctx, m); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(missing) = %v", err)
	}
}

// waitCached polls until the given hash is loaded into the store's cache or
// the deadline passes. It lets the asynchronous prefetch goroutine finish.
func waitCached(t *testing.T, cs *mem.CachedStore[testObject], h cas.Hash) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if co := cs.Lookup(h.String()); co != nil && co.IsLoaded() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hash %s never became cached", h)
}

// TestSmartCachePrefetchReference warms a directly referenced object: the
// parent references the leaf, so GetWithPrefetch on the parent must
// asynchronously load the leaf into the cache.
func TestSmartCachePrefetchReference(t *testing.T) {
	ctx := context.Background()
	s, cs := newStore(t)
	leaf, _ := s.Put(ctx, testObject{Name: "leaf"})
	parent, _ := s.Put(ctx, testObject{Name: "parent", Refs: []cas.Hash{leaf}})
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
	parent, _ := s.Put(ctx, testObject{Name: "parent", Refs: []cas.Hash{leaf}})
	root, _ := s.Put(ctx, testObject{Name: "root", Refs: []cas.Hash{parent}})
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
	missing, _ := cas.ParseHash("sha256:1111111111111111111111111111111111111111111111111111111111111111")
	parent, _ := s.Put(ctx, testObject{Name: "parent", Refs: []cas.Hash{child, missing}})
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
