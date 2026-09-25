package gitlike

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256hash "github.com/dmundt/go-cask/cas/hash/sha256"
	casrepo "github.com/dmundt/go-cask/cas/repo"
)

// closerBackend is an in-memory store that also implements io.Closer, so the
// repository's Close chain can be observed: Store.Close forwards to it exactly
// once and reports its error.
type closerBackend struct {
	inner cas.Backend
	close func() error

	closes int
}

func (b *closerBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	return b.inner.Put(ctx, d, r)
}

func (b *closerBackend) Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error) {
	return b.inner.Get(ctx, d)
}

func (b *closerBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	return b.inner.Exists(ctx, d)
}

func (b *closerBackend) Delete(ctx context.Context, d cas.Digest) error {
	return b.inner.Delete(ctx, d)
}

func (b *closerBackend) List(ctx context.Context) ([]cas.Digest, error) {
	return b.inner.List(ctx)
}

func (b *closerBackend) Stats(ctx context.Context) (*cas.Stats, error) {
	return b.inner.Stats(ctx)
}

func (b *closerBackend) Close() error {
	b.closes++
	if b.close != nil {
		return b.close()
	}
	return nil
}

// TestRepositoryCloseForwardsToTheBackendOnce pins both shapes of Close: a
// backend that holds no resources reports nil, and a backend implementing
// io.Closer is closed exactly once through the shared store and reports its own
// error — the same error on a second Close, because Store.Close is idempotent.
func TestRepositoryCloseForwardsToTheBackendOnce(t *testing.T) {
	t.Run("backend without a closer", func(t *testing.T) {
		repo := newRepo(t, backmem.New())
		if err := repo.Close(); err != nil {
			t.Fatalf("Close over a backend with no closer = %v, want nil", err)
		}
		if err := repo.Close(); err != nil {
			t.Fatalf("second Close = %v, want nil", err)
		}
	})

	t.Run("backend with a closer", func(t *testing.T) {
		boom := errors.New("backend close exploded")
		backend := &closerBackend{inner: backmem.New(), close: func() error { return boom }}
		repo := newRepo(t, backend)

		if err := repo.Close(); !errors.Is(err, boom) {
			t.Fatalf("Close over a failing closer = %v, want %v", err, boom)
		}
		if err := repo.Close(); !errors.Is(err, boom) {
			t.Fatalf("second Close = %v, want the memoized %v", err, boom)
		}
		if backend.closes != 1 {
			t.Fatalf("backend Close calls = %d, want 1 (Close is idempotent)", backend.closes)
		}
	})
}

// TestCachedRepositoryCloseForwardsToTheSharedBackend pins the cached
// repository's Close: it releases the backend through the wrapped repository, so
// the four per-type caches do not each close the one backend they share.
func TestCachedRepositoryCloseForwardsToTheSharedBackend(t *testing.T) {
	boom := errors.New("backend close exploded")
	backend := &closerBackend{inner: backmem.New(), close: func() error { return boom }}
	repo := newRepo(t, backend)
	cached, err := NewCachedRepository(repo, 8)
	if err != nil {
		t.Fatal(err)
	}

	if err := cached.Close(); !errors.Is(err, boom) {
		t.Fatalf("cached Close = %v, want %v", err, boom)
	}
	if err := cached.Close(); !errors.Is(err, boom) {
		t.Fatalf("second cached Close = %v, want the memoized %v", err, boom)
	}
	if backend.closes != 1 {
		t.Fatalf("backend Close calls = %d, want 1", backend.closes)
	}
}

// TestNewCachedRepositoryRejectsANonPositiveSize pins the constructor's guard:
// a cache with no capacity is a caller error, so no partially built repository
// is returned.
//
// The three later error returns in NewCachedRepository — trees (cached.go:36),
// commits (cached.go:40) and tags (cached.go:44) — are left uncovered on purpose
// (testing-strategy §5): every one of them is the same lru.New failure, and
// lru.New rejects maxSize through cache.ValidateMaxSize before it touches a
// store, so the first (blobs) call always fails first for a size that is invalid
// at all. With a valid size none of the four can fail. Reaching a later arm
// would need an injected failing lru.New, and neither gitlike nor cas/cache/lru
// exposes such a seam.
func TestNewCachedRepositoryRejectsANonPositiveSize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		maxSize int
	}{
		{"zero", 0},
		{"negative", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cached, err := NewCachedRepository(newRepo(t, backmem.New()), tc.maxSize)
			if err == nil {
				t.Fatalf("NewCachedRepository(maxSize %d) = %v, want an error", tc.maxSize, cached)
			}
			if cached != nil {
				t.Fatalf("NewCachedRepository(maxSize %d) returned a cache alongside the error", tc.maxSize)
			}
		})
	}
}

// TestCachedRepositoryGetTagServesFromTheTagCache pins the tag getter: the tag
// is returned with its target, and a second read is served from the cache
// (one miss, then one hit) rather than re-reading the store.
func TestCachedRepositoryGetTagServesFromTheTagCache(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, backmem.New())
	commit, err := repo.Commits.Put(ctx, &Commit{
		Tree:    mustPutTree(t, repo),
		Author:  "Alice",
		Message: "release",
	})
	if err != nil {
		t.Fatal(err)
	}
	tagHash, err := repo.Tags.Put(ctx, &Tag{Name: "v1.0", Target: commit, Tagger: "Bob", Message: "release"})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := NewCachedRepository(repo, 8)
	if err != nil {
		t.Fatal(err)
	}

	got, err := cached.GetTag(ctx, tagHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "v1.0" || !got.Target.Equal(commit) || got.Tagger != "Bob" {
		t.Fatalf("GetTag = %+v, want the stored v1.0 tag targeting %s", got, commit)
	}
	again, err := cached.GetTag(ctx, tagHash)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "v1.0" {
		t.Fatalf("second GetTag = %+v, want the cached tag", again)
	}
	if st := cached.Tags.CacheStats(); st.Misses != 1 || st.Hits != 1 {
		t.Fatalf("tag cache stats = misses %d hits %d, want 1/1", st.Misses, st.Hits)
	}
}

// mustPutTree stores the empty tree and returns its digest, so a commit in
// these tests satisfies its invariant.
func mustPutTree(t *testing.T, repo *Repository) cas.Digest {
	t.Helper()
	d, err := repo.Trees.Put(context.Background(), &Tree{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestResolvedObjectReferencesCoversEveryUnionArm pins the union accessor for
// each populated field: a commit contributes its tree and parent, a tree its
// entry hashes, a tag its target, a blob none (the default arm, since a blob is
// a leaf), and an empty union none.
func TestResolvedObjectReferencesCoversEveryUnionArm(t *testing.T) {
	treeHash := sha256hash.Of([]byte("tree"))
	parentHash := sha256hash.Of([]byte("parent"))
	targetHash := sha256hash.Of([]byte("target"))

	for _, tc := range []struct {
		name string
		ro   *ResolvedObject
		want []cas.Digest
	}{
		{
			name: "commit",
			ro: &ResolvedObject{Type: "commit", Commit: &Commit{
				Tree:   treeHash,
				Parent: parentHash,
			}},
			want: []cas.Digest{treeHash, parentHash},
		},
		{
			name: "tree",
			ro: &ResolvedObject{Type: "tree", Tree: &Tree{Entries: []TreeEntry{
				{Name: "a", Hash: treeHash},
				{Name: "b", Hash: parentHash},
				{Name: "absent"},
			}}},
			want: []cas.Digest{treeHash, parentHash},
		},
		{
			name: "tag",
			ro:   &ResolvedObject{Type: "tag", Tag: &Tag{Name: "v1", Target: targetHash}},
			want: []cas.Digest{targetHash},
		},
		{
			name: "blob has no references",
			ro:   &ResolvedObject{Type: "blob", Blob: &Blob{Data: []byte("leaf")}},
			want: nil,
		},
		{
			name: "empty union has no references",
			ro:   &ResolvedObject{},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.ro.References()
			if len(got) != len(tc.want) {
				t.Fatalf("References() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if !got[i].Equal(tc.want[i]) {
					t.Fatalf("References()[%d] = %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// unknownObject is a casrepo.Object this repository has no union arm for, so
// resolvedObjectOf's default arm and WalkGraph's error propagation can be
// reached directly.
type unknownObject struct{}

func (unknownObject) Type() string             { return "widget@1" }
func (unknownObject) References() []cas.Digest { return nil }

// TestResolvedObjectOfRejectsATypeTheUnionDoesNotModel pins the union mapper's
// default arm: a casrepo.Object outside the four model types is reported as
// cas.ErrUnknownType naming the offending type, never as a half-filled union.
func TestResolvedObjectOfRejectsATypeTheUnionDoesNotModel(t *testing.T) {
	ro, err := resolvedObjectOf(unknownObject{})
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("resolvedObjectOf(foreign object) = (%v, %v), want cas.ErrUnknownType", ro, err)
	}
	if ro != nil {
		t.Fatalf("resolvedObjectOf(foreign object) = %v, want no union", ro)
	}
	if !strings.Contains(err.Error(), "widget@1") {
		t.Fatalf("resolvedObjectOf(foreign object) = %v, want it to name widget@1", err)
	}
}

// fixedResolver returns a caller-supplied object for every digest, so the
// union mapper's default arm can be driven with an object type the repository
// cannot map.
type fixedResolver struct{ obj casrepo.Object }

func (r fixedResolver) Resolve(context.Context, cas.Digest) (casrepo.Object, error) {
	return r.obj, nil
}

// Two further branches are left uncovered on purpose (testing-strategy §5):
//
//   - cached.go:122, Preloader.worker's `if !ok { return }`. The jobs channel is
//     unexported and Stop cancels the context instead of closing it, so a
//     receive from a closed channel cannot happen through this package's API.
//
//   - repo.go:264, WalkGraph's `return err` when the union mapper rejects a
//     resolved node. WalkGraph takes a concrete *Resolver, whose Resolve returns
//     only the four model types (it reports any other stored type as
//     cas.ErrUnknownType before resolvedObjectOf runs), so no digest reaches the
//     mapper with a foreign object. TestWalkGraphReportsATypeTheUnionCannotModel
//     pins the branch's behavior through the same mapper directly.

// TestWalkGraphReportsATypeTheUnionCannotModel pins the mapper the walk applies
// to every node: WalkGraph maps each resolved casrepo.Object onto the union, and
// a node the union does not model aborts the traversal with cas.ErrUnknownType
// instead of visiting a half-built ResolvedObject.
//
// The walk itself is casrepo.Walk, driven here with the same resolver shape
// WalkGraph uses — gitlike's *Resolver can only return the four model types, so
// the foreign type comes from a stand-in resolver.
func TestWalkGraphReportsATypeTheUnionCannotModel(t *testing.T) {
	visited := 0
	err := casrepo.Walk(context.Background(), fixedResolver{obj: unknownObject{}},
		[]cas.Digest{sha256hash.Of([]byte("any"))},
		func(_ cas.Digest, obj casrepo.Object) error {
			ro, err := resolvedObjectOf(obj)
			if err != nil {
				return err
			}
			visited++
			_ = ro
			return nil
		})
	if !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("walk over a foreign object = %v, want cas.ErrUnknownType", err)
	}
	if visited != 0 {
		t.Fatalf("visited = %d, want 0: the union mapper rejected the only node", visited)
	}
}
