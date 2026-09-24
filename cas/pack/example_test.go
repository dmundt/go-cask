package pack_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

func ExampleSaveJSON() {
	dir, err := os.MkdirTemp("", "pack-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	metaPath := filepath.Join(dir, "meta.json")
	meta := pack.Data{"kind": "artifact", "owner": "team-a"}
	if err := pack.SaveJSON(context.Background(), metaPath, meta); err != nil {
		panic(err)
	}
	loaded, err := pack.LoadJSON[pack.Data](context.Background(), metaPath)
	if err != nil {
		panic(err)
	}
	fmt.Println(loaded["kind"])
	fmt.Println(loaded["owner"])
	// Output:
	// artifact
	// team-a
}
