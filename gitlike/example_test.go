package gitlike

import (
	"context"
	"fmt"

	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// Example shows the reference object model in use (cas-core §4.12): build a
// Repository over a backend with the caller's hasher and codecs, store a Blob,
// and read it back. gitlike names no codec, so the repository is always built
// with an explicit Codecs set (see jsonCodecs in gitlike_test.go).
func Example() {
	ctx := context.Background()
	repo := NewRepository(backmem.New(), sha256.New(), jsonCodecs())
	h, err := repo.Blobs.Put(ctx, &Blob{Data: []byte("hi")})
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

// ExampleRepository shows cross-type resolution: ResolveAny reads the stored
// envelope and returns a typed union whose Type names the object.
func ExampleRepository() {
	ctx := context.Background()
	repo := NewRepository(backmem.New(), sha256.New(), jsonCodecs())
	h, err := repo.Blobs.Put(ctx, &Blob{Data: []byte("hi")})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	ro, err := NewResolver(repo).ResolveAny(ctx, h)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(ro.Type)
	// Output:
	// blob
}
