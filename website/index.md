# go-cask

Content-addressable store kit for Go applications. Store immutable typed objects
by content-derived `Digest`, verify stored bytes on demand, and keep object
semantics separate from persistence.

[Get started](getting-started.md) · [Architecture](architecture.md) ·
[Specifications](specs.md) · [GitHub](https://github.com/dmundt/go-cask)

## What it provides

| Capability | Contract |
|---|---|
| Content identity | Same envelope bytes yield the same `Digest`; identical objects deduplicate. |
| Typed storage | `Object[T]`, `Codec[T]`, and `Store[T]` keep application types out of the byte layer. |
| Pluggable policy | Applications select their `Hasher`, codec, and `Backend`. |
| Verification | `cas.Verify` and `cas.NewVerifier` recompute a stored object's digest explicitly. |
| Storage backends | Filesystem, in-memory, and an opt-in packfile backend (`cask -backend fs\|packfs`) ship; implementations of `Backend` remain interchangeable. |

## When to reach for it

| Use go-cask when | Use something else when |
|---|---|
| content identity matters more than a filename or row key | you need relational queries, joins, or transactions |
| identical payloads should dedupe automatically | you need multi-writer distributed consensus |
| you want an explicit, swappable storage boundary behind a small interface | you need a full version-control workflow (branches, merges, remotes) |

## Minimal example

This complete program uses the generic core directly with a typed object,
filesystem backend, JSON codec, and SHA-256 hasher.

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

// Note implements cas.Object[*Note]: a versioned type name plus the digests
// it references (none, here).
type Note struct {
    Text string `json:"text"`
}

func (n *Note) Type() string             { return "note@1" }
func (n *Note) References() []cas.Digest { return nil }

func main() {
    ctx := context.Background()

    backend, err := fsbackend.New("./repo")
    if err != nil {
        panic(err)
    }

    store := cas.New(backend, jsoncodec.New[*Note](), sha256.New())

    ref, err := store.Put(ctx, &Note{Text: "hello"})
    if err != nil {
        panic(err)
    }

    loaded, err := store.Get(ctx, ref)
    if err != nil {
        panic(err)
    }
    fmt.Println(loaded.Text)

    // Verification is explicit and separate from storage: it re-reads the
    // bytes and recomputes the digest with the caller's hasher.
    if err := cas.Verify(ctx, backend, ref, sha256.New()); err != nil {
        panic(err)
    }
}
```

The [Getting started](getting-started.md) guide introduces the `gitlike`
reference object model, which builds blobs, trees, commits, and tags on the
same `Store[T]` primitives.

## Documentation

- [Getting started](getting-started.md) — install, first store, next steps
- [Architecture](architecture.md) — layers, data flow, the `gitlike` reference model
- [Concepts](concepts/index.md) — digests, hashers, codecs, backends
- [Recipes](recipes/filesystem-backend.md) — filesystem backend and custom codecs
- [Specifications](specs.md) — pointers into the repository's normative spec set
- [FAQ](faq.md)
- [Changelog](changelog.md)

## Project status

Pure Go — no CGO dependency in the core library. MIT licensed. See
[CI](https://github.com/dmundt/go-cask/actions/workflows/ci.yml) and
[pkg.go.dev](https://pkg.go.dev/github.com/dmundt/go-cask) for build status and
generated API docs.
