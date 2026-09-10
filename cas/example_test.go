package cas_test

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// note is a minimal Object[T] used to show the generic Store.
type note struct{ Body string }

func (note) Type() string             { return "note@1" }
func (note) References() []cas.Digest { return nil }

// Example shows the typed Store (cas-core §4.8): build one over a backend with
// the JSON codec and the client's hasher, Put a value, and Get it back as the
// concrete type.
func Example() {
	ctx := context.Background()
	s := cas.New(backmem.New(), jsoncodec.New[note](), sha256.New())
	h, err := s.Put(ctx, note{Body: "hi"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	n, err := s.Get(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(n.Body)
	// Output:
	// hi
}
