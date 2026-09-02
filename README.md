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

## Design principles & grounding

go-cask is a **single-host content-addressed storage kit**. The durable
decisions that shape the repo (each named spec is the normative contract):

- **No network surface ships.** The product has no CAS JSON API, no client
  SDK, and no server binary — it is `cas` + the CLI + the embedded viewer.
  HTTP exposure is an app-author pattern, demonstrated by `examples/api`
  (backend-architecture §1).
- **The viewer is a byte-layer admin tool.** It shows objects, bytes, and
  integrity — never typed references or graphs — and product code never
  imports `examples/` (viewer-design §7, coding-guidelines §9).
- **Dependencies are one-directional.** `cas`/`internal`/`cmd` never import
  `examples/`; examples never import `internal/` and are self-contained
  except the `gitlike` shared reference library (examples §2 rule 11).
- **Lean generic core with reference implementations.** `cas` stays
  app-agnostic; each pluggable seam ships one reference (`sha1`/`sha256`,
  `MemoryRawStore`, `JSONCodec`), and only the cas-core §7.1 surface is
  stable — speculative surface is cut, not kept.
- **The byte layer is policy-free.** GC/prune take app-supplied roots;
  roots are pins (there is no per-object pinned property); the store never
  interprets typed references (consistency §4).
- **Examples teach, never ship.** `gitlike` is the shared reference object
  model; `artifacts` shows the compression-codec seam; `api` shows how an
  app exposes a store over HTTP.

## Repository layout

```text
cas/       core library (package cas) — generic, app-agnostic, public
internal/  implementation detail: web (the viewer), index
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
