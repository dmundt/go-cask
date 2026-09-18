# Getting started

go-cask gives Go applications stable, content-derived object identities. You can store typed data, verify integrity, and swap storage backends without rewriting your application model.

## Install

```bash
go get github.com/dmundt/go-cask
```

## Minimal example

This example creates a filesystem-backed store and stores a Git-like blob object.

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

## Important mental model

The core idea is straightforward:

- content determines object identity
- the digest is the object key
- the codec owns serialization
- the backend owns storage
- the application owns semantics

## Run the examples

The repository includes a few runnable examples that show different patterns.

```bash
go run ./examples/files --help
go run ./examples/bloom
go run ./examples/notes
```

## Next steps

- read the [architecture overview](architecture.md)
- review the [concepts](concepts/index.md)
- browse the [specifications](specs.md)
- start from a small local backend and grow into custom codecs or hash policies as needed

## CI and verification

Before committing meaningful changes, run the project verification gate:

```bash
bash ./scripts/verify.sh
```

This keeps the repository in a releasable state and catches common breakage early.
