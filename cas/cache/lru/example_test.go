package lru_test

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/cache/lru"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// Example shows the size-bounded LRU cache (cas-core §4.10): New wraps a
// Store with an eviction policy and Get serves cached objects.
func Example() {
	ctx := context.Background()
	s, err := cas.New(backmem.New(), jsoncodec.New[item](), "sha256")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	h, err := s.Put(ctx, item{ID: "one"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	c, err := lru.New(s, 100)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	got, err := c.Get(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("id:", got.ID)
	// Output:
	// id: one
}
