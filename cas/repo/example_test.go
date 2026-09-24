package repo_test

import (
	"context"
	"fmt"
	"sort"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/repo"
)

// note and collection are two independent Object[T] types, each with its own
// Store, wired into one Registry so a digest of either type can be resolved,
// walked, and reachability-checked without the caller ever branching on type.

type note struct {
	Title string `json:"title"`
}

func (note) Type() string             { return "note@1" }
func (note) References() []cas.Digest { return nil }

type collection struct {
	Name  string       `json:"name"`
	Notes []cas.Digest `json:"notes,omitempty"`
}

func (collection) Type() string { return "collection@1" }
func (c collection) References() []cas.Digest {
	refs := make([]cas.Digest, 0, len(c.Notes))
	for _, d := range c.Notes {
		if !d.IsZero() {
			refs = append(refs, d)
		}
	}
	return refs
}

// ExampleRegistry builds a two-type graph (a collection referencing two
// notes), then uses one Registry to Walk it and to compute Reachable — the
// root set Backend.GC/Backend.Prune require.
func ExampleRegistry() {
	ctx := context.Background()
	backend := backmem.New()
	hasher := sha256.New()

	notes := cas.New(backend, jsoncodec.New[note](), hasher)
	collections := cas.New(backend, jsoncodec.New[collection](), hasher)

	registry := repo.NewRegistry(backend, hasher)
	if err := repo.RegisterStore(registry, note{}.Type(), notes); err != nil {
		panic(err)
	}
	if err := repo.RegisterStore(registry, collection{}.Type(), collections); err != nil {
		panic(err)
	}

	n1, err := notes.Put(ctx, note{Title: "shopping list"})
	if err != nil {
		panic(err)
	}
	n2, err := notes.Put(ctx, note{Title: "todo"})
	if err != nil {
		panic(err)
	}
	root, err := collections.Put(ctx, collection{Name: "home", Notes: []cas.Digest{n1, n2}})
	if err != nil {
		panic(err)
	}

	var visited []string
	if err := repo.Walk(ctx, registry, []cas.Digest{root}, func(_ cas.Digest, obj repo.Object) error {
		visited = append(visited, obj.Type())
		return nil
	}); err != nil {
		panic(err)
	}
	sort.Strings(visited)
	fmt.Println("visited:", visited)

	reachable, err := repo.Reachable(ctx, registry, []cas.Digest{root})
	if err != nil {
		panic(err)
	}
	fmt.Println("reachable:", len(reachable))

	// Output:
	// visited: [collection@1 note@1 note@1]
	// reachable: 3
}
