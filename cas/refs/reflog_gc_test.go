package refs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
	"github.com/dmundt/go-cask/internal/test"
)

// nodeLister adapts Store[test.Node] to cas.RefLister, the shape a typed layer
// satisfies so cas.Reachable can expand a root set without knowing the concrete
// object model.
type nodeLister struct{ store *cas.Store[test.Node] }

func (l nodeLister) References(ctx context.Context, d cas.Digest) ([]cas.Digest, error) {
	n, err := l.store.Get(ctx, d)
	if err != nil {
		return nil, err
	}
	return n.References(), nil
}

// reflogSweepFixture stores two revisions of one node and points "main" at both
// in turn, so the first digest lives only in the reflog.
type reflogSweepFixture struct {
	ctx       context.Context
	backend   *backmem.Backend
	store     *cas.Store[test.Node]
	refs      *refs.Store
	oldDigest cas.Digest
	newDigest cas.Digest
}

func newReflogSweepFixture(t *testing.T) reflogSweepFixture {
	t.Helper()
	f := reflogSweepFixture{
		ctx:     context.Background(),
		backend: backmem.New(),
	}
	f.store = cas.New(f.backend, jsoncodec.New[test.Node](), sha256.New())

	refStore, err := refs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.refs = refStore

	f.oldDigest = f.put(t, "old")
	f.newDigest = f.put(t, "new")
	if err := f.refs.Set(f.ctx, "main", f.oldDigest); err != nil {
		t.Fatal(err)
	}
	if err := f.refs.Set(f.ctx, "main", f.newDigest); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f reflogSweepFixture) put(t *testing.T, name string) cas.Digest {
	t.Helper()
	d, err := f.store.Put(f.ctx, test.Node{Name: name})
	if err != nil {
		t.Fatalf("Put(%s): %v", name, err)
	}
	return d
}

func (f reflogSweepFixture) sweep(t *testing.T, roots []cas.Digest) {
	t.Helper()
	reachable, err := cas.Reachable(f.ctx, nodeLister{f.store}, roots)
	if err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if _, err := cas.Sweep(f.ctx, f.backend, reachable, cas.SweepOptions{}); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
}

// TestSweepCollectsReflogOnlyHistory pins the contract consistency.md §4 and
// cas/refs/README.md now state: Roots is the *current* value of each ref, the
// reflog is history rather than a root source, and a sweep with the documented
// root set therefore collects a digest the log still names. Before the contract
// was written down, this outcome was silent.
func TestSweepCollectsReflogOnlyHistory(t *testing.T) {
	f := newReflogSweepFixture(t)

	// The documented root set: the current value of each ref, and nothing else.
	roots, err := f.refs.Roots(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || !roots[0].Equal(f.newDigest) {
		t.Fatalf("Roots = %v, want only the current value %s", roots, f.newDigest)
	}

	f.sweep(t, roots)

	if _, err := f.store.Get(f.ctx, f.newDigest); err != nil {
		t.Fatalf("the current revision must survive the sweep: %v", err)
	}
	if _, err := f.store.Get(f.ctx, f.oldDigest); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(reflog-only digest) = %v, want ErrNotFound: the reflog is not a root source", err)
	}

	// The history keeps handing back the collected digest — the trap the specs
	// now warn about, pinned so it cannot change unnoticed.
	previous, err := f.refs.Previous(f.ctx, "main")
	if err != nil {
		t.Fatalf("Previous: %v", err)
	}
	if !previous.Equal(f.oldDigest) {
		t.Fatalf("Previous = %s, want the collected %s", previous, f.oldDigest)
	}
}

// TestReflogDigestsInTheRootSetPreserveHistory pins the recovery recipe the
// specs give: a caller that wants a recovery window feeds the log's digests into
// the root set alongside Roots, and the sweep then keeps them.
func TestReflogDigestsInTheRootSetPreserveHistory(t *testing.T) {
	f := newReflogSweepFixture(t)

	roots, err := f.refs.Roots(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := f.refs.Log(f.ctx, "main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("Log returned no entries for a ref that was set twice")
	}
	for _, e := range entries {
		if !e.Digest.IsZero() {
			roots = append(roots, e.Digest)
		}
		if !e.Old.IsZero() {
			roots = append(roots, e.Old)
		}
	}

	f.sweep(t, roots)

	for _, d := range []cas.Digest{f.oldDigest, f.newDigest} {
		if _, err := f.store.Get(f.ctx, d); err != nil {
			t.Fatalf("Get(%s) after rooting the reflog = %v, want the object kept", d, err)
		}
	}
}
