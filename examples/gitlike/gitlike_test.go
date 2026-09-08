package gitlike

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/memory"
)

func newRepo(t *testing.T, raw cas.Backend) *Repository {
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
	repo := newRepo(t, backmem.New())

	hb := putBlob(t, repo, "hello")
	blob, err := repo.Blobs.Get(ctx, hb)
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
	tree, err := repo.Trees.Get(ctx, ht)
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
	commit, err := repo.Commits.Get(ctx, hc)
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
	tag, err := repo.Tags.Get(ctx, htag)
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
	repo := newRepo(t, backmem.New())
	h := putBlob(t, repo, "x")
	raw, err := repo.Blobs.GetRaw(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	// TLV envelope: [version][uvarint typeLen][type][payload].
	env, err := cas.EnvelopeFromBytes(raw)
	if err != nil {
		t.Fatalf("EnvelopeFromBytes = %v", err)
	}
	if env.Type != "blob@1" {
		t.Fatalf("stored type = %q, want blob@1", env.Type)
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
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
	res := NewResolver(repo)
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := res.ResolveAny(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveAny(missing) = %v, want ErrNotFound", err)
	}
}

func TestNewRepositoryUnknownAlgorithm(t *testing.T) {
	if _, err := NewRepository(backmem.New(), "nope"); !errors.Is(err, cas.ErrUnknownAlgorithm) {
		t.Fatalf("NewRepository err = %v, want ErrUnknownAlgorithm", err)
	}
}

func TestResolverResolveAny(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
	res := NewResolver(repo)

	// Store a blob object at the byte layer using the legacy unversioned
	// form (type "blob" without @major) as a TLV envelope — parseType reads
	// the base type, and unmarshalEnvelope appends @1 automatically.
	payload := []byte(`{"data":"bGVnYWN5"}`)
	envelopeBytes := marshalEnvelope("blob", payload)
	h, _ := cas.ParseHash("sha256:" + sha256Hex(envelopeBytes))
	if err := repo.raw.Put(ctx, h, bytes.NewReader(envelopeBytes)); err != nil {
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
	repo := newRepo(t, backmem.New())
	res := NewResolver(repo)
	// Store an object with an unknown type name as a TLV envelope.
	envelopeBytes := marshalEnvelope("mystery@9", []byte(`{}`))
	h, _ := cas.ParseHash("sha256:" + sha256Hex(envelopeBytes))
	if err := repo.raw.Put(ctx, h, bytes.NewReader(envelopeBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := res.ResolveAny(ctx, h); !errors.Is(err, cas.ErrUnknownType) {
		t.Fatalf("ResolveAny(unknown) = %v, want ErrUnknownType", err)
	}
}

// --- parseType ---

func TestParseType(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, backmem.New())
	// Stored type+payload bytes directly (no Store.Put) so we can test parseType.
	env := marshalEnvelope("blob@1", []byte{})
	h, _ := cas.ParseHash("sha256:" + sha256Hex(env))
	if err := repo.raw.Put(ctx, h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	raw, err := repo.raw.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(raw)
	raw.Close()
	typ, err := parseType(b)
	if err != nil {
		t.Fatalf("parseType = %q, %v", typ, err)
	}
	if typ != "blob" {
		t.Fatalf("type = %q, want blob", typ)
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
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
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
	repo := newRepo(t, backmem.New())
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

// marshalEnvelope builds a TLV envelope over payload bytes
// (test helper: production serialization is the cas Store codec).
func marshalEnvelope(typeName string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(1) // version
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	buf.Write(payload)
	return buf.Bytes()
}

// --- Codec-authority decode paths (replaces the old object-level
// Deserialize contract tests): invalid payloads are rejected when the typed
// store decodes them, and error paths of the repo/print APIs are pinned. ---

func mustStoreEnv(t *testing.T, repo *Repository, typeName, payloadJSON string) cas.Hash {
	t.Helper()
	env := marshalEnvelope(typeName, []byte(payloadJSON))
	h, err := cas.ParseHash("sha256:" + sha256Hex(env))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.raw.Put(context.Background(), h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestGetRejectsInvalidHashPayloads(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, backmem.New())

	// Tree with an invalid entry hash string.
	h := mustStoreEnv(t, repo, "tree@1", `{"entries":[{"name":"f","hash":"nope:zz","mode":"m"}]}`)
	if _, err := repo.Trees.Get(ctx, h); err == nil {
		t.Fatal("tree with invalid entry hash must fail decode")
	}
	// Commit with an invalid tree hash.
	h = mustStoreEnv(t, repo, "commit@1", `{"tree":"nope:zz","author":"a"}`)
	if _, err := repo.Commits.Get(ctx, h); err == nil {
		t.Fatal("commit with invalid tree hash must fail decode")
	}
	// Commit with an invalid parent hash.
	h = mustStoreEnv(t, repo, "commit@1", `{"tree":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","parent":"nope:zz","author":"a"}`)
	if _, err := repo.Commits.Get(ctx, h); err == nil {
		t.Fatal("commit with invalid parent hash must fail decode")
	}
	// Tag with an invalid target hash.
	h = mustStoreEnv(t, repo, "tag@1", `{"name":"v","target":"nope:zz","tagger":"t"}`)
	if _, err := repo.Tags.Get(ctx, h); err == nil {
		t.Fatal("tag with invalid target hash must fail decode")
	}
}

func TestRepositoryErrorPaths(t *testing.T) {
	raw := backmem.New()
	if _, err := NewRepository(raw, "bogusalgo"); err == nil {
		t.Fatal("NewRepository with unknown algo must error")
	}
	repo, err := NewRepository(raw, "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCachedRepository(repo, 0); err == nil {
		t.Fatal("NewCachedRepository with maxSize 0 must error")
	}
	// ResolveAny of a missing object → ErrNotFound.
	missing, _ := cas.ParseHash("sha256:" + strings.Repeat("00", 32))
	res := NewResolver(repo)
	if _, err := res.ResolveAny(ctxBackground(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveAny(missing) = %v, want ErrNotFound", err)
	}
	// WalkGraph propagates a visitor error.
	root, err := repo.Blobs.Put(ctxBackground(), &Blob{Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	if err := WalkGraph(ctxBackground(), res, root, func(*ResolvedObject) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("WalkGraph visitor error = %v", err)
	}
}

func ctxBackground() context.Context { return context.Background() }

func TestPrintObjectNilHash(t *testing.T) {
	// A nil-hash reference exercises the shortHash nil guard.
	got := PrintObject(&ResolvedObject{Type: "tree", Tree: &Tree{Entries: []TreeEntry{{Name: "f", Mode: "m"}}}})
	if got == "" {
		t.Fatal("PrintObject of tree with nil-hash entry returned empty")
	}
}

// TestNilOptionalFieldRoundTrips covers marshal/unmarshal + References
// branches for objects with nil optional references (no parent, no target,
// nil-hash tree entry).
func TestNilOptionalFieldRoundTrips(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, backmem.New())

	tree := &Tree{Entries: []TreeEntry{{Name: "f", Mode: "m"}}} // nil Hash entry
	th, err := repo.Trees.Put(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}
	back, err := repo.Trees.Get(ctx, th)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 1 || back.Entries[0].Hash != nil {
		t.Fatalf("tree with nil-hash entry round-trip: %+v", back)
	}
	if refs := tree.References(); len(refs) != 0 {
		t.Fatalf("nil-hash entry must not be a reference: %v", refs)
	}

	commit := &Commit{Tree: th, Author: "a", Message: "root"} // nil Parent
	ch, err := repo.Commits.Put(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := repo.Commits.Get(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if cb.Parent != nil || cb.Author != "a" {
		t.Fatalf("commit with nil parent round-trip: %+v", cb)
	}

	tag := &Tag{Name: "v1"} // nil Target
	tgh, err := repo.Tags.Put(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := repo.Tags.Get(ctx, tgh)
	if err != nil {
		t.Fatal(err)
	}
	if tb.Target != nil || tb.Name != "v1" {
		t.Fatalf("tag with nil target round-trip: %+v", tb)
	}
	if refs := tag.References(); len(refs) != 0 {
		t.Fatalf("nil target must not be a reference: %v", refs)
	}
}

// TestPrintObjectShortHash exercises the shortHash non-truncation branch
// (digest hex of 8 chars or fewer) via a tag target.
func TestPrintObjectShortHash(t *testing.T) {
	h, err := cas.NewHash("sha256", []byte{0xde, 0xad, 0xbe, 0xef}) // 8 hex chars
	if err != nil {
		t.Fatal(err)
	}
	got := PrintObject(&ResolvedObject{Type: "tag", Tag: &Tag{Name: "v1", Target: h}})
	if !strings.Contains(got, "deadbeef") {
		t.Fatalf("PrintObject short hash = %q", got)
	}
}
