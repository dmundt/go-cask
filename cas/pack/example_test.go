package pack_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
	"github.com/dmundt/go-cask/cas/pack"
)

func ExampleSplit() {
	payload := []byte("hello world")
	parts := pack.Split(payload, 5)
	fmt.Println(len(parts))
	fmt.Println(string(pack.Join(parts)))
	// Output:
	// 3
	// hello world
}

// ExampleSaveWith shows the codec seam: the JSON codec is the caller's, named at
// the call site, and the pack layer never chooses a format itself (#307).
func ExampleSaveWith() {
	dir, err := os.MkdirTemp("", "pack-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	metaPath := filepath.Join(dir, "meta.json")
	meta := pack.Data{"kind": "artifact", "owner": "team-a"}
	codec := jsoncodec.New[pack.Data]()
	if err := pack.SaveWith(context.Background(), metaPath, meta, codec); err != nil {
		panic(err)
	}
	loaded, err := pack.LoadWith(context.Background(), metaPath, codec)
	if err != nil {
		panic(err)
	}
	fmt.Println(loaded["kind"])
	fmt.Println(loaded["owner"])
	// Output:
	// artifact
	// team-a
}
