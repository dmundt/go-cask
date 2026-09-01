---
title: Examples — go-cask
description: Guidance for generating example programs for CASK, plus five proposed non-trivial examples that together cover every aspect of the implementation — generic core, gitlike layer, custom app object models, caching/maintenance, the CAS HTTP API and its public client SDK, and the embedded viewer (templates + htmx). Every example ships a README.md documenting the cas core parts used and extended, a code walkthrough, and a Mermaid diagram.
version: v4
---

# Examples — go-cask

> This document defines **how** example programs for CASK are written and
> **which** examples exist. It proposes five non-trivial examples that together
> exercise every aspect of the implementation:
>
> 1. `examples/files` — Git-like versioned file store (the `gitlike`
>    layer)
> 2. `examples/artifacts` — content-addressed build artifact cache
>    (custom codec + hash, caching, GC)
> 3. `examples/notes` — document graph app with its own object types (the
>    "apps build their own repository" pattern, lazy loading)
> 4. `examples/api` — CAS HTTP API server + the public client SDK
>    (streaming, auth, OpenAPI)
> 5. `examples/viewer` — embedded technical viewer (nested Go templates +
>    htmx, dashboard, security)
>
> Examples are runnable reference programs: they demonstrate the documented
> APIs in real use, they compile, and they are covered by tests where behavior
> can be asserted. They are **not** part of the `cas`/`gitlike` libraries.
>
> Related specs: `.github/instructions/cas-core.instructions.md`
> (the design the examples exercise), `.github/instructions/coding-
> guidelines.instructions.md` (how the Go code must be written),
> `.github/instructions/cas-api.instructions.md` and
> `.github/instructions/viewer-api.instructions.md` (HTTP surfaces),
> `.github/instructions/viewer-security.instructions.md` and
> `.github/instructions/viewer-design.instructions.md` (viewer rules).

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
   (`examples/<name>/client/`, ...). Exception: the **public CAS API client
   SDK is a root-level package** (`client/`) so third parties can import it —
   examples use it, they do not contain it (backend-architecture §2).
   - **`examples/gitlike/` is the reference library-style example**: an
     importable package (`package gitlike`, import path
     `github.com/dmundt/go-cask/examples/gitlike`) rather than a runnable
     `main` program — the single documented exception to the runnable rule
     below. It is the reference object model the other examples build on.
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
   - **Cas core parts used** — the exact components/APIs exercised (e.g.
     `Store[T]`, `JSONCodec[T]`, `FSRawStore` fan-out, `Verify`, `GC`,
     `LRUCache[T]`, `SmartCache[T]`), as a list or table;
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

---

## 3. Proposed Examples

### 3.1 `examples/files` — Git-like versioned file store

**Goal.** A small CLI that stores file trees as content-addressed objects and
commits them, using the `gitlike` layer end to end — the closest thing to a
miniature Git built on CASK.

**Aspects covered.** `gitlike` object model (`Blob`/`Tree`/`Commit`/`Tag`),
`Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`, `Store[T]` with
`JSONCodec[T]`, `FSRawStore` fan-out layout, `Verify`, `Stats`, CLI with std
`flag`.

**Structure.**

```text
examples/files/
├── main.go      # CLI: add, commit, log, cat, graph, verify, stats
├── repo.go      # thin helpers over gitlike.Repository (head ref, index)
├── main_test.go # round-trip: add → commit → log → cat; verify
└── README.md    # required per §2 rule 8: core used/extended, walkthrough, mermaid
```

**Key behaviors.**

- `add <path>` stores file bytes as blobs and builds a tree; identical content
  deduplicates (same hash, stored once).
- `commit -m <msg>` creates a `Commit` pointing at the tree (and the parent
  head, if any); the head is a plain `Hash` value held in a small ref file.
- `log` walks parents via `WalkGraph`/`References()`; `cat <hash>` resolves
  and prints blob bytes; `graph` prints the reachable graph with types.
- `verify` recomputes every hash; `stats` prints per-algorithm counts and
  total size.

**Acceptance criteria.** Add→commit→log→cat round-trips; identical content
across commits does not create duplicate blobs; `verify` passes after a clean
commit and reports a mismatch after a stored file is corrupted on disk.

### 3.2 `examples/artifacts` — content-addressed build artifact cache

**Goal.** A build-artifact cache that stores outputs under their content hash,
with a custom codec (gzip), a custom registered hash algorithm, bounded
caching, metrics, and mark-and-sweep GC — exercising the maintenance and
caching machinery.

**Aspects covered.** Custom `Codec[T]` (gzip-wrapped `JSONCodec[T]`),
`RegisterHash` with a std-lib-only custom algorithm (e.g. `sha256double` —
the name must obey the hash-string validation pattern of defaults §2, so
the earlier illustrative `sha256-double` is not used),
`PutDedup` (dedup reporting), `CachedStore[T]`/`LRUCache[T]`,
`CacheMonitor[T]` metrics, `GC` (reachable = manifest-referenced artifacts),
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
- `get <hash>` serves from cache (`LRUCache`) with a `CacheMonitor` printing
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
`SmartCache[T]` prefetch, broken-reference detection.

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
- `SmartCache.GetWithPrefetch` warms references; metrics show cache hits after
  prefetch; a deliberately dangling reference is flagged as broken.

**Acceptance criteria.** Notes resolve across all three types; attachments are
not loaded until accessed; after prefetch the cache reports hits; broken
references are detected and reported without crashing.

### 3.4 `examples/api` — CAS HTTP API server using the public client SDK

**Goal.** A standalone server exposing the CAS HTTP API (`/api/cas/v1`) per
`cas-api.instructions.md`, plus the **public `client/` SDK** that other
programs (and the other examples) can import — demonstrating the API
contract, streaming, and auth.

**Aspects covered.** CAS API surface (`POST/GET /objects`, `meta`, `list`,
`stats`, `verify`, `gc`), bearer-token auth with the role matrix,
streaming upload/download (large objects never fully buffered), dedup,
OpenAPI self-doc at `/api/cas/v1/openapi.yaml`, client SDK usage.

**Structure.** The SDK is a **root-level public package** (`client/`,
`package client`, import path `github.com/dmundt/go-cask/client`), NOT part
of the example — it is the public surface promised by
backend-architecture §2. The example demonstrates it:

```text
client/                # public CAS API client SDK (Put/Get/Meta/List/Stats/Verify)
examples/api/
├── server/
│   ├── main.go        # net/http server, pattern routing, bearer middleware
│   ├── openapi.yaml   # the CAS API OpenAPI document (separate file, //go:embed per api-design §13)
│   └── server_test.go # httptest: round-trip, roles, streaming
├── demo/
│   └── main.go        # demo CLI: round-trips a file through the server via client/
└── README.md
```

**Key behaviors.**

- `server` stores/retrieves bytes by hash with `?algo`, enforces roles
  (viewer: reads; operator: store/verify; admin: delete/gc), returns JSON
  errors, and serves its OpenAPI document.
- `client` mirrors the API with typed functions and streams
  `io.Reader`/`io.ReadCloser`; the demo CLI round-trips a file through the
  server.

**Acceptance criteria.** `client.Put` → `client.Get` returns identical bytes;
a viewer-role token gets 403 on `DELETE`; large payloads stream without
buffering; `GET /api/cas/v1/openapi.yaml` is served and matches the routes.

### 3.5 `examples/viewer` — embedded viewer (nested templates + htmx)

**Goal.** A runnable application embedding the technical viewer per
`viewer-design.instructions.md`: a dashboard (stat cards, algorithm table,
sample objects, search), object detail (metadata, references, hexdump), and
stats — built with nested Go templates + htmx, no CSS/JS, with the security
model from `viewer-security.instructions.md`.

**Aspects covered.** Nested `html/template` composition (`base`/`dashboard`/
`object`/partials), htmx patterns (active search, lazy-loaded hexdump,
out-of-band stats refresh), raw HTML, session auth (startup token + cookie),
role enforcement, CSRF on mutations, backend consuming the CAS API layer.

**Structure.**

```text
examples/viewer/
├── main.go           # routes + handlers (dashboard, objects, detail, stats)
├── auth.go           # startup token, session middleware, roles, CSRF
├── templates/        # embedded with embed.FS (nested defines)
│   ├── base.html
│   ├── dashboard.html
│   ├── object.html
│   └── partials.html
├── main_test.go      # httptest: login flow, role checks, search swap
└── README.md         # required per §2 rule 8: core used/extended, walkthrough, mermaid
```

**Key behaviors.**

- `GET /viewer/` renders the dashboard from real `Stats` + a sample of
  objects; every hash is a link to its detail page.
- Active search swaps the object table via htmx; the hexdump fragment is
  lazy-loaded; the stats panel refreshes out-of-band.
- Mutations (`verify`, `delete`) are POST + CSRF + role-checked +
  audit-logged; 401/403 responses are empty bodies; GET endpoints are
  side-effect free.

**Acceptance criteria.** Login with the startup token reaches the dashboard;
search returns fragments without full reloads; detail pages show references
and a lazy hexdump; a viewer-role session cannot delete; no CSS or hand-written
JS anywhere in the example.

---

## 4. Aspect Coverage Matrix

| Aspect                                            | files | artifacts | notes | api | viewer |
| ------------------------------------------------- | :-------------: | :------------: | :---: | :-----: | :--------: |
| `Hash` / pluggable algorithms                     | ✓ (sha256)      | ✓ (custom)     | ✓     | ✓ (algo) | ✓          |
| `FSRawStore` fan-out layouts                      | ✓               | ✓              | ✓     | ✓       | ✓          |
| `Codec[T]` (custom)                               | ✓ (JSON)        | ✓ (gzip)       | ✓     | ✓ (JSON) | ✓ (JSON)   |
| `Object[T]` / `Store[T]`                          | ✓               | ✓              | ✓     | ✓       | ✓          |
| Dedup (`PutDedup`)                                | ✓               | ✓              |       | ✓       |            |
| `gitlike` layer (`Repository`/`Resolver`/`WalkGraph`) | ✓            |                |       |         |            |
| Custom app object model (own repo/resolver)       |                 |                | ✓     |         |            |
| Generic `Walker[T]`                               | ✓               |                | ✓     |         |            |
| Lazy loading (`CachedObject[T]`)                  |                 |                | ✓     |         | ✓ (hexdump)|
| Caching (`CachedStore[T]`/`LRUCache[T]`)          |                 | ✓              | ✓     |         |            |
| `SmartCache[T]` prefetch                          |                 |                | ✓     |         |            |
| Metrics (`CacheMonitor[T]`)                       |                 | ✓              |       |         |            |
| Background `Preloader`                            |                 |                | ✓     |         |            |
| `Stats` / `Verify` / `GC`                         | ✓ (verify/stats)| ✓ (gc/stats)   |       | ✓       | ✓ (via API)|
| CAS HTTP API + client SDK                         |                 |                |       | ✓       | ✓ (consumes)|
| Viewer: nested templates + htmx + dashboard       |                 |                |       |         | ✓          |
| Security (authn/authz, sessions, CSRF)            |                 |                |       | ✓ (bearer) | ✓ (session)|
| Streaming (`io.Reader`/`io.ReadCloser`)           | ✓ (files)       | ✓              |       | ✓       | ✓          |

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

- [ ] All five proposed examples exist under `examples/` and build
      (`go build ./...`)
- [ ] Each example runs standalone (`go run ./examples/<name>`) with
      meaningful output
- [ ] No external dependencies; no CSS/JS in the viewer example; no `any` in
      example APIs
- [ ] Examples use only documented public APIs of `cas`/`gitlike`; the
      libraries themselves are untouched
- [ ] Each example ships a `README.md` with the §2 rule 8 content (core used,
      extended, code walkthrough, Mermaid diagram, how to run) plus a package
      comment, and is covered by tests where behavior can be asserted
- [ ] The aspect matrix (§4) stays complete — every aspect of the
      implementation is demonstrated by at least one example
- [ ] The viewer example complies with `viewer-security.instructions.md` and
      `viewer-design.instructions.md`
