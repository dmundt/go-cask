# Filesystem backend recipe

`cas/backend/fs` is the durable, default backend: each object is one file
under `<base>/<fan-out dirs>/<full-hex-digest>`, written atomically.

## When to use it

- a local, persistent object store for a single host
- a developer tool or CLI that needs durable, inspectable artifacts
- the simplest backend to reason about before writing a custom one

## Example

```go
package main

import (
    "context"
    "fmt"

    "github.com/dmundt/go-cask/cas"
    fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
    jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
    sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

type Note struct {
    Text string `json:"text"`
}

func (n *Note) Type() string             { return "note@1" }
func (n *Note) References() []cas.Digest { return nil }

func main() {
    ctx := context.Background()

    raw, err := fsbackend.New("./repo")
    if err != nil {
        panic(err)
    }

    store := cas.New(raw, jsoncodec.New[*Note](), sha256.New())

    ref, err := store.Put(ctx, &Note{Text: "hello"})
    if err != nil {
        panic(err)
    }
    fmt.Println(ref)
}
```

## Layout and options

The default layout is the Git-like `FanOut=2`, `FanLevels=1` (`aa/<full hex>`).
Both are configurable:

```go
raw, err := fsbackend.New("./repo",
    fsbackend.WithFanOut(2),
    fsbackend.WithFanLevels(1),
)
```

## Good fit

- local content-addressable stores
- developer tooling and CLIs
- object graphs and immutable content handling
- examples that need a persistent, inspectable layout

## Constraints

- the backend is a raw storage layer only — it does not own application
  semantics, codecs, or hashing
- one filesystem backend base directory belongs to exactly one store; do not
  nest one store's base inside another's
- the backend never verifies the digest of what it stores or returns — use
  `cas.Verify` for that
