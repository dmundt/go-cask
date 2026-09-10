package cas_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

func TestWalkerTraversal(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())

	// Build a small graph: a -> b -> c (leaf).
	hc, err := s.Put(ctx, test.Node{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.Put(ctx, test.Node{Name: "b", Refs: []cas.Digest{hc}})
	if err != nil {
		t.Fatal(err)
	}
	ha, err := s.Put(ctx, test.Node{Name: "a", Refs: []cas.Digest{hb}})
	if err != nil {
		t.Fatal(err)
	}

	var visited []string
	w := cas.NewWalker(s, func(n test.Node) error {
		visited = append(visited, n.Name)
		return nil
	})
	if err := w.Walk(ctx, ha); err != nil {
		t.Fatal(err)
	}
	if len(visited) != 3 {
		t.Fatalf("visited %d nodes, want 3: %v", len(visited), visited)
	}
	names := map[string]bool{}
	for _, n := range visited {
		names[n] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !names[want] {
			t.Errorf("node %q not visited", want)
		}
	}
}

// rawRefsObj returns its references verbatim, including an absent one: the
// object contract allows the zero Digest in References() to mean "no
// reference", so the walker must skip it rather than treat it as a missing
// object (the same rule WalkGraph follows).
type rawRefsObj struct {
	Name string
	Refs []cas.Digest
}

func (rawRefsObj) Type() string               { return "rawrefs@1" }
func (o rawRefsObj) References() []cas.Digest { return o.Refs }

// TestWalkerSkipsAbsentReferences pins that a zero reference does not fail the
// whole walk with ErrInvalidDigest.
func TestWalkerSkipsAbsentReferences(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[rawRefsObj](), sha256.New())
	child, err := s.Put(ctx, rawRefsObj{Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.Put(ctx, rawRefsObj{Name: "root", Refs: []cas.Digest{nil, child}})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	w := cas.NewWalker(s, func(o rawRefsObj) error {
		visited = append(visited, o.Name)
		return nil
	})
	if err := w.Walk(ctx, root); err != nil {
		t.Fatalf("Walk over an absent reference = %v, want nil", err)
	}
	if len(visited) != 2 {
		t.Fatalf("visited %v, want the root and its child", visited)
	}
}

func TestWalkerNotFound(t *testing.T) {
	s := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())
	missing := sha256.Of([]byte("never stored"))
	w := cas.NewWalker(s, func(test.Node) error { return nil })
	if err := w.Walk(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk(missing) = %v, want ErrNotFound", err)
	}
}

func TestWalkerVisitError(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())
	h, err := s.Put(ctx, test.Node{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	w := cas.NewWalker(s, func(test.Node) error { return sentinel })
	if err := w.Walk(ctx, h); !errors.Is(err, sentinel) {
		t.Fatalf("Walk = %v, want sentinel", err)
	}
}

// TestWalkerRecursionErrors covers walker behavior below the root: a missing
// reference mid-graph surfaces ErrNotFound, and a visit error from a child
// propagates.
func TestWalkerRecursionErrors(t *testing.T) {
	ctx := context.Background()
	st := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []cas.Digest{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	missingD := test.DigestData([]byte("missing"))
	brokenH, err := st.Put(ctx, test.Node{Name: "broken", Refs: []cas.Digest{missingD}})
	if err != nil {
		t.Fatal(err)
	}

	w := cas.NewWalker(st, func(test.Node) error { return nil })
	if err := w.Walk(ctx, brokenH); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk over broken ref = %v, want ErrNotFound", err)
	}

	seen := 0
	w2 := cas.NewWalker(st, func(o test.Node) error {
		seen++
		if o.References() == nil { // the leaf
			return errors.New("stop at leaf")
		}
		return nil
	})
	if err := w2.Walk(ctx, rootH); err == nil || err.Error() != "stop at leaf" {
		t.Fatalf("Walk child error = %v", err)
	}
	if seen != 2 {
		t.Fatalf("visited %d objects, want root+leaf = 2", seen)
	}
}

// TestWalkerSharedSubgraphVisitedOnce pins the visited-set contract: a digest
// reached through two paths is visited once, not once per path.
func TestWalkerSharedSubgraphVisitedOnce(t *testing.T) {
	ctx := context.Background()
	st := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	leftH, err := st.Put(ctx, test.Node{Name: "left", Refs: []cas.Digest{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	rightH, err := st.Put(ctx, test.Node{Name: "right", Refs: []cas.Digest{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []cas.Digest{leftH, rightH}})
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	w := cas.NewWalker(st, func(n test.Node) error {
		counts[n.Name]++
		return nil
	})
	if err := w.Walk(ctx, rootH); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 4 {
		t.Fatalf("visited %d distinct objects, want 4: %v", len(counts), counts)
	}
	if counts["leaf"] != 1 {
		t.Fatalf("shared leaf visited %d times, want 1", counts["leaf"])
	}
}

// TestWalkerVisitedSetKeyedByAddress pins that the visited set is keyed by the
// full address: two distinct objects never collide, and the walk terminates.
// (A cycle is not constructible through the public API: the core's one
// algorithm is sha256, so an address depends on the bytes that contain it.)
func TestWalkerVisitedSetKeyedByAddress(t *testing.T) {
	ctx := context.Background()
	st := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []cas.Digest{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	w := cas.NewWalker(st, func(n test.Node) error {
		seen[n.Name]++
		return nil
	})
	if err := w.Walk(ctx, rootH); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen["root"] != 1 || seen["leaf"] != 1 {
		t.Fatalf("visited %v, want root and leaf once each", seen)
	}
}
