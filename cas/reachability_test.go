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

// nodeLister adapts a Store[test.Node] to cas.RefLister, the shape a
// typed layer needs to satisfy so cas.Reachable can expand roots without
// knowing the concrete object model.
type nodeLister struct{ store *cas.Store[test.Node] }

func (l nodeLister) References(ctx context.Context, d cas.Digest) ([]cas.Digest, error) {
	n, err := l.store.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	return n.References(), nil
}

func TestReachableExpandsRootsAcrossTheGraph(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())

	// a -> b -> c (leaf); a shared leaf d reachable from both a and b.
	hd, err := s.Put(ctx, test.Node{Name: "d"})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := s.Put(ctx, test.Node{Name: "c", Refs: []cas.Digest{hd}})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.Put(ctx, test.Node{Name: "b", Refs: []cas.Digest{hc, hd}})
	if err != nil {
		t.Fatal(err)
	}
	ha, err := s.Put(ctx, test.Node{Name: "a", Refs: []cas.Digest{hb}})
	if err != nil {
		t.Fatal(err)
	}

	reachable, err := cas.Reachable(ctx, nodeLister{s}, []cas.Digest{ha})
	if err != nil {
		t.Fatal(err)
	}
	want := []cas.Digest{ha, hb, hc, hd}
	if len(reachable) != len(want) {
		t.Fatalf("Reachable = %d digests, want %d: %v", len(reachable), len(want), reachable)
	}
	for _, d := range want {
		if !reachable[d.String()] {
			t.Errorf("Reachable missing %s", d)
		}
	}
}

func TestReachableVisitsSharedDigestOnce(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())

	hc, err := s.Put(ctx, test.Node{Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	// Two roots both reference the same leaf.
	ha, err := s.Put(ctx, test.Node{Name: "a", Refs: []cas.Digest{hc}})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.Put(ctx, test.Node{Name: "b", Refs: []cas.Digest{hc}})
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	counting := cas.RefListerFunc(func(ctx context.Context, d cas.Digest) ([]cas.Digest, error) {
		calls++
		return nodeLister{s}.References(ctx, d)
	})
	reachable, err := cas.Reachable(ctx, counting, []cas.Digest{ha, hb})
	if err != nil {
		t.Fatal(err)
	}
	if len(reachable) != 3 {
		t.Fatalf("Reachable = %v, want {a,b,c}", reachable)
	}
	if calls != 3 {
		t.Fatalf("References called %d times, want 3 (each digest expanded once)", calls)
	}
}

func TestReachableIgnoresAbsentReferences(t *testing.T) {
	ctx := context.Background()
	s := cas.New(mem.New(), jsoncodec.New[test.Node](), sha256.New())
	ha, err := s.Put(ctx, test.Node{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	reachable, err := cas.Reachable(ctx, nodeLister{s}, []cas.Digest{ha, nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(reachable) != 1 || !reachable[ha.String()] {
		t.Fatalf("Reachable(a, absent) = %v, want {a}", reachable)
	}
}

func TestReachablePropagatesExpansionError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("boom")
	failing := cas.RefListerFunc(func(context.Context, cas.Digest) ([]cas.Digest, error) {
		return nil, wantErr
	})
	root := test.DigestData([]byte("root"))
	if _, err := cas.Reachable(ctx, failing, []cas.Digest{root}); !errors.Is(err, wantErr) {
		t.Fatalf("Reachable error = %v, want wrapping %v", err, wantErr)
	}
}

func TestReachableRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := test.DigestData([]byte("root"))
	lister := cas.RefListerFunc(func(context.Context, cas.Digest) ([]cas.Digest, error) {
		return nil, nil
	})
	if _, err := cas.Reachable(ctx, lister, []cas.Digest{root}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reachable(canceled ctx) = %v, want context.Canceled", err)
	}
}
