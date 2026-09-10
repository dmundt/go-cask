# CASK — Content Addressable Store Kit

[![CI](https://github.com/dmundt/go-cask/actions/workflows/ci.yml/badge.svg)](https://github.com/dmundt/go-cask/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dmundt/go-cask.svg)](https://pkg.go.dev/github.com/dmundt/go-cask)
[![License](https://img.shields.io/github/license/dmundt/go-cask)](LICENSE)

A generic, Git-like **content-addressable store** for Go: store any bytes once under the digest of their content, reference them by digest, and build typed object graphs on top — reusable across apps and domains.

- **Content-addressable** — same bytes ⇒ same digest ⇒ stored once (dedup).
- **Immutable & verifiable** — objects never change; `Verify` detects corruption.
- **Generic core, typed apps** — the `cas` core knows nothing about your types; each app layers its own `Object[T]` model on top (the `gitlike` package is the shared reference object model).
- **Composable** — codecs and storage backends (filesystem + memory ship) plug in behind one `Backend` contract, and the client injects its hash algorithm (`cas/hash/sha256` ships as go-cask's default; the core names none).
- **Simple, fast, powerful** — lock-free reads, streaming I/O, multi-process-safe writers, semver-versioned object models, GC from roots with a Git-style grace period.

## Design decisions

A **single-host content-addressable store kit**. Each named spec is the normative contract:
- **No network surface ships.** Product = `cas` + CLI + embedded viewer; no CAS JSON API, SDK, or server binary. HTTP exposure is an app pattern (`examples/api`) — backend-architecture §1.
- **Viewer is a byte-layer admin tool** — objects/bytes/integrity, never typed references; product code never imports `examples/` (viewer-design §7, coding-guidelines §9).
- **Dependencies one-directional** — `cas`/`internal`/`cmd` never import `examples/`; examples are self-contained except the shared `gitlike` library.
- **Lean generic core** — app-agnostic `cas` that names no hash algorithm (the client injects a `cas.Hasher`; `cas/hash/sha256` is go-cask's default), reference `fs`+`mem` backends and a JSON codec; only the cas-core §7.1 surface is stable.
- **Byte layer policy-free** — GC/prune take app roots; no per-object pinned property; the store never interprets typed references (consistency §4).
- **Concurrent by construction** — writes safe across processes (unique temps + atomic rename); sweeps (`gc`/`prune`/`clean`) hold an exclusive lock and reclaim only objects older than `--min-age`, so fresh writes survive (cas-core §6).
- **Examples teach; `gitlike/` is the shared reference** — the runnable examples teach seams (`artifacts` = compression codec, `api` = HTTP exposure); gitlike is a reference/copy-source object model apps import or copy.

## Repository layout

```text
cas/       core library (package cas) — generic, app-agnostic, public
internal/  implementation detail: web (the viewer), index
gitlike/  shared reference object-model library (package gitlike)
examples/  runnable example programs
benchmarks/  benchmark suite (bench_test.go + scale_bench_test.go) + README.md
cmd/       entry point: cask (CLI store ops; `cask web` starts the embedded viewer)
docs/specs/  the specification set (19 specs + AGENT.md)
docs/design/  non-normative design docs (core-overview pointer, viewer-brief)
AGENTS.md  the agent aggregator at the repo root
.github/   CI only
```

## Core interfaces at a glance

`cas` layers a non-generic **byte layer** (`Digest`, `Backend` + backends) under a generic **typed layer** (`Object[T]`, `Codec[T]`, `Store[T]`, `Walker[T]`), with caching wrappers on top. A store also carries the client's `Hasher` — the algorithm seam. Apps build their own `Object[T]` models on `Store[T]`.

```mermaid
classDiagram
    direction TB
    class Digest { 
        +String() string +Equal(other Digest) bool 
    }
    class Hasher { 
        <<interface>>
        +Digest(r io.Reader) (Digest, error)
        +Validate(d Digest) error
    }
    class Backend { 
        <<interface>>
        +Put(ctx, d, r) error
        +Get(ctx, d) io.ReadCloser
        +Exists(ctx, d) (bool, error)
        +Delete(ctx, d) error
        +List(ctx) ([]Digest, error)
        +Stats(ctx) (*Stats, error)
    }
    class FSBackend { 
        <<backend>>
    }
    class MemBackend { 
        <<backend>>
    }
    Backend <|.. FSBackend : implements
    Backend <|.. MemBackend : implements
    class Object~T~ {
        <<interface>>
        +Type() string
        +References() []Digest
    }
    class Validator {
        <<interface>>
        +Validate() error
    }
    class Codec~T~ {
        <<interface>>
        +Marshal(v T) ([]byte, error)
        +Unmarshal(data []byte) (T, error)
    }
    class JsonCodec["json.Codec~T~ (cas/codec/json)"]
    <<codec>> JsonCodec
    class GobCodec["gob.Codec~T~ (cas/codec/gob)"]
    <<codec>> GobCodec
    class Envelope {
        +Type string
        +Data []byte
    }
    Envelope : +EnvelopeFromBytes(data) (Envelope, error)
    Codec~T~ <|.. JsonCodec : implements
    Codec~T~ <|.. GobCodec : implements
    Store~T~ ..> Envelope : wraps codec payload
    class Store~T~ {
        +Put(ctx, obj T) (Digest, error)
        +Get(ctx, d) (T, error)
        +Delete(ctx, d) error
    }
    class Walker~T~ {
        +Walk(ctx, d) error
    }
    Store~T~ o-- Backend : raw
    Store~T~ o-- Codec~T~ : codec
    Store~T~ o-- Hasher : hasher
    Store~T~ ..> Object~T~ : stores
    Store~T~ ..> Validator : enforces when T declares it
    Walker~T~ ..> Store~T~ : reads via Get
    class CachedStore~T~
    class LRUCache~T~
    CachedStore~T~ o-- Store~T~ : wraps
    LRUCache~T~ --|> CachedStore~T~ : extends
```

## Quick start

```go
import (
    fs "github.com/dmundt/go-cask/cas/backend/fs" // or use the mem backend
    "github.com/dmundt/go-cask/cas"
    jsoncodec "github.com/dmundt/go-cask/cas/codec/json"
    sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
    "github.com/dmundt/go-cask/gitlike"
)

raw, _ := fs.New("./objects")  // backend
// typed layer: the client supplies both the hasher and the codecs, so the
// repository names neither the algorithm nor the wire format.
repo := gitlike.NewRepository(raw, sha256.New(), gitlike.Codecs{
    Blob:   jsoncodec.New[*gitlike.Blob](),
    Tree:   jsoncodec.New[*gitlike.Tree](),
    Commit: jsoncodec.New[*gitlike.Commit](),
    Tag:    jsoncodec.New[*gitlike.Tag](),
})
d, _ := repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello")})
blob, _ := repo.Blobs.Get(ctx, d)                 // *gitlike.Blob
```

For tests/ephemeral use, swap the backend:

```go
mem "github.com/dmundt/go-cask/cas/backend/mem" // declares package memory
raw := mem.New() // fast, deterministic, not persistent
```

## The specification set

`docs/specs/` is the complete design contract: core architecture, coding guidelines, library design, performance, testing, consistency (GC/pruning), viewer HTTP surface, viewer design & security, versioning, defaults, examples, extensions. Read `docs/specs/AGENT.md` (its meta-guide) before editing any spec; the full inventory is in `AGENT.md` §10. Non-normative material lives in `docs/design/`. Agents auto-load the repo-root `AGENTS.md`, which points at the full set.

## Building & testing

```text
go build ./...
go vet ./...
go test -race ./...
gofmt -l .
```

Requires Go 1.27 (toolchain self-managing; library baseline Go 1.24+, needed for the `omitzero` JSON tags used by `cas.Digest` reference fields). See `CONTRIBUTING.md` for the workflow, and `benchmarks/README.md` for running/reading the benchmarks.

## License

MIT — see [LICENSE](LICENSE). Copyright (c) 2026 Daniel Mundt.
