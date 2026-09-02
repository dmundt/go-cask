package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dmundt/go-cask/cas"
)

// demo builds a small document graph and exercises cross-type resolution,
// lazy attachment loading, SmartCache prefetch, broken-reference detection,
// and the generic Walker[T] over a same-type related chain.
func demo() error {
	ctx := context.Background()
	raw, err := cas.NewFSRawStore("./objects")
	if err != nil {
		return err
	}
	repo, err := newRepository(raw)
	if err != nil {
		return err
	}
	res := newResolver(repo)

	// Lazy attachment cache (large blobs load on demand) and the prefetch
	// cache for notes.
	attachments := cas.NewCachedStore(repo.Attachments)
	noteCache := cas.NewCachedStore(repo.Notes)
	smart := NewSmartCache(noteCache, 2)

	// Tags.
	workTag, err := repo.Tags.Put(ctx, &Tag{Name: "work"})
	if err != nil {
		return err
	}
	ideaTag, err := repo.Tags.Put(ctx, &Tag{Name: "idea"})
	if err != nil {
		return err
	}

	// A large attachment (loaded lazily).
	att, err := repo.Attachments.Put(ctx, &Attachment{Data: make([]byte, 1<<20)})
	if err != nil {
		return err
	}

	// Notes: a related chain (for Walker[T]) plus cross-type references.
	second, err := repo.Notes.Put(ctx, &Note{Title: "second", Body: "related note"})
	if err != nil {
		return err
	}
	first, err := repo.Notes.Put(ctx, &Note{
		Title:       "first",
		Body:        "the root note",
		Tags:        []cas.Hash{workTag, ideaTag},
		Attachments: []cas.Hash{att},
		Related:     []cas.Hash{second},
	})
	if err != nil {
		return err
	}

	// Cross-type resolution.
	ro, err := res.ResolveAny(ctx, first)
	if err != nil {
		return err
	}
	fmt.Printf("resolved: %s %q (tags=%d attachments=%d related=%d)\n",
		ro.Type, ro.Note.Title, len(ro.Note.Tags), len(ro.Note.Attachments), len(ro.Note.Related))
	for _, th := range ro.Note.Tags {
		t, err := res.ResolveTag(ctx, th)
		if err != nil {
			return err
		}
		fmt.Printf("  tag: %s\n", t.Name)
	}

	// Lazy loading: the attachment is NOT loaded until accessed.
	co, err := attachments.Get(ctx, att)
	if err != nil {
		return err
	}
	fmt.Printf("attachment loaded before access: %v\n", co.IsLoaded())
	obj, err := co.Load(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("attachment loaded after access: %v (%d bytes)\n", co.IsLoaded(), len(obj.Data))

	// SmartCache prefetch: loading a related-only note warms its references.
	prefetchRoot, err := repo.Notes.Put(ctx, &Note{Title: "prefetch-root", Related: []cas.Hash{second}})
	if err != nil {
		return err
	}
	if _, err := smart.GetWithPrefetch(ctx, prefetchRoot); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if noteCache.CacheStats().Size >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("note cache after prefetch: size=%d\n", noteCache.CacheStats().Size)

	// Broken reference: a note pointing at a hash that was never stored.
	// The note itself resolves; its dangling reference is reported when the
	// graph is walked.
	missing, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	broken, err := repo.Notes.Put(ctx, &Note{Title: "broken", Related: []cas.Hash{missing}})
	if err != nil {
		return err
	}
	ro, err = res.ResolveAny(ctx, broken)
	if err != nil {
		return err
	}
	for _, ref := range ro.Note.References() {
		if _, err := res.ResolveAny(ctx, ref); err != nil {
			fmt.Printf("broken reference detected: %s -> %v\n", ref, err)
		}
	}

	// Generic Walker[T] over a pure same-type related chain (cross-type
	// refs are the resolver's job, not the single-type Walker's).
	leaf, err := repo.Notes.Put(ctx, &Note{Title: "chain-c"})
	if err != nil {
		return err
	}
	mid, err := repo.Notes.Put(ctx, &Note{Title: "chain-b", Related: []cas.Hash{leaf}})
	if err != nil {
		return err
	}
	root, err := repo.Notes.Put(ctx, &Note{Title: "chain-a", Related: []cas.Hash{mid}})
	if err != nil {
		return err
	}
	seen := 0
	w := cas.NewWalker(repo.Notes, func(obj *Note) error {
		seen++
		fmt.Printf("walk: %s\n", obj.Title)
		return nil
	})
	if err := w.Walk(ctx, root); err != nil {
		return err
	}
	fmt.Printf("walked %d notes\n", seen)
	return nil
}

func main() {
	if err := demo(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
