package cas_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

func TestWalkerTraversal(t *testing.T) {
	ctx := context.Background()
	s, err := cas.New(mem.New(), jsoncodec.New[testNode](), "sha256")
	if err != nil {
		t.Fatal(err)
	}

	// Build a small graph: a -> b -> c (leaf).
	hc, err := s.Put(ctx, testNode{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.Put(ctx, testNode{Name: "b", Refs: []cas.Hash{hc}})
	if err != nil {
		t.Fatal(err)
	}
	ha, err := s.Put(ctx, testNode{Name: "a", Refs: []cas.Hash{hb}})
	if err != nil {
		t.Fatal(err)
	}

	var visited []string
	w := cas.NewWalker(s, func(n testNode) error {
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
	s, err := cas.New(mem.New(), jsoncodec.New[testNode](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	w := cas.NewWalker(s, func(testNode) error { return nil })
	if err := w.Walk(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk(missing) = %v, want ErrNotFound", err)
	}
}

func TestWalkerVisitError(t *testing.T) {
	ctx := context.Background()
	s, err := cas.New(mem.New(), jsoncodec.New[testNode](), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Put(ctx, testNode{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	w := cas.NewWalker(s, func(testNode) error { return sentinel })
	if err := w.Walk(ctx, h); !errors.Is(err, sentinel) {
		t.Fatalf("Walk = %v, want sentinel", err)
	}
}
