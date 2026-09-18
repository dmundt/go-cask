# CASK

CASK is a Git-like, content-addressable storage kit for Go.

It is built around a generic core that stores bytes by content digest, keeps objects immutable, and lets applications layer typed object models on top. The project is intentionally split into a lean `cas` core, a reference `gitlike` model, optional helper packages, and example programs that show how to build on top of it.

## Why this project exists

- deduplicate content by digest
- keep storage generic and app-agnostic
- model object graphs with typed references
- expose a clear backend and codec seam
- make integrity checks explicit and separate from object identity

## Project layout

- `cas/` — generic core library
- `gitlike/` — reference object model built on top of `cas`
- `examples/` — runnable demo apps
- `cmd/` — CLI entry point
- `docs/specs/` — normative design and implementation specs
- `benchmarks/` — benchmarks and performance notes

## Quick start

```bash
go get github.com/dmundt/go-cask
```

Then use the public packages in your own code, or run one of the examples:

```bash
go run ./examples/files --help
go run ./examples/bloom
```

## Documentation map

- [Getting started](getting-started.md)
- [Architecture](architecture.md)
- [Specification set](specs.md)
- [Changelog](changelog.md)
- [GitHub repository](https://github.com/dmundt/go-cask)
