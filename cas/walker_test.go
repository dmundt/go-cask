package cas_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

func TestWalkerTraversal(t *testing.T) {
	ctx := context.Background()
	s := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())

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
	s := cas.New(backmem.New(), jsoncodec.New[rawRefsObj](), sha256.New())
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
	s := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())
	missing := sha256.Of([]byte("never stored"))
	w := cas.NewWalker(s, func(test.Node) error { return nil })
	if err := w.Walk(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk(missing) = %v, want ErrNotFound", err)
	}
}

func TestWalkerVisitError(t *testing.T) {
	ctx := context.Background()
	s := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())
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
	st := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())
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
	st := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())
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
	st := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())
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

// TestWalkDigestsMatchesWalker walks the same graph through both public
// entries — Walker[T], the typed adapter, and WalkDigests, the shared
// primitive — and requires the same visit order and the same at-most-once rule.
// The two share one implementation now (go-cask#319), so this pins the
// observable contract a future refactor could break silently: if either walk
// changed its stack discipline or its visited rule, the orders would diverge
// here rather than in a subtle behavioral difference no test names.
func TestWalkDigestsMatchesWalker(t *testing.T) {
	ctx := context.Background()
	st := cas.New(backmem.New(), jsoncodec.New[test.Node](), sha256.New())

	// A diamond with two leaves sharing one child, so reference order and the
	// visited set both matter: a, b -> {shared, leafX} each, and a -> b too.
	shared, err := st.Put(ctx, test.Node{Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	leafX, err := st.Put(ctx, test.Node{Name: "leafX"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Put(ctx, test.Node{Name: "b", Refs: []cas.Digest{shared, leafX}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Put(ctx, test.Node{Name: "a", Refs: []cas.Digest{b, shared, leafX}})
	if err != nil {
		t.Fatal(err)
	}

	viaWalker := make([]string, 0, 4)
	w := cas.NewWalker(st, func(n test.Node) error {
		viaWalker = append(viaWalker, n.Name)
		return nil
	})
	if err := w.Walk(ctx, a); err != nil {
		t.Fatalf("Walker.Walk: %v", err)
	}

	viaPrimitive := make([]string, 0, 4)
	resolve := func(ctx context.Context, d cas.Digest) (cas.Node, []cas.Digest, error) {
		obj, err := st.Get(ctx, d)
		if err != nil {
			return nil, nil, err
		}
		return obj, nil, nil // nil refs: the walk asks the node
	}
	err = cas.WalkDigests(ctx, resolve, []cas.Digest{a}, func(_ cas.Digest, node cas.Node, _ []cas.Digest) error {
		viaPrimitive = append(viaPrimitive, node.(test.Node).Name)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDigests: %v", err)
	}

	if len(viaWalker) != 4 || len(viaPrimitive) != 4 {
		t.Fatalf("visited %v via Walker and %v via WalkDigests, want 4 objects each", viaWalker, viaPrimitive)
	}
	if !slices.Equal(viaWalker, viaPrimitive) {
		t.Fatalf("Walker visited %v, WalkDigests %v — the shared traversal must agree", viaWalker, viaPrimitive)
	}
}

// TestWalkDigestsSkipsAbsentReferencesAndZeroRoots pins the zero-Digest rule at
// the primitive: an absent reference is "no reference", never a digest to
// resolve, whether it arrives as a root or inside References().
func TestWalkDigestsSkipsAbsentReferencesAndZeroRoots(t *testing.T) {
	ctx := context.Background()
	st := cas.New(backmem.New(), jsoncodec.New[rawRefsObj](), sha256.New())
	child, err := st.Put(ctx, rawRefsObj{Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := st.Put(ctx, rawRefsObj{Name: "root", Refs: []cas.Digest{nil, child}})
	if err != nil {
		t.Fatal(err)
	}

	var visited []string
	resolve := func(ctx context.Context, d cas.Digest) (cas.Node, []cas.Digest, error) {
		obj, err := st.Get(ctx, d)
		if err != nil {
			return nil, nil, err
		}
		return obj, nil, nil
	}
	err = cas.WalkDigests(ctx, resolve, []cas.Digest{nil, root}, func(_ cas.Digest, node cas.Node, _ []cas.Digest) error {
		visited = append(visited, node.(rawRefsObj).Name)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDigests over an absent root/reference = %v, want nil", err)
	}
	if len(visited) != 2 {
		t.Fatalf("visited %v, want the root and its child only", visited)
	}
}
