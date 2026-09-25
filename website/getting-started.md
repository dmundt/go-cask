# Getting started

go-cask gives Go applications content-derived object identity: store typed
data, verify integrity on demand, and swap storage backends behind a small
interface.

## Install

```bash
go get github.com/dmundt/go-cask
```

## First store

Start with the complete generic-core program on the [home page](index.md).
It defines one `Object[T]`, opens the filesystem backend, stores the object,
retrieves it by `Digest`, and verifies the stored bytes. Keep one `Store[T]`
per application object type.

## Example: the `gitlike` reference model

`gitlike` is a separate, reusable object model (blob/tree/commit/tag) built on
the same `Store[T]`. Use it directly, or copy its pattern for your own graph
shape.

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
    backend, err := fs.New("./repo")
    if err != nil {
        panic(err)
    }

    repo := gitlike.NewRepository(backend, sha256.New(), gitlike.Codecs{
        Blob:   jsoncodec.New[*gitlike.Blob](),
        Tree:   jsoncodec.New[*gitlike.Tree](),
        Commit: jsoncodec.New[*gitlike.Commit](),
        Tag:    jsoncodec.New[*gitlike.Tag](),
    })

    // Blobs.Put returns a cas.Digest — the content address of the blob.
    blobDigest, err := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello")})
    if err != nil {
        panic(err)
    }

    fmt.Println(blobDigest)
}
```

## Important mental model

- content determines object identity
- the digest is the object key (it covers the whole envelope: the version, the
  codec tag, the type name, and the encoded payload)
- the codec owns serialization
- the backend owns storage and never checks the digest itself
- verification is explicit (`cas.Verify`) — nothing verifies automatically on
  every read
- the application owns semantics: `cas` and `gitlike` do not know what a
  "note" or a "commit" means to your app

## Run the examples

The repository ships six runnable example programs. From the repo root:

```bash
go run ./examples/files --help
go run ./examples/bloom
go run ./examples/notes
go run ./examples/artifacts -store ./store put app v1.bin
go run ./examples/pack roundtrip 8 "hello world"
go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080
```

Each `examples/` program is self-contained and documented in its own
`README.md`, which lists its full command set. An example imports the module's
libraries (`cas`, and `gitlike` for the reference object model) and nothing else
outside itself.

## Next steps

- read the [architecture overview](architecture.md)
- review the [concepts](concepts/index.md)
- browse the [specifications](specs.md)
- start from a small filesystem-backed store and grow into custom codecs or
  hash algorithms as needed

## CI and verification

Before committing meaningful changes, run the project verification gate. It is
a Bash script (Git Bash or WSL on Windows):

```bash
bash ./scripts/verify.sh
```

This runs `gofmt`, `go vet`, the import-boundary checks, `govulncheck`, and
the test suite with race detection and coverage — the same gate CI runs.
