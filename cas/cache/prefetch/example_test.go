package prefetch_test

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	memcache "github.com/dmundt/go-cask/cas/cache/mem"
	"github.com/dmundt/go-cask/cas/cache/prefetch"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// Example shows prefetch-on-access (cas-core §4.10): NewSmartCache wraps a
// CachedStore and GetWithPrefetch loads an object, warming the cache with its
// references.
func Example() {
	ctx := context.Background()
	s := cas.New(backmem.New(), jsoncodec.New[testObject](), sha256.New())
	h, err := s.Put(ctx, testObject{Name: "root"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	sc := prefetch.NewSmartCache(memcache.New(s), 2)
	got, err := sc.GetWithPrefetch(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("name:", got.Name)
	// Output:
	// name: root
}
