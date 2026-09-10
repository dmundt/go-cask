package gitlike_test

import (
	"context"
	"fmt"
	"time"

	memory "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/gitlike"
)

// These Examples are the consumer's view: they live in the external test
// package, so every call below is public API, and the Codecs set is spelled out
// in each one rather than shared through a helper — a rendered Example must be
// copy-pasteable on its own.

// Example shows the reference object model in use (cas-core §4.12): build a
// Repository over a backend with the caller's hasher and codecs, store a Blob,
// and read it back. gitlike names neither the algorithm nor the wire format, so
// the client supplies both.
func Example() {
	ctx := context.Background()
	repo := gitlike.NewRepository(memory.New(), sha256.New(), gitlike.Codecs{
		Blob:   jsoncodec.New[*gitlike.Blob](),
		Tree:   jsoncodec.New[*gitlike.Tree](),
		Commit: jsoncodec.New[*gitlike.Commit](),
		Tag:    jsoncodec.New[*gitlike.Tag](),
	})

	h, err := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hi")})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	blob, err := repo.Blobs.Get(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(string(blob.Data))
	// Output:
	// hi
}

// ExampleRepository shows resolution "of anything": ResolveAny reads the stored
// envelope's type name and returns a typed union, so an unknown digest can be
// inspected without any type assertion.
func ExampleRepository() {
	ctx := context.Background()
	repo := gitlike.NewRepository(memory.New(), sha256.New(), gitlike.Codecs{
		Blob:   jsoncodec.New[*gitlike.Blob](),
		Tree:   jsoncodec.New[*gitlike.Tree](),
		Commit: jsoncodec.New[*gitlike.Commit](),
		Tag:    jsoncodec.New[*gitlike.Tag](),
	})

	h, err := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hi")})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	ro, err := gitlike.NewResolver(repo).ResolveAny(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(ro.Type)
	// Output:
	// blob
}

// ExampleWalkGraph builds the whole graph — blob → tree → commit → tag — and
// walks it from the tag. Each step uses the resolver method matching the
// digest's type: a tag digest resolves through ResolveTag, and only its Target
// is a commit (ResolveCommit on a tag digest fails, because the stored type and
// the requested type must agree). WalkGraph then follows References() until the
// graph is exhausted.
func ExampleWalkGraph() {
	ctx := context.Background()
	repo := gitlike.NewRepository(memory.New(), sha256.New(), gitlike.Codecs{
		Blob:   jsoncodec.New[*gitlike.Blob](),
		Tree:   jsoncodec.New[*gitlike.Tree](),
		Commit: jsoncodec.New[*gitlike.Commit](),
		Tag:    jsoncodec.New[*gitlike.Tag](),
	})
	resolver := gitlike.NewResolver(repo)

	blobHash, err := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello\n")})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	treeHash, err := repo.Trees.Put(ctx, &gitlike.Tree{Entries: []gitlike.TreeEntry{
		{Name: "hello.txt", Hash: blobHash, Mode: "100644"},
	}})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	commitHash, err := repo.Commits.Put(ctx, &gitlike.Commit{
		Tree: treeHash, Author: "Ada", Message: "initial", Time: time.Unix(0, 0).UTC(),
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	tagHash, err := repo.Tags.Put(ctx, &gitlike.Tag{
		Name: "v1.0", Target: commitHash, Tagger: "Ada", Message: "release",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	tag, err := resolver.ResolveTag(ctx, tagHash)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	commit, err := resolver.ResolveCommit(ctx, tag.Target)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	tree, err := resolver.ResolveTree(ctx, commit.Tree)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	blob, err := resolver.ResolveBlob(ctx, tree.Entries[0].Hash)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("tag %s -> commit %q -> %s -> %s\n",
		tag.Name, commit.Message, tree.Entries[0].Name, string(blob.Data))

	err = gitlike.WalkGraph(ctx, resolver, tagHash, func(o *gitlike.ResolvedObject) error {
		fmt.Println("visited:", o.Type)
		return nil
	})
	if err != nil {
		fmt.Println("error:", err)
	}
	// Output:
	// tag v1.0 -> commit "initial" -> hello.txt -> hello
	//
	// visited: tag
	// visited: commit
	// visited: tree
	// visited: blob
}
