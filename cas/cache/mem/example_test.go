package memory_test

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	cachemem "github.com/dmundt/go-cask/cas/cache/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// Example shows the lazy, in-memory CachedStore (cas-core §4.10): New wraps a
// Store, Proxy hands out a not-yet-loaded CachedObject, and Get loads exactly
// once.
func Example() {
	ctx := context.Background()
	s, err := cas.New(backmem.New(), jsoncodec.New[testObject](), "sha256")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	h, err := s.Put(ctx, testObject{Name: "alpha"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	c := cachemem.New(s)
	proxy, err := c.Proxy(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("loaded before:", proxy.IsLoaded())
	got, err := c.Get(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("loaded after:", proxy.IsLoaded())
	fmt.Println("name:", got.Name)
	// Output:
	// loaded before: false
	// loaded after: true
	// name: alpha
}
