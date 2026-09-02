package gitlike

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
)

func newRepo(t *testing.T, raw cas.RawStore) *Repository {
	repo, err := NewRepository(raw, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func putBlob(t *testing.T, repo *Repository, data string) cas.Hash {
	h, err := repo.Blobs.Put(context.Background(), &Blob{Data: []byte(data)})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// --- Object round-trips through the stores ---

func TestObjectRoundTrips(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())

	hb := putBlob(t, repo, "hello")
	blob, err := repo.Blobs.GetTyped(ctx, hb)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob.Data) != "hello" {
		t.Fatalf("blob round-trip: %q", blob.Data)
	}

	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{
		{Name: "a.txt", Hash: hb, Mode: "100644"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.Trees.GetTyped(ctx, ht)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 1 || tree.Entries[0].Name != "a.txt" || !tree.Entries[0].Hash.Equal(hb) {
		t.Fatalf("tree round-trip: %+v", tree)
	}

	hc, err := repo.Commits.Put(ctx, &Commit{
		Tree: ht, Author: "a", Message: "m", Time: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.Commits.GetTyped(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}
	if commit.Message != "m" || !commit.Tree.Equal(ht) || commit.Parent != nil {
		t.Fatalf("commit round-trip: %+v", commit)
	}
	if commit.Time.Unix() != 1 {
		t.Fatalf("commit time round-trip: %v", commit.Time)
	}

	htag, err := repo.Tags.Put(ctx, &Tag{Name: "v1", Target: hc, Tagger: "t", Message: "tag"})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := repo.Tags.GetTyped(ctx, htag)
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "v1" || !tag.Target.Equal(hc) {
		t.Fatalf("tag round-trip: %+v", tag)
	}
}

// --- Versioned type names (object-versioning §6) ---

func TestVersionedTypeNames(t *testing.T) {
	if (&Blob{}).Type() != "blob@1" {
		t.Error("blob type name")
	}
	if (&Tree{}).Type() != "tree@1" {
		t.Error("tree type name")
	}
	if (&Commit{}).Type() != "commit@1" {
		t.Error("commit type name")
	}
	if (&Tag{}).Type() != "tag@1" {
		t.Error("tag type name")
	}
}

func TestStoredEnvelopeCarriesVersion(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	h := putBlob(t, repo, "x")
	raw, err := repo.Blobs.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "blob@1" {
		t.Fatalf("stored envelope type = %q, want blob@1", env.Type)
	}
}

// --- References ---

func TestReferences(t *testing.T) {
	hb := mustHash(t, "sha256:"+strings.Repeat("ab", 32))
	hc := mustHash(t, "sha256:"+strings.Repeat("cd", 32))

	if got := (&Blob{}).References(); got != nil {
		t.Errorf("blob refs = %v", got)
	}
	tree := &Tree{Entries: []TreeEntry{{Name: "a", Hash: hb, Mode: "m"}, {Name: "b", Hash: hc, Mode: "m"}}}
	if got := tree.References(); len(got) != 2 || !got[0].Equal(hb) || !got[1].Equal(hc) {
		t.Errorf("tree refs = %v", got)
	}
	commit := &Commit{Tree: hb, Parent: hc}
	if got := commit.References(); len(got) != 2 {
		t.Errorf("commit refs = %v", got)
	}
	root := &Commit{Tree: hb}
	if got := root.References(); len(got) != 1 {
		t.Errorf("root commit refs = %v", got)
	}
	tag := &Tag{Target: hb}
	if got := tag.References(); len(got) != 1 || !got[0].Equal(hb) {
		t.Errorf("tag refs = %v", got)
	}
}

// --- Resolver ---

func TestResolverTyped(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)

	hb := putBlob(t, repo, "data")
	blob, err := res.ResolveBlob(ctx, hb)
	if err != nil || string(blob.Data) != "data" {
		t.Fatalf("ResolveBlob = %v, %v", blob, err)
	}
	// Wrong resolver for a hash → decode/type error, not silent garbage.
	if _, err := res.ResolveCommit(ctx, hb); err == nil {
		t.Fatal("ResolveCommit on a blob must fail")
	}
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := res.ResolveBlob(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveBlob(missing) = %v, want ErrNotFound", err)
	}
}

func TestResolverResolveAnyMissing(t *testing.T) {
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := res.ResolveAny(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveAny(missing) = %v, want ErrNotFound", err)
	}
}

func TestNewRepositoryUnknownAlgorithm(t *testing.T) {
	if _, err := NewRepository(cas.NewMemoryRawStore(), "nope"); !errors.Is(err, cas.ErrUnknownAlgorithm) {
		t.Fatalf("NewRepository err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestResolverResolveAny(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)

	hb := putBlob(t, repo, "data")
	ro, err := res.ResolveAny(ctx, hb)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Type != "blob" || ro.Blob == nil || string(ro.Blob.Data) != "data" {
		t.Fatalf("ResolveAny(blob) = %+v", ro)
	}
	if ro.Commit != nil || ro.Tree != nil || ro.Tag != nil {
		t.Fatal("ResolveAny must fill exactly one field")
	}

	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: hb, Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	ro, err = res.ResolveAny(ctx, ht)
	if err != nil || ro.Type != "tree" || ro.Tree == nil || len(ro.Tree.Entries) != 1 {
		t.Fatalf("ResolveAny(tree) = %+v, %v", ro, err)
	}

	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ht, Author: "a", Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ro, err = res.ResolveAny(ctx, hc)
	if err != nil || ro.Type != "commit" || ro.Commit == nil || ro.Commit.Message != "m" {
		t.Fatalf("ResolveAny(commit) = %+v, %v", ro, err)
	}

	htag, err := repo.Tags.Put(ctx, &Tag{Name: "v", Target: hc})
	if err != nil {
		t.Fatal(err)
	}
	ro, err = res.ResolveAny(ctx, htag)
	if err != nil || ro.Type != "tag" || ro.Tag == nil || ro.Tag.Name != "v" {
		t.Fatalf("ResolveAny(tag) = %+v, %v", ro, err)
	}
}

// A legacy unversioned envelope ("blob" without "@major") must resolve as
// the @1 default (object-versioning §2).
func TestResolveAnyLegacyUnversioned(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)

	payload := base64.StdEncoding.EncodeToString([]byte(`{"data":"bGVnYWN5"}`))
	envelopeBytes, err := json.Marshal(map[string]string{"type": "blob", "data": payload})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := cas.ParseHash("sha256:" + sha256Hex(envelopeBytes))
	if err := repo.raw.Put(ctx, h, strings.NewReader(string(envelopeBytes))); err != nil {
		t.Fatal(err)
	}
	ro, err := res.ResolveAny(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Type != "blob" || string(ro.Blob.Data) != "legacy" {
		t.Fatalf("legacy resolve = %+v", ro)
	}
}

func TestResolveAnyUnknownType(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)
	// Store an envelope with an unknown type name.
	payload := base64.StdEncoding.EncodeToString([]byte(`{}`))
	envelopeBytes, err := json.Marshal(map[string]string{"type": "mystery@9", "data": payload})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := cas.ParseHash("sha256:" + sha256Hex(envelopeBytes))
	if err := repo.raw.Put(ctx, h, strings.NewReader(string(envelopeBytes))); err != nil {
		t.Fatal(err)
	}
	if _, err := res.ResolveAny(ctx, h); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("ResolveAny(unknown) = %v, want ErrUnknownType", err)
	}
}

// --- parseType ---

func TestParseType(t *testing.T) {
	ser, err := marshalEnvelope("blob@1", []byte(`{"data":"eA=="}`))
	if err != nil {
		t.Fatal(err)
	}
	typ, err := parseType(ser)
	if err != nil || typ != "blob" {
		t.Fatalf("parseType = %q, %v", typ, err)
	}
	// Unversioned legacy name.
	legacy, _ := json.Marshal(map[string]string{"type": "commit"})
	typ, err = parseType(legacy)
	if err != nil || typ != "commit" {
		t.Fatalf("parseType(legacy) = %q, %v", typ, err)
	}
	if _, err := parseType([]byte("garbage")); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("parseType(garbage) = %v, want ErrUnknownType", err)
	}
	if _, err := parseType([]byte(`{"type":""}`)); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("parseType(empty type) = %v, want ErrUnknownType", err)
	}
}

// --- PrintObject & WalkGraph ---

func TestPrintObject(t *testing.T) {
	cases := []struct {
		ro   *ResolvedObject
		want string
	}{
		{&ResolvedObject{Type: "blob", Blob: &Blob{Data: make([]byte, 5)}}, "blob (5 bytes)"},
		{&ResolvedObject{Type: "tree", Tree: &Tree{}}, "tree (0 entries)"},
		{&ResolvedObject{Type: "commit", Commit: &Commit{Author: "alice", Message: "hi"}}, "commit by alice: hi"},
		{&ResolvedObject{Type: "tag", Tag: &Tag{Name: "v1", Target: mustHash(t, "sha256:"+strings.Repeat("ab", 32))}}, "tag \"v1\" -> " + strings.Repeat("ab", 4)},
		{&ResolvedObject{Type: "other"}, "unknown type \"other\""},
	}
	for _, tc := range cases {
		if got := PrintObject(tc.ro); got != tc.want {
			t.Errorf("PrintObject(%+v) = %q, want %q", tc.ro, got, tc.want)
		}
	}
}

func TestWalkGraph(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)

	hb := putBlob(t, repo, "content")
	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: hb, Mode: "100644"}}})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ht, Author: "a", Message: "first"})
	if err != nil {
		t.Fatal(err)
	}

	var seen []string
	err = WalkGraph(ctx, res, hc, func(ro *ResolvedObject) error {
		seen = append(seen, ro.Type)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("walked %d nodes, want 3 (commit, tree, blob): %v", len(seen), seen)
	}
	types := map[string]bool{}
	for _, s := range seen {
		types[s] = true
	}
	for _, want := range []string{"commit", "tree", "blob"} {
		if !types[want] {
			t.Errorf("type %q not visited", want)
		}
	}

	// Tag → commit → tree → blob.
	htag, err := repo.Tags.Put(ctx, &Tag{Name: "v1", Target: hc})
	if err != nil {
		t.Fatal(err)
	}
	seen = nil
	if err := WalkGraph(ctx, res, htag, func(ro *ResolvedObject) error {
		seen = append(seen, ro.Type)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf("tag walk visited %d nodes, want 4: %v", len(seen), seen)
	}

	// Missing root → ErrNotFound.
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err := WalkGraph(ctx, res, missing, func(*ResolvedObject) error { return nil }); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("WalkGraph(missing) = %v", err)
	}
}

// A visit error must stop the walk and propagate.
func TestWalkGraphVisitError(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	res := NewResolver(repo)
	hb := putBlob(t, repo, "x")
	sentinel := errors.New("stop walking")
	err := WalkGraph(ctx, res, hb, func(*ResolvedObject) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("WalkGraph = %v, want sentinel", err)
	}
}

// --- CachedRepository & Preloader ---

func TestCachedRepository(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	cached, err := NewCachedRepository(repo, 10)
	if err != nil {
		t.Fatal(err)
	}

	hb := putBlob(t, repo, "cached data")
	blob, err := cached.GetBlob(ctx, hb)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob.Data) != "cached data" {
		t.Fatalf("GetBlob = %q", blob.Data)
	}
	// Second read hits the cache.
	if _, err := cached.GetBlob(ctx, hb); err != nil {
		t.Fatal(err)
	}
	if st := cached.Blobs.CacheStats(); st.Hits < 1 {
		t.Fatalf("cache hits = %d, want >= 1", st.Hits)
	}

	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: hb, Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetTree(ctx, ht); err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ht, Author: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetCommit(ctx, hc); err != nil {
		t.Fatal(err)
	}
	ro, err := cached.ResolveAny(ctx, hb)
	if err != nil || ro.Type != "blob" {
		t.Fatalf("cached ResolveAny = %+v, %v", ro, err)
	}
	if _, err := NewCachedRepository(repo, 0); err == nil {
		t.Fatal("maxSize 0 must be rejected")
	}
}

func TestCachedRepositoryMissing(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	cached, err := NewCachedRepository(repo, 10)
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := cached.GetCommit(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetCommit(missing) = %v", err)
	}
	if _, err := cached.GetTree(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetTree(missing) = %v", err)
	}
	if _, err := cached.GetBlob(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("GetBlob(missing) = %v", err)
	}
}

func TestPreloaderDefaultWorkers(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	cached, err := NewCachedRepository(repo, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPreloader(cached, 0) // workers <= 0 → default
	defer p.Stop()
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: mustHash(t, "sha256:"+strings.Repeat("ab", 32)), Author: "a"})
	if err != nil {
		t.Fatal(err)
	}
	p.Preload(hc)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := cached.Commits.CacheStats(); st.Size >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := cached.Commits.CacheStats(); st.Size < 1 {
		t.Fatalf("preloader (default workers) did not load: size = %d", st.Size)
	}
}

func TestPreloader(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, cas.NewMemoryRawStore())
	cached, err := NewCachedRepository(repo, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPreloader(cached, 2)
	defer p.Stop()

	hb := putBlob(t, repo, "data")
	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: hb, Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ht, Author: "a", Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	p.Preload(hc)

	// The preloader must load the commit into the commit cache.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := cached.Commits.CacheStats(); st.Size >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := cached.Commits.CacheStats(); st.Size < 1 {
		t.Fatalf("preloader did not load the commit: size = %d", st.Size)
	}
}

// --- Helpers ---

func mustHash(t *testing.T, s string) cas.Hash {
	h, err := cas.ParseHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// marshalEnvelope builds a self-describing envelope over payload bytes
// (test helper: production serialization is the cas Store codec).
func marshalEnvelope(typeName string, payload []byte) ([]byte, error) {
	env := map[string]string{
		"type": typeName,
		"data": base64.StdEncoding.EncodeToString(payload),
	}
	return json.Marshal(env)
}
