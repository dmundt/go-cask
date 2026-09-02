# CASK — Content Addressed Storage Kit

[![CI](https://github.com/dmundt/go-cask/actions/workflows/ci.yml/badge.svg)](https://github.com/dmundt/go-cask/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/badge/Go-1.27-blue)](https://github.com/dmundt/go-cask)
[![License](https://img.shields.io/github/license/dmundt/go-cask)](LICENSE)

A generic, Git-like, **content-addressed storage** library for Go: store any bytes once under the hash of their content, reference them by
hash, and build typed object graphs on top — reusable across apps and
domains.

- **Content-addressed** — same bytes ⇒ same hash ⇒ stored once (dedup).
- **Immutable & verifiable** — objects never change; `Verify` detects any
  corruption.
- **Generic core, typed apps** — the `cas` core knows nothing about your
  types; each app layers its own `Object[T]` model on top (the `gitlike`
  package is the reference example).
- **Pluggable** — hash algorithms, codecs, and storage backends
  (filesystem, memory, S3, …) behind one `RawStore` contract.
- **Simple, fast, powerful** — lock-free reads, streaming I/O, semver-versioned
  object models, GC from roots, age-based pruning — no over-engineering.

## Repository layout

```text
cas/       core library (package cas) — generic, app-agnostic, public
internal/  implementation detail: web (the viewer), storage, index
examples/  runnable example programs (incl. the gitlike reference object model)
cmd/       entry point: cask (CLI store ops; `cask web` starts the embedded viewer)
.github/   specification set + CI (see below)
```

## Quick start

```go
import (
    "github.com/dmundt/go-cask/cas"
    "github.com/dmundt/go-cask/examples/gitlike"
)

raw, _ := cas.NewFSRawStore("./objects")          // backend
repo, _ := gitlike.NewRepository(raw, "sha256")   // typed layer on top
h, _ := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello")})
blob, _ := repo.Blobs.GetTyped(ctx, h)            // *gitlike.Blob
```

For tests and ephemeral use, swap the backend:

```go
raw := cas.NewMemoryRawStore() // fast, deterministic, not persistent
```

## The specification set

This project is specified, not guessed: `.github/instructions/` contains the
complete design contract — core architecture (`cas-core`), coding guidelines,
library design, performance, testing, consistency (GC/pruning), the viewer
HTTP surface, viewer design & security, versioning, defaults, examples,
and extensions. `AGENT.md` in that folder is the meta-guide; read it before
editing any spec. The full inventory is in `AGENT.md` §10.

## Building & testing

```text
go build ./...
go vet ./...
go test -race ./...
gofmt -l .
```

Requires Go 1.27 (library baseline: Go 1.21+). See `CONTRIBUTING.md` for the
development workflow.

## License

MIT — see [LICENSE](LICENSE). Copyright (c) 2026 Daniel Mundt.
