# gitlike — the shared reference object-model library

**Status: reference / copy-source.** Not part of the generic cas core and not part of its stable surface (cas-core §7.1). Importable at github.com/dmundt/go-cask/gitlike for convenience, but it carries **no supported-API guarantee** — if your object model isn't a Git-style content tree, copy this pattern into your own package and extend that copy, never this library.

**What it demonstrates.** The reference library-style example: a Git-like `Blob`/`Tree`/`Commit`/`Tag` object model layered on the generic cas core (`examples.md` §2.1, `cas-core` §4.12). It is an **importable package** (not a runnable program) that other examples and apps copy as the pattern for building their own typed layers on `Store[T]`.

## `cas` core parts used

| Component | Where |
|---|---|
| `Store[T]` + the JSON codec (`json.New[T]()`) | the four per-type stores |
| `Object[T]` (versioned `blob@1`…`tag@1`) | `types.go` |
| `cas.Digest` reference fields (rendered by the type's own `MarshalText`) | all references (tree entries, commit tree/parent, tag target) |
| `cas.Hasher` (injected, not chosen here; the tests wire `sha256.New()`) | `NewRepository(raw, hasher)` → each per-type `cas.New` |
| `cas.Backend` | the shared backend under `Repository` |
| `Store.Get` (envelope type verification) | resolver reads |
| `LRUCache[T]` | `CachedRepository` |
| `CachedStore[T].PreloadRecursive` | `Preloader` |

## What it extends

- **Four `Object[T]` types** with the self-describing envelope (`types.go`). Reference fields are plain `cas.Digest`: the zero value is "absent", the tag `omitzero` leaves an absent reference out of the encoding (`TreeEntry.Hash`, `Commit.Parent`), and `Digest` renders itself as one lowercase-hex string through `encoding.TextMarshaler` and validates as it decodes — so no hash JSON code lives in `gitlike` (`cas/codec/json` is imported only for `json.New[T]()`), and `Tree`, `TreeEntry` and `Tag` contain **no** JSON code at all. `Commit` keeps two small methods for its one mandatory-field rule: write refuses a tree-less commit, decode rejects a missing, empty, or null tree. Each reference is one bare hex string on the wire (the previous build wrote `"sha256:hexdigest"`) — see the migration note below.
- **`Validate() error`** on `TreeEntry`/`Tree`/`Commit`/`Tag` — advisory checks for hand-built objects (`TreeEntry` needs a name, `Commit` needs a tree, `Tag` needs a name; an absent digest is valid where absence is legal). `Store.Put` marshals, it does not validate, so call a `Validate` yourself before `Put` when you build objects in code; a tree-less commit is the one case still rejected at `Put` (via `Commit.MarshalJSON`).
- **`Repository`** — per-type `Store[T]` over one `cas.Backend` and the caller's `cas.Hasher` (`NewRepository(raw, hasher)`); cross-type access without `any`, and the wrong store is a compile-time error.
- **`Resolver` / `ResolvedObject` / `parseType` / `ResolveAny`** — typed resolution; `ResolveAny` reads the envelope type via `parseType` and dispatches to the typed `Resolve*`.
- **`WalkGraph`, `CachedRepository`, `Preloader`** — whole-graph traversal, per-type LRU caches, and a background commit preloader.
- **`cas` is untouched** — the canonical *consumer* pattern.

## Code walkthrough

- `types.go` — `Blob` (leaf), `Tree`/`TreeEntry`, `Commit` (tree + optional parent), `Tag` (target); `Type()` returns the versioned names so object majors can coexist; `Validate()` carries the per-type rules. `parseType` reads the envelope type from stored bytes (wrapping `cas.EnvelopeFromBytes`) — the parser every app with its own model copies.
- `repo.go` — `Repository` wires the four stores over one backend and the caller's hasher; `Resolver.ResolveAny` resolves any digest via `parseType` → typed `Resolve*` → `ResolvedObject` union; `PrintObject` renders via a type switch (no reflection); `WalkGraph` traverses the whole graph.
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
    class TreeEntry { +Name string +Hash cas.Digest +Mode string }
    class Commit { +Tree cas.Digest +Parent cas.Digest +Author +Message +Time }
    class Tag { +Name string +Target cas.Digest +Tagger +Message }
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

## Migration: previously stored objects do not decode

The object type names stay `@1` (`blob@1` … `tag@1`) even though a reference payload changed from `"sha256:hexdigest"` to bare hex. `cas.Digest.UnmarshalText` is strict — it rejects the legacy `sha256:` prefix rather than reinterpreting it — so an object stored by the previous build fails to decode with `cas.ErrCorrupt` (surfaced by `Store.Get`) instead of resolving to a different address. This is a deliberate loud break: there is **no** migration tool and **no** `@2` type. Keep the previous build available to decode those objects, re-create the values with the current build, and treat the old store as read-only until then (`operations.md` §5).

## How to run

```text
go test ./gitlike/...
```

It is a library; there is no standalone program. Apps import it with `github.com/dmundt/go-cask/gitlike` and layer their own types the same way.
