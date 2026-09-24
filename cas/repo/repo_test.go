package repo_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/repo"
)

// Three independent test object types, so a graph can span more than one
// type: a leaf with no references, a branch that references leaves (or other
// branches), and a root that references exactly one branch. Each gets its own
// cas.Store[T] and its own type name, matching how a real, several-type
// application would be laid out (gitlike's Blob/Tree/Commit/Tag is the same
// shape, just fixed at four types).

type leaf struct {
	Value string `json:"value"`
}

func (leaf) Type() string             { return "leaf@1" }
func (leaf) References() []cas.Digest { return nil }

type branch struct {
	Label    string       `json:"label"`
	Children []cas.Digest `json:"children,omitempty"`
}

func (branch) Type() string { return "branch@1" }
func (b branch) References() []cas.Digest {
	refs := make([]cas.Digest, 0, len(b.Children))
	for _, d := range b.Children {
		if !d.IsZero() {
			refs = append(refs, d)
		}
	}
	return refs
}

type root struct {
	Name   string     `json:"name"`
	Branch cas.Digest `json:"branch,omitempty"`
}

func (root) Type() string { return "root@1" }
func (r root) References() []cas.Digest {
	if r.Branch.IsZero() {
		return nil
	}
	return []cas.Digest{r.Branch}
}

// testStores bundles the three per-type stores plus a Registry with all three
// registered, over a shared backmem.Backend.
type testStores struct {
	backend  *backmem.Backend
	leaves   *cas.Store[leaf]
	branches *cas.Store[branch]
	roots    *cas.Store[root]
	reg      *repo.Registry
}

func newTestStores(t *testing.T) *testStores {
	t.Helper()
	backend := backmem.New()
	hasher := sha256.New()
	ts := &testStores{
		backend:  backend,
		leaves:   cas.New(backend, jsoncodec.New[leaf](), hasher),
		branches: cas.New(backend, jsoncodec.New[branch](), hasher),
		roots:    cas.New(backend, jsoncodec.New[root](), hasher),
		reg:      repo.NewRegistry(backend, hasher),
	}
	if err := repo.RegisterStore(ts.reg, "leaf@1", ts.leaves); err != nil {
		t.Fatalf("register leaf: %v", err)
	}
	if err := repo.RegisterStore(ts.reg, "branch@1", ts.branches); err != nil {
		t.Fatalf("register branch: %v", err)
	}
	if err := repo.RegisterStore(ts.reg, "root@1", ts.roots); err != nil {
		t.Fatalf("register root: %v", err)
	}
	return ts
}

func TestWalkVisitsEveryObjectOnceAcrossThreeTypes(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	hl1, err := ts.leaves.Put(ctx, leaf{Value: "l1"})
	if err != nil {
		t.Fatal(err)
	}
	hl2, err := ts.leaves.Put(ctx, leaf{Value: "l2"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ts.branches.Put(ctx, branch{Label: "b", Children: []cas.Digest{hl1, hl2, hl1}}) // hl1 shared twice
	if err != nil {
		t.Fatal(err)
	}
	hr, err := ts.roots.Put(ctx, root{Name: "r", Branch: hb})
	if err != nil {
		t.Fatal(err)
	}

	visited := make(map[string]int)
	var types []string
	err = repo.Walk(ctx, ts.reg, []cas.Digest{hr}, func(d cas.Digest, obj repo.Object) error {
		visited[d.String()]++
		types = append(types, obj.Type())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []cas.Digest{hr, hb, hl1, hl2}
	if len(visited) != len(want) {
		t.Fatalf("visited %d digests, want %d: %v", len(visited), len(want), visited)
	}
	for _, d := range want {
		if visited[d.String()] != 1 {
			t.Errorf("digest %s visited %d times, want exactly once", d, visited[d.String()])
		}
	}
	wantTypes := map[string]bool{"root@1": true, "branch@1": true, "leaf@1": true}
	for _, typ := range types {
		if !wantTypes[typ] {
			t.Errorf("unexpected type visited: %q", typ)
		}
	}
	if len(types) < 3 {
		t.Fatalf("expected at least 3 distinct-type visits, got %v", types)
	}
}

// TestWalkTerminatesOnCycle pins termination on a store this library did not
// write: the Backend stores bytes without re-verifying their digest, so two
// hand-written branches can reference each other. The visited set makes the
// walk stop after each digest once instead of looping forever.
func TestWalkTerminatesOnCycle(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	hasher := sha256.New()
	reg := repo.NewRegistry(backend, hasher)
	branches := cas.New(backend, jsoncodec.New[branch](), hasher)
	if err := repo.RegisterStore(reg, "branch@1", branches); err != nil {
		t.Fatal(err)
	}

	dA := mustDigest(t, strings.Repeat("aa", 32))
	dB := mustDigest(t, strings.Repeat("bb", 32))
	storeEnvelope(t, backend, dA, "branch@1", branch{Label: "a", Children: []cas.Digest{dB}})
	storeEnvelope(t, backend, dB, "branch@1", branch{Label: "b", Children: []cas.Digest{dA}})

	visits := 0
	err := repo.Walk(ctx, reg, []cas.Digest{dA}, func(cas.Digest, repo.Object) error {
		visits++
		return nil
	})
	if err != nil {
		t.Fatalf("walk over a cyclic store = %v", err)
	}
	if visits != 2 {
		t.Fatalf("visited %d objects, want 2 (the cycle closes after both)", visits)
	}
}

func TestRegisterDuplicateTypeNameFails(t *testing.T) {
	backend := backmem.New()
	hasher := sha256.New()
	reg := repo.NewRegistry(backend, hasher)
	leaves := cas.New(backend, jsoncodec.New[leaf](), hasher)

	if err := repo.RegisterStore(reg, "leaf@1", leaves); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := repo.RegisterStore(reg, "leaf@1", leaves)
	if err == nil {
		t.Fatal("second register with the same type name = nil, want an error")
	}
	if !contains(err.Error(), "leaf@1") {
		t.Fatalf("duplicate error %q does not name the colliding type", err.Error())
	}
}

func TestRegistryResolveUnknownTypeReturnsErrUnknownType(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)
	hl, err := ts.leaves.Put(ctx, leaf{Value: "l"})
	if err != nil {
		t.Fatal(err)
	}

	// A registry that never registered "leaf@1".
	other := repo.NewRegistry(ts.backend, sha256.New())
	obj, err := other.Resolve(ctx, hl)
	if obj != nil {
		t.Fatalf("Resolve for an unregistered type returned a non-nil object: %v", obj)
	}
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Resolve error = %v, want wrapping cas.ErrUnknownType", err)
	}
	var ute *repo.UnknownTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("Resolve error = %v, want an *UnknownTypeError", err)
	}
	if ute.TypeName != "leaf@1" {
		t.Fatalf("UnknownTypeError.TypeName = %q, want %q", ute.TypeName, "leaf@1")
	}
}

// TestWalkReportsUnknownTypeWithoutAborting builds a branch that references
// one known leaf and one digest whose stored type has no decoder. Walk must
// report the unknown one to visit (as an *UnknownObject) and keep going to the
// sibling leaf, rather than aborting the whole walk.
func TestWalkReportsUnknownTypeWithoutAborting(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	hl, err := ts.leaves.Put(ctx, leaf{Value: "known"})
	if err != nil {
		t.Fatal(err)
	}
	dUnknown := mustDigest(t, strings.Repeat("cc", 32))
	storeEnvelope(t, ts.backend, dUnknown, "mystery@1", map[string]string{"x": "y"})

	hb, err := ts.branches.Put(ctx, branch{Label: "b", Children: []cas.Digest{hl, dUnknown}})
	if err != nil {
		t.Fatal(err)
	}

	var unknownSeen bool
	var leafSeen bool
	err = repo.Walk(ctx, ts.reg, []cas.Digest{hb}, func(d cas.Digest, obj repo.Object) error {
		switch o := obj.(type) {
		case *repo.UnknownObject:
			unknownSeen = true
			if o.Digest.String() != dUnknown.String() {
				t.Errorf("UnknownObject.Digest = %s, want %s", o.Digest, dUnknown)
			}
			if o.TypeName != "mystery@1" {
				t.Errorf("UnknownObject.TypeName = %q, want %q", o.TypeName, "mystery@1")
			}
			if !errors.Is(o.Err, cas.ErrUnknownType) {
				t.Errorf("UnknownObject.Err = %v, want wrapping cas.ErrUnknownType", o.Err)
			}
		case leaf:
			leafSeen = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk with an unknown-type reference = %v, want no error (it must not abort)", err)
	}
	if !unknownSeen {
		t.Fatal("Walk never reported the unknown-type digest to visit")
	}
	if !leafSeen {
		t.Fatal("Walk did not continue to the sibling leaf after the unknown-type digest")
	}
}

// TestWalkAbortsOnMissingReference pins the opposite behavior from unknown
// types: a reference to a digest that does not exist in the backend at all is
// a broken store, and Walk must surface it as a named error rather than
// silently stopping.
func TestWalkAbortsOnMissingReference(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	dMissing := mustDigest(t, strings.Repeat("dd", 32))
	hb, err := ts.branches.Put(ctx, branch{Label: "b", Children: []cas.Digest{dMissing}})
	if err != nil {
		t.Fatal(err)
	}

	err = repo.Walk(ctx, ts.reg, []cas.Digest{hb}, func(cas.Digest, repo.Object) error { return nil })
	if !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Walk with a missing reference = %v, want wrapping cas.ErrNotFound", err)
	}
}

// TestWalkAbortsOnCorruptEnvelope keeps damage in the same class as a missing
// reference: a digest whose stored bytes do not parse is not an unknown type, so
// Walk must abort with ErrCorrupt instead of reporting it to visit as an
// *UnknownObject and carrying on. Treating it as "just another type" is what
// lets a maintenance pass skip — and then delete — an object it cannot read.
func TestWalkAbortsOnCorruptEnvelope(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	dCorrupt := mustDigest(t, strings.Repeat("ee", 32))
	if err := ts.backend.Put(ctx, dCorrupt, bytes.NewReader([]byte{0xff})); err != nil {
		t.Fatal(err)
	}
	hb, err := ts.branches.Put(ctx, branch{Label: "b", Children: []cas.Digest{dCorrupt}})
	if err != nil {
		t.Fatal(err)
	}

	var unknownSeen bool
	err = repo.Walk(ctx, ts.reg, []cas.Digest{hb}, func(_ cas.Digest, obj repo.Object) error {
		if _, ok := obj.(*repo.UnknownObject); ok {
			unknownSeen = true
		}
		return nil
	})
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Walk over a corrupt envelope = %v, want wrapping cas.ErrCorrupt", err)
	}
	if unknownSeen {
		t.Fatal("Walk reported a corrupt object as an unknown type instead of aborting")
	}
}

// TestReachableBuildsRootSetForGCAcrossTypes exercises Reachable end to end as
// the documented way to build the GC/Prune argument: everything not in the
// reachable set from the live root is deleted, and everything reachable
// survives, across all three object types.
func TestReachableBuildsRootSetForGCAcrossTypes(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	hl1, err := ts.leaves.Put(ctx, leaf{Value: "kept-leaf"})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ts.branches.Put(ctx, branch{Label: "kept-branch", Children: []cas.Digest{hl1}})
	if err != nil {
		t.Fatal(err)
	}
	hr, err := ts.roots.Put(ctx, root{Name: "kept-root", Branch: hb})
	if err != nil {
		t.Fatal(err)
	}
	// An orphan, unreferenced from the live root.
	hOrphan, err := ts.leaves.Put(ctx, leaf{Value: "orphan"})
	if err != nil {
		t.Fatal(err)
	}

	reachable, err := repo.Reachable(ctx, ts.reg, []cas.Digest{hr})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []cas.Digest{hr, hb, hl1} {
		if !reachable[d.String()] {
			t.Errorf("Reachable missing kept digest %s", d)
		}
	}
	if reachable[hOrphan.String()] {
		t.Fatal("Reachable unexpectedly included the orphan")
	}

	// Simulate GC: delete everything the backend holds that Reachable did not
	// mark, then confirm the kept objects are still there and the orphan is
	// gone.
	all, err := ts.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range all {
		if !reachable[d.String()] {
			if err := ts.backend.Delete(ctx, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := ts.roots.Get(ctx, hr); err != nil {
		t.Fatalf("kept root did not survive GC: %v", err)
	}
	if _, err := ts.leaves.Get(ctx, hOrphan); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("orphan survived GC: err = %v, want cas.ErrNotFound", err)
	}
}

func TestReachableTreatsUnknownTypeAsReachableLeaf(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	dUnknown := mustDigest(t, strings.Repeat("ee", 32))
	storeEnvelope(t, ts.backend, dUnknown, "mystery@1", map[string]string{"x": "y"})
	hb, err := ts.branches.Put(ctx, branch{Label: "b", Children: []cas.Digest{dUnknown}})
	if err != nil {
		t.Fatal(err)
	}

	reachable, err := repo.Reachable(ctx, ts.reg, []cas.Digest{hb})
	if err != nil {
		t.Fatal(err)
	}
	if !reachable[dUnknown.String()] {
		t.Fatal("Reachable did not include the unknown-type digest — GC would delete it")
	}
}

// TestLookupStoreReturnsRegisteredStore pins the happy path: the exact
// *cas.Store[T] RegisterStore recorded comes back, same pointer, no assertion.
func TestLookupStoreReturnsRegisteredStore(t *testing.T) {
	ts := newTestStores(t)

	got, err := repo.LookupStore[leaf](ts.reg, "leaf@1")
	if err != nil {
		t.Fatalf("LookupStore(leaf@1) = %v", err)
	}
	if got != ts.leaves {
		t.Fatal("LookupStore did not return the exact store RegisterStore recorded")
	}
}

// TestLookupStoreUnknownTypeReturnsErrUnknownType pins the first failure
// outcome: a type name nothing registered is the same typed error Resolve
// returns, never a nil store.
func TestLookupStoreUnknownTypeReturnsErrUnknownType(t *testing.T) {
	ts := newTestStores(t)

	got, err := repo.LookupStore[leaf](ts.reg, "mystery@1")
	if got != nil {
		t.Fatalf("LookupStore for an unregistered type returned a non-nil store: %v", got)
	}
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("LookupStore error = %v, want wrapping cas.ErrUnknownType", err)
	}
	var ute *repo.UnknownTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("LookupStore error = %v, want an *UnknownTypeError", err)
	}
	if ute.Unwrap() != cas.ErrUnknownType {
		t.Fatalf("UnknownTypeError.Unwrap() = %v, want cas.ErrUnknownType", ute.Unwrap())
	}
	if ute.TypeName != "mystery@1" {
		t.Fatalf("UnknownTypeError.TypeName = %q, want %q", ute.TypeName, "mystery@1")
	}
}

// TestLookupStoreRegisterOnlyTypeReturnsErrUnknownType pins the documented
// boundary: Register records a Decoder but no store, so LookupStore has
// nothing to hand back and reports the same typed error an unknown name does.
func TestLookupStoreRegisterOnlyTypeReturnsErrUnknownType(t *testing.T) {
	reg := repo.NewRegistry(backmem.New(), sha256.New())
	if err := reg.Register("x@1", func(context.Context, cas.Backend, cas.Digest) (repo.Object, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.LookupStore[leaf](reg, "x@1")
	if got != nil || !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("LookupStore for a Register-only type = (%v, %v), want (nil, wrapping cas.ErrUnknownType)", got, err)
	}
}

// TestLookupStoreWrongTypeReturnsNamingError pins the second failure outcome:
// asking for a type name under the wrong T is an error naming both types, not
// a failed type assertion handed back to the caller.
func TestLookupStoreWrongTypeReturnsNamingError(t *testing.T) {
	ts := newTestStores(t)

	got, err := repo.LookupStore[branch](ts.reg, "leaf@1")
	if got != nil {
		t.Fatalf("LookupStore with the wrong T returned a non-nil store: %v", got)
	}
	if err == nil {
		t.Fatal("LookupStore with the wrong T = nil error, want an error naming both types")
	}
	if errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("LookupStore with the wrong T = %v, want a type-mismatch error, not cas.ErrUnknownType", err)
	}
	for _, want := range []string{"leaf@1", "repo_test.leaf", "repo_test.branch"} {
		if !contains(err.Error(), want) {
			t.Fatalf("LookupStore error %q does not name %q", err.Error(), want)
		}
	}
}

func TestRegisterRejectsEmptyNameAndNilDecoder(t *testing.T) {
	reg := repo.NewRegistry(backmem.New(), sha256.New())
	if err := reg.Register("", func(context.Context, cas.Backend, cas.Digest) (repo.Object, error) { return nil, nil }); err == nil {
		t.Fatal("Register with an empty type name = nil, want an error")
	}
	if err := reg.Register("x@1", nil); err == nil {
		t.Fatal("Register with a nil decoder = nil, want an error")
	}
}

func TestWalkRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ts := newTestStores(t)
	root := mustDigest(t, strings.Repeat("ff", 32))
	err := repo.Walk(ctx, ts.reg, []cas.Digest{root}, func(cas.Digest, repo.Object) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Walk(canceled ctx) = %v, want context.Canceled", err)
	}
}

func TestWalkIgnoresAbsentRoots(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)
	visits := 0
	err := repo.Walk(ctx, ts.reg, []cas.Digest{nil}, func(cas.Digest, repo.Object) error {
		visits++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visits != 0 {
		t.Fatalf("Walk visited %d digests for an absent-only root list, want 0", visits)
	}
}

func TestUnknownTypeErrorMessage(t *testing.T) {
	d := mustDigest(t, strings.Repeat("11", 32))
	err := &repo.UnknownTypeError{Digest: d, TypeName: "mystery@1"}
	if !contains(err.Error(), "mystery@1") || !contains(err.Error(), d.String()) {
		t.Fatalf("UnknownTypeError.Error() = %q, want it to name both the digest and the type", err.Error())
	}
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("UnknownTypeError does not unwrap to cas.ErrUnknownType")
	}
	// A lookup by type name has no digest: the message names the type only.
	lookupMsg := (&repo.UnknownTypeError{TypeName: "mystery@1"}).Error()
	if lookupMsg != `cas/repo: unknown type "mystery@1"` {
		t.Fatalf("UnknownTypeError.Error() without a digest = %q, want the type name only", lookupMsg)
	}
}

func TestUnknownObjectMethods(t *testing.T) {
	u := &repo.UnknownObject{TypeName: "mystery@1"}
	if u.Type() != "mystery@1" {
		t.Fatalf("UnknownObject.Type() = %q, want %q", u.Type(), "mystery@1")
	}
	if u.References() != nil {
		t.Fatalf("UnknownObject.References() = %v, want nil", u.References())
	}
}

func TestRegisterStoreRejectsNilStore(t *testing.T) {
	reg := repo.NewRegistry(backmem.New(), sha256.New())
	if err := repo.RegisterStore[leaf](reg, "leaf@1", nil); err == nil {
		t.Fatal("RegisterStore with a nil store = nil, want an error")
	}
}

// TestResolveRejectsInvalidDigest pins the CheckDigest/hasher.Validate guards
// Resolve applies before ever touching the backend: an absent digest and a
// wrong-length digest are both rejected without a lookup.
func TestResolveRejectsInvalidDigest(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)

	if _, err := ts.reg.Resolve(ctx, nil); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Resolve(absent digest) error = %v, want cas.ErrInvalidDigest", err)
	}
	shortDigest := cas.NewDigest([]byte{0x01, 0x02, 0x03})
	if _, err := ts.reg.Resolve(ctx, shortDigest); err == nil {
		t.Fatal("Resolve(wrong-length digest) = nil error, want a hasher validation error")
	}
}

// TestResolveWrapsMalformedEnvelope pins the path where the stored bytes do
// not even parse as a TLV envelope: that is damage, so Resolve reports
// ErrCorrupt rather than ErrUnknownType — the sentinel a caller uses for "a
// type I have no decoder for". A caller that classified damage as an unknown
// type would treat an unreadable object as one it can safely skip.
func TestResolveWrapsMalformedEnvelope(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)
	d := mustDigest(t, strings.Repeat("22", 32))
	if err := ts.backend.Put(ctx, d, bytes.NewReader([]byte{0xff})); err != nil {
		t.Fatal(err)
	}
	_, err := ts.reg.Resolve(ctx, d)
	if !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Resolve(malformed envelope) error = %v, want wrapping cas.ErrCorrupt", err)
	}
	if errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Resolve(malformed envelope) error = %v, must not be reported as an unknown type", err)
	}
	var ute *repo.UnknownTypeError
	if errors.As(err, &ute) {
		t.Fatalf("Resolve(malformed envelope) error = %v, want no *UnknownTypeError", err)
	}
}

// TestResolveKeepsUnknownTypeForAnIntactEnvelope is the other half of the
// contract above, asserted through the same Registry: a well-formed frame that
// names a type nothing registered is ErrUnknownType (an *UnknownTypeError),
// never ErrCorrupt.
func TestResolveKeepsUnknownTypeForAnIntactEnvelope(t *testing.T) {
	ctx := context.Background()
	ts := newTestStores(t)
	d := mustDigest(t, strings.Repeat("23", 32))
	storeEnvelope(t, ts.backend, d, "mystery@1", map[string]string{"x": "y"})

	_, err := ts.reg.Resolve(ctx, d)
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("Resolve(intact envelope, unregistered type) error = %v, want wrapping cas.ErrUnknownType", err)
	}
	if errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("Resolve(intact envelope, unregistered type) error = %v, must not report damage", err)
	}
	var ute *repo.UnknownTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("Resolve error = %v, want an *UnknownTypeError", err)
	}
	if ute.TypeName != "mystery@1" {
		t.Fatalf("UnknownTypeError.TypeName = %q, want mystery@1", ute.TypeName)
	}
}

// TestResolvePropagatesDecoderError and TestResolveRejectsNilObjectFromDecoder
// pin Resolve's handling of a registered Decoder that itself misbehaves: one
// that errors, and one that returns a nil Object with a nil error.
func TestResolvePropagatesDecoderError(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	hasher := sha256.New()
	reg := repo.NewRegistry(backend, hasher)
	wantErr := errors.New("decode exploded")
	if err := reg.Register("broken@1", func(context.Context, cas.Backend, cas.Digest) (repo.Object, error) {
		return nil, wantErr
	}); err != nil {
		t.Fatal(err)
	}
	d := mustDigest(t, strings.Repeat("33", 32))
	storeEnvelope(t, backend, d, "broken@1", map[string]string{})
	if _, err := reg.Resolve(ctx, d); !errors.Is(err, wantErr) {
		t.Fatalf("Resolve error = %v, want wrapping %v", err, wantErr)
	}
}

func TestResolveRejectsNilObjectFromDecoder(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	hasher := sha256.New()
	reg := repo.NewRegistry(backend, hasher)
	if err := reg.Register("empty@1", func(context.Context, cas.Backend, cas.Digest) (repo.Object, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	d := mustDigest(t, strings.Repeat("44", 32))
	storeEnvelope(t, backend, d, "empty@1", map[string]string{})
	if _, err := reg.Resolve(ctx, d); err == nil {
		t.Fatal("Resolve for a decoder returning (nil, nil) = nil error, want an error")
	}
}

// erroringBackend wraps a real Backend but replaces Get's returned reader with
// one whose Read or Close fails, so readEnvelopeHeader's own error paths (not
// reachable through a well-behaved Backend) can be exercised directly.
type erroringBackend struct {
	cas.Backend
	failRead  bool
	failClose bool
}

type erroringReadCloser struct {
	failRead  bool
	failClose bool
}

func (e erroringReadCloser) Read([]byte) (int, error) {
	if e.failRead {
		return 0, errors.New("read exploded")
	}
	return 0, io.EOF
}

func (e erroringReadCloser) Close() error {
	if e.failClose {
		return errors.New("close exploded")
	}
	return nil
}

func (b erroringBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	if b.failRead || b.failClose {
		return erroringReadCloser{failRead: b.failRead, failClose: b.failClose}, nil
	}
	return b.Backend.Get(ctx, d)
}

func TestResolveWrapsReadHeaderFailure(t *testing.T) {
	ctx := context.Background()
	hasher := sha256.New()
	reg := repo.NewRegistry(erroringBackend{Backend: backmem.New(), failRead: true}, hasher)
	d := mustDigest(t, strings.Repeat("55", 32))
	if _, err := reg.Resolve(ctx, d); err == nil || !contains(err.Error(), "read object header") {
		t.Fatalf("Resolve error = %v, want a wrapped read-header failure", err)
	}
}

func TestResolveWrapsCloseHeaderFailure(t *testing.T) {
	ctx := context.Background()
	hasher := sha256.New()
	reg := repo.NewRegistry(erroringBackend{Backend: backmem.New(), failClose: true}, hasher)
	d := mustDigest(t, strings.Repeat("66", 32))
	if _, err := reg.Resolve(ctx, d); err == nil || !contains(err.Error(), "close object header reader") {
		t.Fatalf("Resolve error = %v, want a wrapped close-header failure", err)
	}
}

// --- Helpers ---

func mustDigest(t *testing.T, hexDigest string) cas.Digest {
	t.Helper()
	d, err := cas.ParseDigest(hexDigest)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

// storeEnvelope writes a hand-built TLV envelope directly into backend at d,
// bypassing the codec/Store layer entirely (test helper: production
// serialization always goes through a Store's Codec).
func storeEnvelope(t *testing.T, backend cas.Backend, d cas.Digest, typeName string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteByte(1) // version
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	n = binary.PutUvarint(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:n])
	buf.Write(data)
	if err := backend.Put(context.Background(), d, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
}
