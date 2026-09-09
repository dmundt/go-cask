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
	s, err := cas.New(mem.New(), jsoncodec.New[test.Node](), "sha256")
	if err != nil {
		t.Fatal(err)
	}

	// Build a small graph: a -> b -> c (leaf).
	hc, err := s.Put(ctx, test.Node{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.Put(ctx, test.Node{Name: "b", Refs: []cas.Hash{hc}})
	if err != nil {
		t.Fatal(err)
	}
	ha, err := s.Put(ctx, test.Node{Name: "a", Refs: []cas.Hash{hb}})
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
	s, err := cas.New(mem.New(), jsoncodec.New[test.Node](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	w := cas.NewWalker(s, func(test.Node) error { return nil })
	if err := w.Walk(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk(missing) = %v, want ErrNotFound", err)
	}
}

func TestWalkerVisitError(t *testing.T) {
	ctx := context.Background()
	s, err := cas.New(mem.New(), jsoncodec.New[test.Node](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
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
	st, err := cas.New(mem.New(), jsoncodec.New[test.Node](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	leafH, err := st.Put(ctx, test.Node{Name: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	rootH, err := st.Put(ctx, test.Node{Name: "root", Refs: []cas.Hash{leafH}})
	if err != nil {
		t.Fatal(err)
	}
	missingH, _ := test.HashData("sha256", []byte("missing"))
	brokenH, err := st.Put(ctx, test.Node{Name: "broken", Refs: []cas.Hash{missingH}})
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
