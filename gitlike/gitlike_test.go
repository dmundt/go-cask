package gitlike

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	gobcodec "github.com/dmundt/go-cask/cas/codec/gob"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256hash "github.com/dmundt/go-cask/cas/hash/sha256"
)

// ref keeps object literals readable: a reference field is a plain cas.Digest
// now, so this is the identity function (it also documents where a reference
// goes in a literal).
func ref(d cas.Digest) cas.Digest { return d }

// jsonCodecs is the Codecs set the tests build repositories with: gitlike names
// no codec, so every construction passes one explicitly.
func jsonCodecs() Codecs {
	return Codecs{
		Blob:   jsoncodec.New[*Blob](),
		Tree:   jsoncodec.New[*Tree](),
		Commit: jsoncodec.New[*Commit](),
		Tag:    jsoncodec.New[*Tag](),
	}
}

func newRepo(t *testing.T, raw cas.Backend) *Repository {
	t.Helper()
	return NewRepository(raw, sha256hash.New(), jsonCodecs())
}

func putBlob(t *testing.T, repo *Repository, data string) cas.Digest {
	h, err := repo.Blobs.Put(context.Background(), &Blob{Data: []byte(data)})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// --- Object round-trips through the stores ---

func TestObjectRoundTrips(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())

	hb := putBlob(t, repo, "hello")
	blob, err := repo.Blobs.Get(ctx, hb)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob.Data) != "hello" {
		t.Fatalf("blob round-trip: %q", blob.Data)
	}

	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{
		{Name: "a.txt", Hash: ref(hb), Mode: "100644"},
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
		Tree: ref(ht), Author: "a", Message: "m", Time: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.Commits.Get(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}
	if commit.Message != "m" || !commit.Tree.Equal(ht) || !commit.Parent.IsZero() {
		t.Fatalf("commit round-trip: %+v", commit)
	}
	if commit.Time.Unix() != 1 {
		t.Fatalf("commit time round-trip: %v", commit.Time)
	}

	htag, err := repo.Tags.Put(ctx, &Tag{Name: "v1", Target: ref(hc), Tagger: "t", Message: "tag"})
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
	repo := newRepo(t, mem.New())
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
	hb := mustDigest(t, strings.Repeat("ab", 32))
	hc := mustDigest(t, strings.Repeat("cd", 32))

	if got := (&Blob{}).References(); got != nil {
		t.Errorf("blob refs = %v", got)
	}
	tree := &Tree{Entries: []TreeEntry{{Name: "a", Hash: ref(hb), Mode: "m"}, {Name: "b", Hash: ref(hc), Mode: "m"}}}
	if got := tree.References(); len(got) != 2 || !got[0].Equal(hb) || !got[1].Equal(hc) {
		t.Errorf("tree refs = %v", got)
	}
	commit := &Commit{Tree: ref(hb), Parent: ref(hc)}
	if got := commit.References(); len(got) != 2 {
		t.Errorf("commit refs = %v", got)
	}
	root := &Commit{Tree: ref(hb)}
	if got := root.References(); len(got) != 1 {
		t.Errorf("root commit refs = %v", got)
	}
	tag := &Tag{Target: ref(hb)}
	if got := tag.References(); len(got) != 1 || !got[0].Equal(hb) {
		t.Errorf("tag refs = %v", got)
	}
}

// --- Resolver ---

func TestResolverTyped(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)

	hb := putBlob(t, repo, "data")
	blob, err := res.ResolveBlob(ctx, hb)
	if err != nil || string(blob.Data) != "data" {
		t.Fatalf("ResolveBlob = %v, %v", blob, err)
	}
	// Wrong resolver for a digest → decode/type error, not silent garbage.
	if _, err := res.ResolveCommit(ctx, hb); err == nil {
		t.Fatal("ResolveCommit on a blob must fail")
	}
	missing := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := res.ResolveBlob(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveBlob(missing) = %v, want ErrNotFound", err)
	}
}

func TestResolverResolveAnyMissing(t *testing.T) {
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)
	missing := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := res.ResolveAny(context.Background(), missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveAny(missing) = %v, want ErrNotFound", err)
	}
}

func TestResolverResolveAny(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())
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

	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: ref(hb), Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	ro, err = res.ResolveAny(ctx, ht)
	if err != nil || ro.Type != "tree" || ro.Tree == nil || len(ro.Tree.Entries) != 1 {
		t.Fatalf("ResolveAny(tree) = %+v, %v", ro, err)
	}

	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ref(ht), Author: "a", Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ro, err = res.ResolveAny(ctx, hc)
	if err != nil || ro.Type != "commit" || ro.Commit == nil || ro.Commit.Message != "m" {
		t.Fatalf("ResolveAny(commit) = %+v, %v", ro, err)
	}

	htag, err := repo.Tags.Put(ctx, &Tag{Name: "v", Target: ref(hc)})
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
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)

	// Store a blob object at the byte layer using the legacy unversioned
	// form (type "blob" without @major) as a TLV envelope — parseType reads
	// the base type, and unmarshalEnvelope appends @1 automatically.
	payload := []byte(`{"data":"bGVnYWN5"}`)
	envelopeBytes := marshalEnvelope("blob", payload)
	h := sha256hash.Of(envelopeBytes)
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
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)
	// Store an object with an unknown type name as a TLV envelope.
	envelopeBytes := marshalEnvelope("mystery@9", []byte(`{}`))
	h := sha256hash.Of(envelopeBytes)
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
	repo := newRepo(t, mem.New())
	// Stored type+payload bytes directly (no Store.Put) so we can test parseType.
	env := marshalEnvelope("blob@1", []byte{})
	h := sha256hash.Of(env)
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
		{&ResolvedObject{Type: "tag", Tag: &Tag{Name: "v1", Target: ref(mustDigest(t, strings.Repeat("ab", 32)))}}, "tag \"v1\" -> " + strings.Repeat("ab", 4)},
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
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)

	hb := putBlob(t, repo, "content")
	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: ref(hb), Mode: "100644"}}})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ref(ht), Author: "a", Message: "first"})
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
	htag, err := repo.Tags.Put(ctx, &Tag{Name: "v1", Target: ref(hc)})
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
	missing := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000000")
	if err := WalkGraph(ctx, res, missing, func(*ResolvedObject) error { return nil }); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("WalkGraph(missing) = %v", err)
	}
}

// A visit error must stop the walk and propagate.
func TestWalkGraphVisitError(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())
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
	repo := newRepo(t, mem.New())
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

	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: ref(hb), Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.GetTree(ctx, ht); err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ref(ht), Author: "a"})
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
	repo := newRepo(t, mem.New())
	cached, err := NewCachedRepository(repo, 10)
	if err != nil {
		t.Fatal(err)
	}
	missing := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000000")
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
	repo := newRepo(t, mem.New())
	cached, err := NewCachedRepository(repo, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPreloader(cached, 0) // workers <= 0 → default
	defer p.Stop()
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ref(mustDigest(t, strings.Repeat("ab", 32))), Author: "a"})
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
	repo := newRepo(t, mem.New())
	cached, err := NewCachedRepository(repo, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPreloader(cached, 2)
	defer p.Stop()

	hb := putBlob(t, repo, "data")
	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: ref(hb), Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ref(ht), Author: "a", Message: "m"})
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

func mustDigest(t *testing.T, hexDigest string) cas.Digest {
	t.Helper()
	d, err := cas.ParseDigest(hexDigest)
	if err != nil {
		t.Fatal(err)
	}
	return d
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
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	return buf.Bytes()
}

// --- Codec-authority decode paths (replaces the old object-level
// Deserialize contract tests): invalid payloads are rejected when the typed
// store decodes them, and error paths of the repo/print APIs are pinned. ---

func mustStoreEnv(t *testing.T, repo *Repository, typeName, payloadJSON string) cas.Digest {
	t.Helper()
	env := marshalEnvelope(typeName, []byte(payloadJSON))
	h := sha256hash.Of(env)
	if err := repo.raw.Put(context.Background(), h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestGetRejectsInvalidDigestPayloads(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())

	// Tree with an invalid reference string.
	h := mustStoreEnv(t, repo, "tree@1", `{"entries":[{"name":"f","hash":"nope:zz","mode":"m"}]}`)
	if _, err := repo.Trees.Get(ctx, h); err == nil {
		t.Fatal("tree with an invalid entry digest must fail decode")
	}
	// Commit with an invalid tree reference.
	h = mustStoreEnv(t, repo, "commit@1", `{"tree":"nope:zz","author":"a"}`)
	if _, err := repo.Commits.Get(ctx, h); err == nil {
		t.Fatal("commit with an invalid tree digest must fail decode")
	}
	// Commit with an invalid parent reference.
	h = mustStoreEnv(t, repo, "commit@1", `{"tree":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","parent":"nope:zz","author":"a"}`)
	if _, err := repo.Commits.Get(ctx, h); err == nil {
		t.Fatal("commit with an invalid parent digest must fail decode")
	}
	// Tag with an invalid target reference.
	h = mustStoreEnv(t, repo, "tag@1", `{"name":"v","target":"nope:zz","tagger":"t"}`)
	if _, err := repo.Tags.Get(ctx, h); err == nil {
		t.Fatal("tag with an invalid target digest must fail decode")
	}
}

// TestLegacyAlgoPrefixedReferenceFailsLoudly pins the deliberate break that
// comes with keeping the `@1` type names: an object written before the address
// became a bare digest stores its references as "sha256:<hex>", and the strict
// hex parser rejects that prefix — so the object fails to decode as ErrCorrupt
// instead of being silently misread as a different address.
func TestLegacyAlgoPrefixedReferenceFailsLoudly(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())
	legacy := "sha256:" + strings.Repeat("ab", 32)

	h := mustStoreEnv(t, repo, TypeTree, `{"entries":[{"name":"f","hash":"`+legacy+`","mode":"m"}]}`)
	if _, err := repo.Trees.Get(ctx, h); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("legacy tree decode = %v, want ErrCorrupt", err)
	}
	h = mustStoreEnv(t, repo, TypeCommit, `{"tree":"`+legacy+`","author":"a","message":"m","time":"2026-09-09T12:00:00Z"}`)
	if _, err := repo.Commits.Get(ctx, h); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("legacy commit decode = %v, want ErrCorrupt", err)
	}
	h = mustStoreEnv(t, repo, TypeTag, `{"name":"v","target":"`+legacy+`","tagger":"t"}`)
	if _, err := repo.Tags.Get(ctx, h); !errors.Is(err, cas.ErrCorrupt) {
		t.Fatalf("legacy tag decode = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryErrorPaths(t *testing.T) {
	raw := mem.New()
	repo := NewRepository(raw, sha256hash.New(), jsonCodecs())
	if _, err := NewCachedRepository(repo, 0); err == nil {
		t.Fatal("NewCachedRepository with maxSize 0 must error")
	}
	// ResolveAny of a missing object → ErrNotFound.
	missing := mustDigest(t, strings.Repeat("00", 32))
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

func TestPrintObjectNilDigest(t *testing.T) {
	// An absent-digest reference exercises the shortDigest nil guard.
	got := PrintObject(&ResolvedObject{Type: "tree", Tree: &Tree{Entries: []TreeEntry{{Name: "f", Mode: "m"}}}})
	if got == "" {
		t.Fatal("PrintObject of tree with an absent-digest entry returned empty")
	}
}

// TestNilOptionalFieldRoundTrips covers marshal/unmarshal + References
// branches for objects with nil optional references (no parent, no target,
// an absent-digest tree entry).
func TestNilOptionalFieldRoundTrips(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, mem.New())

	tree := &Tree{Entries: []TreeEntry{{Name: "f", Mode: "m"}}} // absent Hash reference
	th, err := repo.Trees.Put(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}
	back, err := repo.Trees.Get(ctx, th)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 1 || !back.Entries[0].Hash.IsZero() {
		t.Fatalf("tree with an absent-digest entry round-trip: %+v", back)
	}
	if refs := tree.References(); len(refs) != 0 {
		t.Fatalf("an absent-digest entry must not be a reference: %v", refs)
	}

	commit := &Commit{Tree: ref(th), Author: "a", Message: "root"} // absent Parent
	ch, err := repo.Commits.Put(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := repo.Commits.Get(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if !cb.Parent.IsZero() || cb.Author != "a" {
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
	if !tb.Target.IsZero() || tb.Name != "v1" {
		t.Fatalf("tag with nil target round-trip: %+v", tb)
	}
	if refs := tag.References(); len(refs) != 0 {
		t.Fatalf("nil target must not be a reference: %v", refs)
	}
}

// --- decode error branches ---

// TestUnmarshalInvalidJSON pins that malformed JSON fails for every object
// type. TreeEntry and Tag have no JSON code of their own any more, so this
// goes through json.Unmarshal (the path a store read takes); Commit's required
// tree and cas.Digest's reference validation are covered separately.
func TestUnmarshalInvalidJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		into any
	}{
		{"TreeEntry", &TreeEntry{}},
		{"Commit", &Commit{}},
		{"Tag", &Tag{}},
		{"Tree", &Tree{}},
		{"Blob", &Blob{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte("{not-json"), tc.into); err == nil {
				t.Fatalf("%s on malformed JSON must error", tc.name)
			}
		})
	}
}

// TestCommitRequiredTreeDecode pins the required-tree rule on the read path: a
// stored commit whose payload has a missing, empty, or null tree decodes into an
// object that violates its own invariant, so the store reports ErrCorrupt (the
// rule is Commit.Validate, enforced by the store, so it holds under any codec).
// A malformed reference still fails earlier, inside the codec.
func TestCommitRequiredTreeDecode(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, mem.New())
	h := mustDigest(t, strings.Repeat("ab", 32))

	// Malformed references fail while decoding (cas.Digest.UnmarshalText).
	for _, payload := range []string{
		`{"tree":"nope:zz","author":"a"}`,        // unknown algorithm
		`{"tree":"sha256:not-hex","author":"a"}`, // malformed digest
		`{"tree":42,"author":"a"}`,               // wrong type
	} {
		d := mustStoreEnv(t, repo, TypeCommit, payload)
		if _, err := repo.Commits.Get(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
			t.Fatalf("commit from %s = %v, want ErrCorrupt", payload, err)
		}
	}

	// A missing, empty or null tree decodes, but the object violates its own
	// invariant: the store rejects it rather than returning a rootless commit.
	for _, payload := range []string{
		`{"author":"a"}`,           // missing
		`{"tree":"","author":"a"}`, // empty string
		`{"tree":null,"author":"a"}`,
	} {
		d := mustStoreEnv(t, repo, TypeCommit, payload)
		if _, err := repo.Commits.Get(ctx, d); !errors.Is(err, cas.ErrCorrupt) {
			t.Fatalf("commit from %s = %v, want ErrCorrupt", payload, err)
		}
	}

	// A valid tree decodes; an absent parent stays absent.
	d := mustStoreEnv(t, repo, TypeCommit, `{"tree":"`+h.String()+`","parent":null,"author":"a"}`)
	c, err := repo.Commits.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tree.IsZero() || !c.Tree.Equal(h) {
		t.Fatalf("tree = %v", c.Tree)
	}
	if !c.Parent.IsZero() {
		t.Fatalf("null parent = %v, want absent", c.Parent)
	}
}

// TestCommitWithParentRoundTrip exercises the optional-parent path through the
// store: the parent is serialized by the codec and re-parsed back to a digest.
func TestCommitWithParentRoundTrip(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, mem.New())

	hb := putBlob(t, repo, "a")
	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "a.txt", Hash: ref(hb), Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := repo.Commits.Put(ctx, &Commit{Tree: ref(ht), Author: "a", Message: "root"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.Commits.Put(ctx, &Commit{Tree: ref(ht), Parent: ref(root), Author: "a", Message: "child"})
	if err != nil {
		t.Fatal(err)
	}
	back, err := repo.Commits.Get(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	if back.Message != "child" || back.Parent.IsZero() || !back.Parent.Equal(root) {
		t.Fatalf("commit with parent round-trip: %+v", back)
	}
}

// --- Digest field marshalling and validation ---

// TestDigestFieldsMarshalWithoutCustomCode pins the delegation that let
// TreeEntry drop its hand-written marshaller: the cas.Digest field renders
// itself as bare lowercase hex, and an absent reference is omitted.
func TestDigestFieldsMarshalWithoutCustomCode(t *testing.T) {
	h := mustDigest(t, strings.Repeat("ab", 32))

	raw, err := json.Marshal(TreeEntry{Name: "f", Hash: ref(h), Mode: "100644"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"f","hash":"` + h.String() + `","mode":"100644"}`
	if string(raw) != want {
		t.Fatalf("TreeEntry JSON = %s, want %s", raw, want)
	}

	raw, err = json.Marshal(TreeEntry{Name: "g", Mode: "100644"}) // absent reference
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"name":"g","mode":"100644"}` {
		t.Fatalf("absent-reference TreeEntry JSON = %s", raw)
	}
}

// TestStoredAddressesPinned is the compatibility guard for the marshalling
// change: every expectation below is the historical payload literal, and the
// address of a stored object is the hash of exactly those bytes. If any of
// these fail, existing stores no longer resolve.
func TestStoredAddressesPinned(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, mem.New())
	hb := putBlob(t, repo, "hello")
	treeHash := mustDigest(t, strings.Repeat("ab", 32))
	parentHash := mustDigest(t, strings.Repeat("cd", 32))
	ts := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		typeName string
		payload  string
		put      func() (cas.Digest, error)
	}{
		{
			"tree with a hash entry and an absent-hash entry",
			TypeTree,
			`{"entries":[{"name":"f","hash":"` + treeHash.String() + `","mode":"100644"},{"name":"g","mode":"100644"}]}`,
			func() (cas.Digest, error) {
				return repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{
					{Name: "f", Hash: ref(treeHash), Mode: "100644"},
					{Name: "g", Mode: "100644"},
				}})
			},
		},
		{
			"root commit (absent parent omitted)",
			TypeCommit,
			`{"tree":"` + treeHash.String() + `","author":"a","message":"m","time":"2026-09-09T12:00:00Z"}`,
			func() (cas.Digest, error) {
				return repo.Commits.Put(ctx, &Commit{Tree: ref(treeHash), Author: "a", Message: "m", Time: ts})
			},
		},
		{
			"commit with parent",
			TypeCommit,
			`{"tree":"` + treeHash.String() + `","parent":"` + parentHash.String() + `","author":"a","message":"m","time":"2026-09-09T12:00:00Z"}`,
			func() (cas.Digest, error) {
				return repo.Commits.Put(ctx, &Commit{Tree: ref(treeHash), Parent: ref(parentHash), Author: "a", Message: "m", Time: ts})
			},
		},
		{
			"tag with target",
			TypeTag,
			`{"name":"v1","target":"` + hb.String() + `","tagger":"t","message":"rel"}`,
			func() (cas.Digest, error) {
				return repo.Tags.Put(ctx, &Tag{Name: "v1", Target: ref(hb), Tagger: "t", Message: "rel"})
			},
		},
		{
			"tag without target keeps the historical empty string",
			TypeTag,
			`{"name":"v2","target":"","tagger":"","message":""}`,
			func() (cas.Digest, error) {
				return repo.Tags.Put(ctx, &Tag{Name: "v2"})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := sha256hash.Of(marshalEnvelope(tc.typeName, []byte(tc.payload)))
			got, err := tc.put()
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) {
				t.Fatalf("stored address changed:\n got %s\nwant %s\npayload was %s", got, want, tc.payload)
			}
		})
	}
}

// TestValidate pins the advisory validation contract: naming rules for entries
// and tags, the mandatory commit tree, and absent optional references.
func TestValidate(t *testing.T) {
	h := mustDigest(t, strings.Repeat("ab", 32))

	if err := (&Commit{Tree: ref(h)}).Validate(); err != nil {
		t.Errorf("commit with tree: %v", err)
	}
	if err := (&Commit{}).Validate(); err == nil {
		t.Error("commit without a tree must not validate")
	}
	if err := (&Tag{Name: "v1"}).Validate(); err != nil {
		t.Errorf("named tag (absent target): %v", err)
	}
	if err := (&Tag{}).Validate(); err == nil {
		t.Error("nameless tag must not validate")
	}
	if err := (&Tree{Entries: []TreeEntry{{Name: "f", Hash: ref(h)}, {Name: "g"}}}).Validate(); err != nil {
		t.Errorf("named entries: %v", err)
	}
	if err := (&Tree{Entries: []TreeEntry{{Hash: ref(h)}}}).Validate(); err == nil {
		t.Error("tree with a nameless entry must not validate")
	}
	if err := (TreeEntry{Name: "f"}).Validate(); err != nil {
		t.Errorf("absent-hash entry: %v", err)
	}
	if err := (TreeEntry{}).Validate(); err == nil {
		t.Error("nameless entry must not validate")
	}
}

// TestRepositoryWithAnotherCodec pins that gitlike names no codec: the same
// object model runs over gob, cross-type resolution and WalkGraph still work
// (the envelope is codec-agnostic), and the mandatory-tree rule still holds —
// because it is Commit.Validate enforced by the store, not a codec-specific
// method.
func TestRepositoryWithAnotherCodec(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(mem.New(), sha256hash.New(), Codecs{
		Blob:   gobcodec.New[*Blob](),
		Tree:   gobcodec.New[*Tree](),
		Commit: gobcodec.New[*Commit](),
		Tag:    gobcodec.New[*Tag](),
	})

	// The invariant is codec-independent: a gob-backed repository refuses a
	// tree-less commit just like the JSON one.
	if _, err := repo.Commits.Put(ctx, &Commit{Author: "a"}); err == nil {
		t.Fatal("a gob-backed repository must still refuse a tree-less commit")
	}

	hb := putBlob(t, repo, "gob blob")
	ht, err := repo.Trees.Put(ctx, &Tree{Entries: []TreeEntry{{Name: "f", Hash: hb, Mode: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ht, Author: "a", Message: "m", Time: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	hTag, err := repo.Tags.Put(ctx, &Tag{Name: "v1", Target: hc})
	if err != nil {
		t.Fatal(err)
	}

	// Typed reads and cross-type resolution work over the non-JSON codec.
	res := NewResolver(repo)
	commit, err := res.ResolveCommit(ctx, hc)
	if err != nil || !commit.Tree.Equal(ht) || commit.Message != "m" {
		t.Fatalf("ResolveCommit over gob = %+v, %v", commit, err)
	}
	ro, err := res.ResolveAny(ctx, hTag)
	if err != nil || ro.Type != "tag" || ro.Tag == nil || !ro.Tag.Target.Equal(hc) {
		t.Fatalf("ResolveAny(tag) over gob = %+v, %v", ro, err)
	}
	visited := 0
	if err := WalkGraph(ctx, res, hTag, func(*ResolvedObject) error { visited++; return nil }); err != nil {
		t.Fatal(err)
	}
	if visited != 4 {
		t.Fatalf("WalkGraph over gob visited %d objects, want 4", visited)
	}
}

// TestPutRejectsCommitWithoutTree keeps the write-side guarantee: the store
// calls Commit.Validate before encoding, so a tree-less commit is never written
// (this used to live in a hand-written Commit.MarshalJSON).
func TestPutRejectsCommitWithoutTree(t *testing.T) {
	repo := newRepo(t, mem.New())
	if _, err := repo.Commits.Put(context.Background(), &Commit{Author: "a"}); err == nil {
		t.Fatal("Put of a commit without a tree must fail")
	}
}

// --- parseType / ResolveAny error branches ---

// TestParseTypeRejectsMalformedEnvelope covers parseType's EnvelopeFromBytes
// error return (garbage bytes are not a TLV envelope).
func TestParseTypeRejectsMalformedEnvelope(t *testing.T) {
	// Version byte 0 is not the current envelope version.
	if _, err := parseType([]byte{0x00}); err == nil {
		t.Fatal("parseType on a malformed envelope must error")
	}
}

// TestParseTypeRejectsEmptyBase covers the "object missing type" branch when
// the versioned type has no unversioned base ("@1" → base "").
func TestParseTypeRejectsEmptyBase(t *testing.T) {
	env := marshalEnvelope("@1", []byte(`{}`))
	if _, err := parseType(env); err == nil {
		t.Fatal("parseType of a type without a base name must error")
	}
}

// TestResolveAnySurfacesBadEnvelope makes ResolveAny surface a parseType error
// (repo.go's error return after the envelope is decoded).
func TestResolveAnySurfacesBadEnvelope(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)
	h := mustStoreEnv(t, repo, "@1", `{}`)
	if _, err := res.ResolveAny(ctx, h); err == nil {
		t.Fatal("ResolveAny of a type without a base name must error")
	}
}

// TestResolveAnyTypeDecodeErrors exercises each switch-case error return in
// ResolveAny: the envelope advertises a known type but the typed store cannot
// decode the payload.
func TestResolveAnyTypeDecodeErrors(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)

	cases := []struct {
		base string
	}{
		{"blob@1"},
		{"tree@1"},
		{"commit@1"},
		{"tag@1"},
	}
	for _, tc := range cases {
		// Payload that is valid envelope bytes but not decodable JSON for the
		// concrete type.
		h := mustStoreEnv(t, repo, tc.base, `{"broken"`)
		ro, err := res.ResolveAny(ctx, h)
		if err == nil {
			t.Fatalf("ResolveAny(%s) with undecodable payload = %+v, want error", tc.base, ro)
		}
	}
}

// --- shortDigest absent guard via PrintObject on a tag with no target ---

func TestPrintObjectAbsentTargetTag(t *testing.T) {
	got := PrintObject(&ResolvedObject{Type: "tag", Tag: &Tag{Name: "v1"}})
	if !strings.Contains(got, "<absent>") {
		t.Fatalf("PrintObject of absent-target tag = %q, want <absent>", got)
	}
}

// --- WalkGraph propagates a child resolution error (dangling reference) ---

func TestWalkGraphDanglingReference(t *testing.T) {
	ctx := ctxBackground()
	repo := newRepo(t, mem.New())
	res := NewResolver(repo)

	missing := mustDigest(t, strings.Repeat("00", 32))
	hc, err := repo.Commits.Put(ctx, &Commit{Tree: ref(missing), Author: "a", Message: "dangling"})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	err = WalkGraph(ctx, res, hc, func(ro *ResolvedObject) error {
		visited = append(visited, ro.Type)
		return nil
	})
	if err == nil {
		t.Fatal("WalkGraph over a dangling tree reference must error")
	}
	if len(visited) != 1 || visited[0] != "commit" {
		t.Fatalf("visited before failure = %v, want only the commit", visited)
	}
}
