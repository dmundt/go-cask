package cas_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/internal/test"
)

func TestWalkerTraversal(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node]())

	// Build a small graph: a -> b -> c (leaf).
	hc, err := s.Put(ctx, test.Node{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.Put(ctx, test.Node{Name: "b", Refs: []jsoncodec.Hash{jsoncodec.NewHash(hc)}})
	if err != nil {
		t.Fatal(err)
	}
	ha, err := s.Put(ctx, test.Node{Name: "a", Refs: []jsoncodec.Hash{jsoncodec.NewHash(hb)}})
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

func TestWalkerNotFound(t *testing.T) {
	s := cas.New(mem.New(), jsoncodec.New[test.Node]())
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	w := cas.NewWalker(s, func(test.Node) error { return nil })
	if err := w.Walk(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk(missing) = %v, want ErrNotFound", err)
	}
}

func TestWalkerVisitError(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node]())
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
	st := cas.New(mem.New(), jsoncodec.New[test.Node]())
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []jsoncodec.Hash{jsoncodec.NewHash(leafH)}})
	if err != nil {
		t.Fatal(err)
	}
	missingH := test.HashData([]byte("missing"))
	brokenH, err := st.Put(ctx, test.Node{Name: "broken", Refs: []jsoncodec.Hash{jsoncodec.NewHash(missingH)}})
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

// TestWalkerSharedSubgraphVisitedOnce pins the visited-set contract: a hash
// reached through two paths is visited once, not once per path.
func TestWalkerSharedSubgraphVisitedOnce(t *testing.T) {
	ctx := context.Background()
	st := cas.New(mem.New(), jsoncodec.New[test.Node]())
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	leftH, err := st.Put(ctx, test.Node{Name: "left", Refs: []jsoncodec.Hash{jsoncodec.NewHash(leafH)}})
	if err != nil {
		t.Fatal(err)
	}
	rightH, err := st.Put(ctx, test.Node{Name: "right", Refs: []jsoncodec.Hash{jsoncodec.NewHash(leafH)}})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []jsoncodec.Hash{jsoncodec.NewHash(leftH), jsoncodec.NewHash(rightH)}})
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
	st := cas.New(mem.New(), jsoncodec.New[test.Node]())
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []jsoncodec.Hash{jsoncodec.NewHash(leafH)}})
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
