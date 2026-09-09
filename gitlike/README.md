# gitlike — the shared reference object-model library

**Status: reference / copy-source.** Not part of the generic cas core and not part of its stable surface (cas-core §7.1). Importable at github.com/dmundt/go-cask/gitlike for convenience, but it carries **no supported-API guarantee** — if your object model isn't a Git-style content tree, copy this pattern into your own package and extend that copy, never this library.

**What it demonstrates.** The reference library-style example: a Git-like `Blob`/`Tree`/`Commit`/`Tag` object model layered on the generic cas core (`examples.md` §2.1, `cas-core` §4.12). It is an **importable package** (not a runnable program) that other examples and apps copy as the pattern for building their own typed layers on `Store[T]`.

## `cas` core parts used

| Component | Where |
|---|---|
| `Store[T]` + the JSON codec (`json.New[T]()`) | the four per-type stores |
| `Object[T]` (versioned `blob@1`…`tag@1`) | `types.go` |
| `Hash` / `ParseHash` | all references (tree entries, commit tree/parent, tag target) |
| `cas.Backend` | the shared backend under `Repository` |
| `Store.Get` (envelope type verification) | resolver reads |
| `LRUCache[T]` | `CachedRepository` |
| `CachedStore[T].PreloadRecursive` | `Preloader` |

## What it extends

- **Four `Object[T]` types** with the self-describing envelope (`types.go`) — custom JSON methods render `Hash` values as `algo:hex` strings (a `Hash` interface cannot be unmarshaled by `encoding/json` directly).
- **`Repository`** — per-type `Store[T]` over one `cas.Backend` (cross-type access without `any`; the wrong store is a compile-time error).
- **`Resolver` / `ResolvedObject` / `parseType` / `ResolveAny`** — typed resolution; `ResolveAny` reads the envelope type via `parseType` and dispatches to the typed `Resolve*`.
- **`WalkGraph`, `CachedRepository`, `Preloader`** — whole-graph traversal, per-type LRU caches, and a background commit preloader.
- **`cas` is untouched** — the canonical *consumer* pattern.

## Code walkthrough

- `types.go` — `Blob` (leaf), `Tree`/`TreeEntry`, `Commit` (tree + optional parent), `Tag` (target); `Type()` returns the versioned names so object majors can coexist. `parseType` reads the envelope type from stored bytes (wrapping `cas.EnvelopeFromBytes`) — the parser every app with its own model copies.
- `repo.go` — `Repository` wires the four stores; `Resolver.ResolveAny` resolves any hash via `parseType` → typed `Resolve*` → `ResolvedObject` union; `PrintObject` renders via a type switch (no reflection); `WalkGraph` traverses the whole graph.
- `cached.go` — `CachedRepository` (per-type `LRUCache` + convenience getters) and `Preloader` (worker pool running `Commits.PreloadRecursive`).
- `gitlike_test.go` — round-trips, references, `ResolveAny` for every type, legacy unversioned envelopes, `WalkGraph`, cached repository, preloader.

```mermaid
classDiagram
    class Repository {
        +Blobs Store~Blob~
        +Trees Store~Tree~
        +Commits Store~Commit~
        +Tags Store~Tag~
    }
    class Resolver {
        +ResolveCommit() Commit
        +ResolveTree() Tree
        +ResolveBlob() Blob
        +ResolveTag() Tag
        +ResolveAny() ResolvedObject
    }
    class Blob { +Data []byte }
    class Tree { +Entries []TreeEntry }
    class TreeEntry { +Name string +Hash Hash +Mode string }
    class Commit { +Tree Hash +Parent Hash +Author +Message +Time }
    class Tag { +Name string +Target Hash +Tagger +Message }
    Repository --> Blob
    Repository --> Tree
    Repository --> Commit
    Repository --> Tag
    Resolver --> Repository
    Tree o-- TreeEntry
    TreeEntry --> Blob : Hash
    Commit --> Tree : Tree
    Tag --> Commit : Target
```

## How to run

```text
go test ./gitlike/...
```

It is a library; there is no standalone program. Apps import it with `github.com/dmundt/go-cask/gitlike` and layer their own types the same way.
