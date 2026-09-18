# CASK

CASK is a Git-like content-addressable storage kit for Go.

It gives you a generic byte-store core, typed object layers, hash-agnostic identity, and explicit verification seams without forcing one application model into the storage layer. The design stays small: the core stores bytes by digest, a codec serializes your type, a backend persists the bytes, and the object graph sits on top.

## Why this project exists

- deduplicate content by digest
- keep storage generic and app-agnostic
- model object graphs with typed references
- keep the hash, codec, and backend as independent seams
- separate object identity from integrity validation

## Mental model

```mermaid
flowchart LR
    A[Go object] --> B[Codec[T]]
    B --> C[Bytes]
    C --> D[Hash]
    D --> E[Digest]
    E --> F[Backend]
    F --> G[Content-addressed storage]
```

## Project layout

- `cas/` — generic core library
- `gitlike/` — reference object model built on top of `cas`
- `examples/` — runnable demo apps
- `cmd/` — CLI entry point
- `docs/specs/` — normative implementation and design specs
- `benchmarks/` — benchmarks and performance notes

## Quick start

```bash
go get github.com/dmundt/go-cask
```

Use the package in your own app or run the example programs:

```bash
go run ./examples/files --help
go run ./examples/bloom
```

## Documentation map

- [Getting started](getting-started.md)
- [Architecture](architecture.md)
- [Concepts](concepts/index.md)
- [Specification set](specs.md)
- [Changelog](changelog.md)
- [Go docs](https://pkg.go.dev/github.com/dmundt/go-cask)
- [GitHub repository](https://github.com/dmundt/go-cask)
