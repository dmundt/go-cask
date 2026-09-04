---
title: Examples — go-cask
description: Guidance for generating example programs for CASK, plus four runnable examples (files, artifacts, notes, api) and the gitlike shared reference library — the viewer aspect is covered by the product viewer (internal/web). Every example ships a README.md documenting the `cas` core parts used and extended, a code walkthrough, and a Mermaid diagram.
version: v10
---

# Examples — go-cask

> This document defines **how** example programs for CASK are written and
> **which** examples exist. It proposes five non-trivial examples that together
> exercise every aspect of the implementation:
>
> 1. `examples/files` — Git-like versioned file store (the `gitlike`
>    layer)
> 2. `examples/artifacts` — content-addressable build artifact cache
>    (custom codec + hash, caching, GC)
> 3. `examples/notes` — document graph app with its own object types (the
>    "apps build their own repository" pattern, lazy loading)
> 4. `examples/api` — HTTP-exposure pattern: a self-contained store server
>    over the public `cas` surface (versioned prefix, bearer auth, rate
>    limit, streaming, OpenAPI)
> 5. `examples/viewer` — covered by the **product viewer** in `internal/web/`
>    (nested Go templates + htmx, dashboard, security); see §3.5 for details.
>
> Examples are runnable reference programs: they demonstrate the documented
> APIs in real use, they compile, and they are covered by tests where behavior
> can be asserted. They are **not** part of the `cas`/`gitlike` libraries.
>
> Related specs: `docs/instructions/cas-core.md`
> (the design the examples exercise), `docs/instructions/coding-guidelines.md`
> (how the Go code must be written), `docs/instructions/api-design.md`
> (HTTP conventions for example surfaces),
> `docs/instructions/viewer-security.md`
> and `docs/instructions/viewer-design.md` (viewer rules).

---

## 1. Purpose

Examples serve three audiences:

1. **Developers reading the docs** — a runnable program is worth a thousand
   API signatures; each example maps to the spec sections it demonstrates.
2. **App authors** — examples are starting points: copy the pattern for your
   own object types, repository/resolver, or server.
3. **The test suite** — examples' assertions (round-trips, dedup, GC
   behavior) keep the public API honest.

---

## 2. How to Generate an Example (Rules)

When creating or extending an example, follow these rules:

1. **Location.** Each example lives in `examples/<name>/` inside the main
   module (no separate `go.mod` unless genuinely required). A runnable demo is
   `package main`; reusable pieces are subpackages
   (`examples/<name>/client/`, ...).
   - **`examples/gitlike/` is the shared reference support library** — the
     single designated cross-example dependency (rule 11): an importable
     package (`package gitlike`, import path
     `github.com/dmundt/go-cask/examples/gitlike`) rather than a runnable
     `main` program — the documented exception to the runnable rule below.
     It is the reference object model app authors (and the `files` example)
     build on; it stays an example, never part of `cas`.
2. **Runnable.** `go build ./...`, `go run ./examples/<name>`, and
   `go test ./examples/...` MUST all pass (except `examples/gitlike`, which
   is a library, not a program). The demo prints meaningful output
   (hashes, stats, traversal results) — it must be obvious what it did.
3. **Standard library only.** No external dependencies
   (coding-guidelines §3). Custom hash algorithms use `RegisterHash` with
   std-lib primitives (e.g. `sha256` of `sha256`); compression codecs use
   `compress/gzip`.
4. **Public APIs only.** Examples use the documented exported API of
   `cas` and `gitlike`; they MUST NOT reach into unexported internals.
5. **No `any` in example APIs.** Examples follow the same no-`any` rule as the
   core; they define their own typed objects, repositories, and resolvers
   (copy the `gitlike` pattern — never extend `cas` or `gitlike` itself).
6. **One focus per example, real-world shape.** Each example has a clear
   primary aspect (see §4) but stays a small, believable program — not a
   kitchen sink and not a toy.
7. **Idiomatic Go.** `gofmt`, doc comments on exported identifiers,
   `context.Context` first, wrapped errors, table-driven tests where
   assertions exist (coding-guidelines §2, §7).
8. **Documented — a `README.md` in the example folder is REQUIRED.** Every
   example MUST ship a `README.md` next to its code (in addition to the
   package comment) that teaches the example. It MUST contain:
   - **What it demonstrates** — the primary aspect (§4) and acceptance
     criteria, in one short paragraph;
   - **`cas` core parts used** — the exact components/APIs exercised (e.g.
     `Store[T]`, `JSONCodec[T]`, `FSRawStore` fan-out, `Verify`, `GC`,
     `LRUCache[T]`, `CachedObject[T]`), as a list or table;
   - **What it extends** — everything the example adds on top of the core
     (custom `Codec[T]`, `RegisterHash` algorithms, own `Object[T]` types,
     own repository/resolver pattern, HTTP surface), and explicitly what it
     does NOT modify (the `cas`/`gitlike` libraries stay untouched);
   - **Code walkthrough** — how the code is structured (files and their
     roles) and the key flow, so a reader can follow it file by file;
   - **A Mermaid diagram** — one `mermaid` flowchart (or class diagram)
     visualizing the example's flow/architecture; the diagram MUST be
     balanced (AGENT.md §9);
   - **How to run** — the exact `go run`/`go test` commands and expected
     output shape.
   The README lives at `examples/<name>/README.md`; keep it focused and
   concrete — it is documentation for app authors copying the pattern
   (examples §1).
9. **Coverage.** The set of examples as a whole MUST keep covering the
   aspect matrix (§4). A new example that duplicates an existing aspect
   without adding a new one is discouraged unless it is a better teaching
   vehicle.
10. **Never modify the libraries for an example's sake.** If an example needs
    a feature that does not exist, that is a spec/library change — raise it
    separately; do not hack around it inside the example.
11. **Self-contained.** Every example is self-contained: an example MUST NOT
    import another example's package. The one exception is
    `examples/gitlike` — the shared reference support library (rule 1) —
    which examples MAY import (the `files` example does). Examples never
    depend on `files`, `artifacts`, `notes`, or `api`, and those never
    depend on each other.

    > Decision (2026-09): cache/recipe helpers stay **inlined** —
    > `SmartCache` (notes), `CacheMonitor` (artifacts), and similar
    > per-example helpers are teaching code carried by their own example,
    > not shared packages (they were extracted from `cas/extra` on exactly
    > that principle). Create a shared home for one of them only when a
    > **second consumer of that same helper** exists (the gitlike shared
    > library exception covers the reference object model, not recipes);
    > decide that home deliberately (example support vs. add-on) when the
    > need appears.

---
## 3. Proposed Examples

### 3.1 `examples/files` — Git-like versioned file store

**Goal.** A small CLI that stores file trees as content-addressable objects and
commits them, using the `gitlike` layer end to end — the closest thing to a
miniature Git built on CASK. It also demonstrates the derived object-state
report: every object is classified verified / orphaned / corrupt / unverified
from the existing operations (`Verify` + reachability from `HEAD`), proving
those states are scan results, never stored metadata.

**Aspects covered.** `gitlike` object model (`Blob`/`Tree`/`Commit`/`Tag`),
`Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`, `Store[T]` with
`JSONCodec[T]`, `FSRawStore` fan-out layout, `Verify`, `Stats`, derived
object-state audit (`List` + reachability mark + per-object `Verify`), CLI
with std `flag`.

**Structure.**

```text
examples/files/
├── main.go      # CLI: add, commit, log, cat, graph, audit, verify, stats
├── repo.go      # thin helpers over gitlike.Repository (head ref, index)
├── audit.go     # derived-state report: List → mark reachable from HEAD → Verify each
├── main_test.go # round-trip: add → commit → log → cat; verify; audit states
└── README.md    # required per §2 rule 8: core used/extended, walkthrough, mermaid
```

**Key behaviors.**

- `add <path>` stores file bytes as blobs and builds a tree; identical content
  deduplicates (same hash, stored once).
- `commit -m <msg>` creates a `Commit` pointing at the tree (and the parent
  head, if any); the head is a plain `Hash` value held in a small ref file.
- `log` walks parents via `WalkGraph`/`References()`; `cat <hash>` resolves
  and prints blob bytes; `graph` prints the reachable graph with types.
- `audit [-no-verify]` lists every stored object, marks the reachable set
  from `HEAD` (`References()` walk), verifies each with `FSRawStore.Verify`,
  and prints per-object state: `verified` (intact + reachable), `orphaned`
  (intact, unreachable — GC candidate), `corrupt` (Verify failed), or
  `unverified` (reachable, integrity skipped under `-no-verify`). States are
  derived at scan time, never persisted (consistency §8).
- `verify` recomputes every hash; `stats` prints per-algorithm counts and
  total size.

**Acceptance criteria.** Add→commit→log→cat round-trips; identical content
across commits does not create duplicate blobs; `verify` passes after a clean
commit and reports a mismatch after a stored file is corrupted on disk;
`audit` reports a clean store as all `verified`, an uncommitted `add`'s
objects as `orphaned`, a corrupted object as `corrupt`, and under
`-no-verify` reachable objects as `unverified`.

### 3.2 `examples/artifacts` — content-addressable build artifact cache

**Goal.** A build-artifact cache that stores outputs under their content hash,
with a custom codec (gzip), a custom registered hash algorithm, bounded
caching, metrics, and mark-and-sweep GC — exercising the maintenance and
caching machinery.

**Aspects covered.** Custom `Codec[T]` (gzip-wrapped `JSONCodec[T]`),
`RegisterHash` with a std-lib-only custom algorithm (e.g. `sha256double` —
the name must obey the hash-string validation pattern of defaults §2, so
the earlier illustrative `sha256-double` is not used),
`PutDedup` (dedup reporting), `CachedStore[T]`/`LRUCache[T]`,
cache metrics (its own `CacheMonitor` recipe), `GC` (reachable = manifest-referenced artifacts),
`Stats`.

**Structure.**

```text
examples/artifacts/
├── main.go      # CLI: put, get, gc, stats, monitor
├── manifest.go  # Manifest object referencing artifact hashes
├── codec.go     # gzipCodec[T] wrapping JSONCodec[T]
├── hasher.go    # RegisterHash("sha256double", ...)
├── main_test.go # dedup, cache hit-rate, GC deletes only unreferenced
└── README.md    # required per §2 rule 8: core used/extended, walkthrough, mermaid
```

**Key behaviors.**

- `put <file>` computes the custom hash, stores via `PutDedup`, and prints
  `deduplicated: true/false`; manifests reference artifact hashes.
- `get <hash>` serves from cache (`LRUCache`) with the example's own `CacheMonitor` printing
  hit rate on exit.
- `gc` mark-and-sweeps: objects not reachable from any manifest are deleted;
  `stats` before/after shows the difference.

**Acceptance criteria.** Same bytes → same hash → `deduplicated: true`;
second `get` hits the cache (hit rate > 0); `gc` deletes only unreferenced
artifacts and leaves manifest-referenced ones intact.

### 3.3 `examples/notes` — document graph with its own object types

**Goal.** An application with **its own** object model (`Note`, `Tag`,
`Attachment`) that does NOT use `gitlike` — proving the "apps build their own
repository/resolver" pattern from the generic core, with lazy loading and
prefetching.

**Aspects covered.** Custom `Object[T]` types on the generic core, own
`Repository`/`Resolver`/`ResolvedObject` (copied from the `gitlike` pattern),
generic `Walker[T]` traversal, lazy loading via `CachedObject[T]`,
prefetch-on-access (own `SmartCache` recipe), broken-reference detection.

**Structure.**

```text
examples/notes/
├── types.go      # Note, Tag, Attachment (Object[T] implementations)
├── repo.go       # own Repository, Resolver, ResolvedObject, parseType
├── main.go       # demo: create notes/tags/attachments, resolve, walk, prefetch
├── main_test.go  # cross-type resolution, lazy load, prefetch warms cache
└── README.md     # required per §2 rule 8: core used/extended, walkthrough, mermaid
```

**Key behaviors.**

- Notes reference tags and attachments by hash; attachments are large blobs
  loaded lazily (`CachedObject.Load` only on access).
- Own `Resolver` resolves any hash to the right concrete type via
  `ResolvedObject` (no `any`), mirroring the `gitlike` example.
- the example's `SmartCache.GetWithPrefetch` warms references; metrics show cache hits after
  prefetch; a deliberately dangling reference is flagged as broken.

**Acceptance criteria.** Notes resolve across all three types; attachments are
not loaded until accessed; after prefetch the cache reports hits; broken
references are detected and reported without crashing.

### 3.4 `examples/api` — HTTP-exposure pattern (server over `cas`)

**Goal.** A self-contained HTTP server that exposes a `cas` store to other
processes — the **pattern an app author copies** when their app needs a
network surface. go-cask the product ships no network JSON API
(backend-architecture §1); this example shows how to build one from the
public `cas` library alone, with std-lib `net/http`.

**Aspects covered.** Versioned prefix (`/api/cas/v1`), bearer-token auth with
the role matrix, streaming upload/download (large objects never fully
buffered), dedup (raw-layer exists-then-put), per-IP rate limiting,
`ParseHash` validation,
JSON errors, OpenAPI self-doc at `/api/cas/v1/openapi.yaml` (api-design
§13), and a plain-HTTP demo client (no SDK — the product has none).

**Structure.**

```text
examples/api/
├── server/
│   ├── main.go        # net/http server, pattern routing, bearer middleware
│   ├── ratelimit.go   # per-IP token bucket (api-design §8 pattern)
│   ├── hash.go        # hash-on-write spooling, envelope-type sniffing
│   ├── openapi.yaml   # the surface's OpenAPI document (separate file,
│   │                  # //go:embed per api-design §13)
│   └── server_test.go # httptest: round-trip, roles, streaming, 429
├── demo/
│   └── main.go        # demo CLI: round-trips a file with plain net/http
└── README.md          # required per rule 8: core used/extended, walkthrough,
                       # mermaid
```

**Key behaviors.**

- `server` stores/retrieves bytes by hash with `?algo`, enforces roles
  (viewer: reads; operator: store/verify; admin: delete/gc), rate-limits per
  IP, returns JSON errors, and serves its OpenAPI document.
- `demo` PUTs a file, GETs it back, and prints meta/stats — using plain
  `net/http` and `cas.ParseHash`, exactly as an app without an SDK would.

**Acceptance criteria.** The demo round-trips a file through the server
(identical bytes); a viewer-role token gets 403 on `DELETE`; large payloads
stream without buffering; `GET /api/cas/v1/openapi.yaml` is served and
matches the routes. The server imports nothing but `cas` and stdlib — proof
the pattern needs no `internal/` and no SDK.

### 3.5 `examples/viewer` — embedded viewer (nested templates + htmx)

The viewer's `nested templates + htmx` and `dashboard` aspects are **not
demonstrated by a separate example** — they are implemented by the **product
viewer** in `internal/web/` (the `cask web` subcommand). The viewer is a
byte-layer tool per `viewer-design.md` and `viewer-security.md`, built with
nested Go templates + htmx, no CSS/JS, session auth, role enforcement, CSRF,
and audit logging. The aspect-coverage matrix (§4) marks these cells as
`product (internal/web)` rather than `✓` to indicate they are covered by
the shipping product rather than a standalone example. The rules in §2
(self-contained, no `internal/` imports) make a separate example infeasible:
the viewer would need to duplicate the product implementation, violating
the "examples teach, never ship" principle.

---

## 4. Aspect Coverage Matrix

| Aspect                                            | files | artifacts | notes | api | viewer |
| ------------------------------------------------- | :-------------: | :------------: | :---: | :-----: | :--------: |
| `Hash` / pluggable algorithms                     | ✓ (sha256)      | ✓ (custom)     | ✓     | ✓ (algo) | product    |
| `FSRawStore` fan-out layouts                      | ✓               | ✓              | ✓     | ✓       | product    |
| `Codec[T]` (custom)                               | ✓ (JSON)        | ✓ (gzip)       | ✓     | ✓ (JSON) | product    |
| `Object[T]` / `Store[T]`                          | ✓               | ✓              | ✓     | ✓       | product    |
| Dedup (`PutDedup`)                                | ✓               | ✓              |       | ✓       |            |
| `gitlike` layer (`Repository`/`Resolver`/`WalkGraph`) | ✓            |                |       |         |            |
| Custom app object model (own repo/resolver)       |                 |                | ✓     |         |            |
| Generic `Walker[T]`                               | ✓               |                | ✓     |         |            |
| Lazy loading (`CachedObject[T]`)                  |                 |                | ✓     |         | product    |
| Caching (`CachedStore[T]`/`LRUCache[T]`)          |                 | ✓              | ✓     |         |            |
| Prefetch-on-access (own `SmartCache`)              |                 |                | ✓     |         |            |
| Cache metrics (own `CacheMonitor`)                 |                 | ✓              |       |         |            |
| Background `Preloader`                            |                 |                | ✓     |         |            |
| `Stats` / `Verify` / `GC`                         | ✓ (verify/audit)| ✓ (gc/stats)   |       | ✓       | product    |
| HTTP-exposure pattern (server over `cas`)          |                 |                |       | ✓       |            |
| Viewer: nested templates + htmx + dashboard       |                 |                |       |         | product    |
| Security (authn/authz, sessions, CSRF)            |                 |                |       | ✓ (bearer) | product   |
| Streaming (`io.Reader`/`io.ReadCloser`)           | ✓ (files)       | ✓              |       | ✓       | product    |

---

## 5. Generating New Examples on Request

When asked to "create an example" or "show how to X":

1. Identify the aspects of X in the matrix (§4) and pick the closest existing
   example as the base; mirror its structure and conventions.
2. Follow §2 rules: runnable, std-lib only, public APIs only, no `any`,
   documented, tested where assertable.
3. Add the example to §3 (or replace an obsolete one) and mark its aspects in
   §4, keeping the matrix complete.
4. Verify with `gofmt -l .`, `go vet ./...`, `go test ./...`, and
   `go run ./examples/<name>`.

---

## 6. Acceptance Checklist

- [x] All five proposed examples exist under `examples/` and build
      (`go build ./...`)
- [x] Each example runs standalone (`go run ./examples/<name>`) with
      meaningful output
- [x] No external dependencies; no CSS/JS in the viewer example; no `any` in
      example APIs
- [x] No example imports another example except the shared reference
      support library `gitlike` (rule 11)
- [x] Examples use only documented public APIs of `cas`/`gitlike`; the
      libraries themselves are untouched
- [x] Each example ships a `README.md` with the §2 rule 8 content (core used,
      extended, code walkthrough, Mermaid diagram, how to run) plus a package
      comment, and is covered by tests where behavior can be asserted
- [x] The aspect matrix (§4) stays complete — every aspect of the
      implementation is demonstrated by at least one example
- [x] The viewer example complies with `viewer-security.md` and
      `viewer-design.md`
