package refs_test

import (
	"context"
	"fmt"
	"os"

	"github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/cas/refs"
)

// ExampleStore demonstrates the atomic-write, reflog, and prefix-resolution
// contract a Store provides on top of plain cas.Digest values.
func ExampleStore() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "refs-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	s, err := refs.Open(dir)
	if err != nil {
		panic(err)
	}

	v1 := sha256.Of([]byte("release 1"))
	v2 := sha256.Of([]byte("release 2"))

	if err := s.Set(ctx, "release/v1", v1); err != nil {
		panic(err)
	}
	if err := s.Set(ctx, "release/v1", v2); err != nil {
		panic(err)
	}

	current, err := s.Get(ctx, "release/v1")
	if err != nil {
		panic(err)
	}
	fmt.Println("current:", current.Equal(v2))

	previous, err := s.Previous(ctx, "release/v1")
	if err != nil {
		panic(err)
	}
	fmt.Println("previous:", previous.Equal(v1))

	resolved, err := s.Resolve(ctx, "release/v1")
	if err != nil {
		panic(err)
	}
	fmt.Println("resolved:", resolved.Name)

	roots, err := s.Roots(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println("roots:", len(roots))

	// Output:
	// current: true
	// previous: true
	// resolved: release/v1
	// roots: 1
}

// ExampleStore_Log shows the reflog: newest first, capped by limit, and
// still readable after the ref itself is deleted.
func ExampleStore_Log() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "refs-example-log")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	s, err := refs.Open(dir)
	if err != nil {
		panic(err)
	}

	v1 := sha256.Of([]byte("a"))
	v2 := sha256.Of([]byte("b"))
	if err := s.Set(ctx, "main", v1); err != nil {
		panic(err)
	}
	if err := s.Set(ctx, "main", v2); err != nil {
		panic(err)
	}
	if err := s.Delete(ctx, "main"); err != nil {
		panic(err)
	}

	log, err := s.Log(ctx, "main", 0)
	if err != nil {
		panic(err)
	}
	fmt.Println("entries:", len(log))
	fmt.Println("newest is delete:", log[0].Digest.IsZero())
	fmt.Println("oldest had no previous value:", log[len(log)-1].Old.IsZero())

	// Output:
	// entries: 3
	// newest is delete: true
	// oldest had no previous value: true
}
