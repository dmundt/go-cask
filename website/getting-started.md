# Getting started

## Install

```bash
go mod init example.com/myapp
go get github.com/dmundt/go-cask
```

## Minimal example

```go
package main

import (
    "context"
    "fmt"

    fs "github.com/dmundt/go-cask/cas/backend/fs"
    jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
    sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
    "github.com/dmundt/go-cask/gitlike"
)

func main() {
    ctx := context.Background()
    raw, err := fs.New("./repo")
    if err != nil {
        panic(err)
    }

    repo := gitlike.NewRepository(raw, sha256.New(), gitlike.Codecs{
        Blob:   jsoncodec.New[*gitlike.Blob](),
        Tree:   jsoncodec.New[*gitlike.Tree](),
        Commit: jsoncodec.New[*gitlike.Commit](),
        Tag:    jsoncodec.New[*gitlike.Tag](),
    })

    blob, err := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello")})
    if err != nil {
        panic(err)
    }

    fmt.Println(blob)
}
```

## Run the examples

```bash
go run ./examples/files --help
go run ./examples/bloom
go run ./examples/notes
```

## CI and verification

The project includes a repository verification gate:

```bash
bash ./scripts/verify.sh
```

Use this before committing significant changes.
