# notes — a document graph with its own object types

**What it demonstrates.** An application with its **own** object model (`Note`, `Tag`, `Attachment`) built directly on the generic cas core — proving the "apps build their own repository/resolver" pattern without `gitlike` (examples spec §3.3). Shows cross-type resolution, lazy loading of large attachments, `SmartCache` prefetch, broken-reference detection, and the generic `Walker[T]`.

## `cas` core parts used

| Component | Where |
|---|---|
| `Object[T]` (versioned `note@1`/`tag@1`/`attachment@1`) | `types.go` |
| `Store[T]` + the JSON codec (`json.New[T]()`) | the three per-type stores in `Repository` |
| `Store.Get` (envelope type verification) | resolver reads |
| `CachedObject[T]` / `CachedStore[T]` | lazy attachment loading |
| prefetch-on-access (own `SmartCache` recipe) | warms references via `CachedStore` |
| `Walker[T]` | same-type related-note traversal |
| `cas.Digest` reference fields / `sha256.New()` / `sha256.Of` | references and `parseType` |

## What it extends

- **Own object types** — `Note` (references tags, attachments, and related notes), `Tag`, `Attachment`; reference fields are `[]cas.Digest`, which render as one lowercase-hex string each (and validate as they decode) with no JSON code here, and the example injects `sha256.New()` at `cas.New` (gitlike pattern, cas-core §4.2/§4.6).
- **Own `Repository` / `Resolver` / `ResolvedObject` / `parseType`** — the pattern copied from gitlike, specific to this object set. The core `Store` builds the self-describing TLV envelope around the codec payload; the app's `parseType` reads the type back from stored bytes.
- **`cas` and `gitlike` are untouched.**

## Code walkthrough

- `types.go` — the three `Object[T]` types + `parseType`. `Note.References()` = tags + attachments + related (single source of truth for traversal and prefetch).
- `repo.go` — `Repository` bundles `Store[*Note]`/`*Tag`/`*Attachment` (each built with `sha256.New()`); `Resolver.ResolveAny` reads the envelope and dispatches on the type name to the matching typed `Resolve*`.
- `main.go` — the demo: creates tags/attachments/notes, resolves a note cross-type, shows the attachment is **not** loaded until accessed (`CachedObject.IsLoaded`), prefetches a related-only note via `SmartCache`, detects a dangling reference, and walks a related chain with `Walker[T]`.
- `main_test.go` — cross-type resolution, lazy load, prefetch warms the cache, broken ref → `ErrNotFound`, walker chain.

```mermaid
flowchart TB
    N["Note (tags, attachments, related)"] -->|"references by digest"| T["Tag"]
    N --> A["Attachment (large blob)"]
    N --> R["Related note (same type)"]
    Res["Resolver.ResolveAny"] -->|"envelope type"| N
    Res --> T
    Res --> A
    C["CachedStore[Attachment]"] -->|"loads on access"| A
    S["SmartCache prefetch"] --> R
    W["Walker[Note]"] --> R
```

## How to run

```text
go run ./examples/notes
go test ./examples/notes/...
```

The demo prints the resolved note (with its tags), the lazy-load transition (`attachment loaded before/after access`), the cache size after prefetch, the detected broken reference, and the walker's visited notes.
