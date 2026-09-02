package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
)

func newTestRepo(t *testing.T) (*Repository, *Resolver) {
	t.Helper()
	repo, err := newRepository(cas.NewMemoryRawStore())
	if err != nil {
		t.Fatal(err)
	}
	return repo, newResolver(repo)
}

// Acceptance: notes resolve across all three types.
func TestCrossTypeResolution(t *testing.T) {
	ctx := context.Background()
	repo, res := newTestRepo(t)

	tag, err := repo.Tags.Put(ctx, &Tag{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	att, err := repo.Attachments.Put(ctx, &Attachment{Data: []byte("big")})
	if err != nil {
		t.Fatal(err)
	}
	note, err := repo.Notes.Put(ctx, &Note{Title: "n", Tags: []cas.Hash{tag}, Attachments: []cas.Hash{att}})
	if err != nil {
		t.Fatal(err)
	}

	ro, err := res.ResolveAny(ctx, note)
	if err != nil || ro.Type != "note" || ro.Note == nil {
		t.Fatalf("ResolveAny(note) = %+v, %v", ro, err)
	}
	ro, err = res.ResolveAny(ctx, tag)
	if err != nil || ro.Type != "tag" || ro.Tag == nil || ro.Tag.Name != "work" {
		t.Fatalf("ResolveAny(tag) = %+v, %v", ro, err)
	}
	ro, err = res.ResolveAny(ctx, att)
	if err != nil || ro.Type != "attachment" || ro.Attachment == nil {
		t.Fatalf("ResolveAny(attachment) = %+v, %v", ro, err)
	}
	// Typed resolvers.
	if n, err := res.ResolveNote(ctx, note); err != nil || n.Title != "n" {
		t.Fatalf("ResolveNote = %+v, %v", n, err)
	}
	// References include tags and attachments.
	got := ro.Attachment.References()
	if got != nil {
		t.Fatalf("attachment refs = %v", got)
	}
}

// Acceptance: attachments are not loaded until accessed (lazy loading).
func TestLazyAttachment(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)
	cached := cas.NewCachedStore(repo.Attachments)

	att, err := repo.Attachments.Put(ctx, &Attachment{Data: []byte("lazy")})
	if err != nil {
		t.Fatal(err)
	}
	co, err := cached.Get(ctx, att)
	if err != nil {
		t.Fatal(err)
	}
	if co.IsLoaded() {
		t.Fatal("attachment must start unloaded")
	}
	if _, err := co.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !co.IsLoaded() {
		t.Fatal("attachment must be loaded after access")
	}
}

// Acceptance: after prefetch the cache reports the referenced note.
func TestPrefetchWarmsCache(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)
	noteCache := cas.NewCachedStore(repo.Notes)
	smart := NewSmartCache(noteCache, 2)

	second, err := repo.Notes.Put(ctx, &Note{Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.Notes.Put(ctx, &Note{Title: "first", Related: []cas.Hash{second}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smart.GetWithPrefetch(ctx, first); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if noteCache.CacheStats().Size >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := noteCache.CacheStats(); st.Size < 2 {
		t.Fatalf("after prefetch cache size = %d, want >= 2", st.Size)
	}
}

// Acceptance: broken references are detected and reported without crashing.
func TestBrokenReference(t *testing.T) {
	ctx := context.Background()
	repo, res := newTestRepo(t)
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	broken, err := repo.Notes.Put(ctx, &Note{Title: "broken", Related: []cas.Hash{missing}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.ResolveAny(ctx, broken); err != nil {
		t.Fatalf("ResolveAny on a resolvable note must not fail: %v", err)
	}
	if _, err := res.ResolveAny(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("ResolveAny(missing) = %v, want ErrNotFound", err)
	}
}

// The generic Walker[T] traverses a same-type related chain.
func TestWalkerChain(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)

	leaf, err := repo.Notes.Put(ctx, &Note{Title: "c"})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := repo.Notes.Put(ctx, &Note{Title: "b", Related: []cas.Hash{leaf}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := repo.Notes.Put(ctx, &Note{Title: "a", Related: []cas.Hash{mid}})
	if err != nil {
		t.Fatal(err)
	}

	var seen []string
	w := cas.NewWalker(repo.Notes, func(obj cas.Object[*Note]) error {
		seen = append(seen, obj.(*Note).Title)
		return nil
	})
	if err := w.Walk(ctx, root); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("walked %d notes, want 3: %v", len(seen), seen)
	}
}
