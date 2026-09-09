package cas_test

import (
	"context"
	"fmt"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
)

// note is a minimal Object[T] used to show the generic Store.
type note struct{ Body string }

func (note) Type() string           { return "note@1" }
func (note) References() []cas.Hash { return nil }

// Example shows the typed Store (cas-core §4.8): build one over a backend with
// the JSON codec, Put a value, and Get it back as the concrete type.
func Example() {
	ctx := context.Background()
	s, err := cas.New(backmem.New(), jsoncodec.New[note](), cas.SHA256)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
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

// ExampleHashBytes shows hashing bytes with a registered algorithm (cas-core
// §4.2); the returned Hash carries its algorithm and digest.
func ExampleHashBytes() {
	h, err := cas.HashBytes("sha256", []byte("hi"))
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(h.Algorithm())
	// Output:
	// sha256
}
