---
type: Specification
title: Examples — go-cask
description: Guidance for generating example programs for CASK, plus four runnable examples (files, artifacts, notes, api) and the gitlike shared reference library — the viewer aspect is covered by the product viewer (internal/web). Every example ships a README.md documenting the `cas` core parts used and extended, a code walkthrough, and a Mermaid diagram.
version: v17
---

# Examples — go-cask

How example programs are written and which exist: `files` (Git-like file store), `artifacts` (artifact cache), `notes` (own object model), `api` (HTTP-exposure pattern); `viewer` is covered by the product viewer in `internal/web/` (§3.5). Examples are runnable reference programs that compile, demonstrate the documented APIs in real use, and are tested where behavior is assertable. They are NOT part of `cas`/`gitlike`. Related: cas-core, coding-guidelines, api-design, viewer-security, viewer-design.

## 1. Purpose

Serve three audiences: **doc readers** (a runnable program beats API signatures; each maps to the spec sections it demonstrates), **app authors** (copy the pattern for your own object types/repository/server), and **the test suite** (assertions keep the public API honest).

## 2. How to generate an example (rules)

1. **Location:** `examples/<name>/` inside the main module (no separate `go.mod` unless genuinely required). Runnable demo = `package main`; reusable pieces = subpackages. **`gitlike/` is the shared reference support library** — the one designated cross-example dependency (rule 11): an importable package (`package gitlike`), not a runnable `main`; the documented exception to the runnable rule. It is the reference object model apps (and `files`) build on; it is NOT part of `cas`.
2. **Runnable:** `go build ./...`, `go run ./examples/<name>`, and `go test ./examples/...` MUST pass (except `gitlike`, a library). The demo prints meaningful output (hashes, stats, traversal results).
3. **Std-lib only:** no external deps (coding-guidelines §3). Compression via `compress/gzip`; hashing is the client's job (`cas/hash/sha256`: `sha256.New()` for a store, `sha256.NewHasher()`/`sha256.Of` for hash-on-write) — the core names no algorithm and there is no registry to extend.
4. **Public APIs only:** documented exported API of `cas`/`gitlike`; never reach into unexported internals.
5. **No `any` in example APIs:** define own typed objects/repositories/resolvers (copy the `gitlike` pattern; never extend `cas`/`gitlike`).
6. **One focus per example, real-world shape:** clear primary aspect (§4), small believable program — not a kitchen sink, not a toy.
7. **Idiomatic Go:** `gofmt`, doc comments on exports, `context.Context` first, wrapped errors, table-driven tests (coding-guidelines §2, §7).
8. **`README.md` is REQUIRED** in the example folder (in addition to the package comment) teaching the example. It MUST contain: **What it demonstrates** (primary aspect + acceptance, one short paragraph); **`cas` core parts used** (exact components/APIs, e.g. `Store[T]`, `json.New[T]()`, `cas.Digest` reference fields, the `sha256.New()`/`sha256.Of` hasher, `fs.WithFanOut`/`WithFanLevels`, `Verify`, `GC`, `memory.CachedStore[T]`/`lru.Cache`, `CachedObject[T]`); **What it extends** (a custom `Codec[T]`, an own `Object[T]`/repo/resolver, an HTTP surface — never a custom hash algorithm, since the client merely injects `cas.Hasher`) and explicitly what it does NOT modify (`cas`/`gitlike` untouched); **Code walkthrough** (files and roles, key flow); **A Mermaid diagram** (balanced, AGENT.md §9); **How to run** (exact commands + expected output shape). Focused and concrete — docs for app authors.
9. **Coverage:** the example set MUST keep covering the aspect matrix (§4); a duplicate-aspect example is discouraged unless a better teaching vehicle.
10. **Never modify the libraries for an example's sake:** a missing feature is a spec/library change — raise it separately; do not hack around it in the example.
11. **Self-contained:** an example MUST NOT import another example's package, except `gitlike` (which `files` imports). Examples never depend on `files`/`artifacts`/`notes`/`api`, and those never on each other.
    - Decision (2026-09): cache/recipe helpers (`SmartCache` in notes, `CacheMonitor` in artifacts) stay **inlined** teaching code in their own example, not shared packages. Create a shared home for one only when a **second consumer of that same helper** exists; decide that home deliberately when the need appears.

## 3. Proposed examples

### 3.1 `examples/files` — Git-like versioned file store

**Goal:** a small CLI storing file trees as content-addressable objects and committing them over the `gitlike` layer end-to-end (a miniature Git). Also demonstrates the derived object-state report: every object classified verified/orphaned/corrupt/unverified from existing ops (`Verify` + reachability from `HEAD`) — proving those states are scan results, never stored metadata.

**Aspects:** `gitlike` model (`Blob`/`Tree`/`Commit`/`Tag`), `Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`, `Store[T]`+JSON codec, `fs` fan-out, `Verify`, `Stats`, derived-state audit (`List` + reachability mark + per-object `Verify`), CLI (manual `-store` parsing).
**Structure:** `main.go` (CLI: add, commit, log, cat, graph, audit, verify, stats), `audit.go` (derived-state report), `main_test.go`, `README.md`.
**Behaviors:** `add` stores blobs + builds a tree (identical content dedups); `commit -m` creates a `Commit` pointing at the tree + parent head (head = a `cas.Digest` in a small ref file); `log` walks parents via `WalkGraph`/`References()`; `cat` resolves+prints blob bytes; `graph` prints reachable graph with types; `audit [-no-verify]` lists all objects, marks reachable from `HEAD`, `Verify`s each, prints per-object state — `verified` (intact+reachable), `orphaned` (intact, unreachable — GC candidate), `corrupt` (Verify failed), `unverified` (reachable, skipped under `-no-verify`); states derived at scan time, never persisted (consistency §8); `verify` recomputes every digest; `stats` prints `N objects, M bytes`.
**Acceptance:** add→commit→log→cat round-trips; identical content across commits doesn't duplicate blobs; `verify` passes after a clean commit and reports a mismatch after on-disk corruption; `audit` reports clean=all `verified`, an uncommitted add's objects=`orphaned`, corrupted=`corrupt`, and under `-no-verify` reachable=`unverified`.

### 3.2 `examples/artifacts` — content-addressable build artifact cache

**Goal:** cache build outputs under their content digest with a custom codec (gzip), bounded caching, metrics, mark-and-sweep GC.
**Aspects:** custom `Codec[T]` (gzip-wrapped JSON), `PutDedup`, caching (`lru.Cache`), cache metrics (`CacheMonitor`), `GC` (reachable = manifest-referenced), `Stats`.
**Structure:** `main.go` (the `Artifact`/`Manifest` types + put/get/gc/stats/monitor CLI), `codec.go` (gzipCodec[T]), `main_test.go`, `README.md`.
**Behaviors:** `put` stores the artifact under the client's `sha256` digest, prints it with `deduplicated: true/false`; manifests reference artifact digests (`[]cas.Digest`). `get` serves from `lru.Cache`, `CacheMonitor` prints hit rate on exit. `gc` mark-and-sweeps (unreferenced-from-any-manifest objects deleted); `stats` before/after shows it.
**Acceptance:** same bytes → same digest → `deduplicated: true`; second `get` hits cache (hit rate > 0); `gc` deletes only unreferenced artifacts, leaves manifest-referenced intact.

### 3.3 `examples/notes` — document graph with its own object types

**Goal:** an app with **its own** object model (`Note`, `Tag`, `Attachment`) not using `gitlike` — proving the "apps build their own repository/resolver" pattern, with lazy loading and prefetching.
**Aspects:** custom `Object[T]` types on the generic core, own `Repository`/`Resolver`/`ResolvedObject` (copied from `gitlike`), generic `Walker[T]`, lazy loading via `CachedObject[T]`, prefetch-on-access (`SmartCache`), broken-reference detection.
**Structure:** `types.go` (Note/Tag/Attachment), `repo.go` (own Repository/Resolver/ResolvedObject/parseType), `main.go` (demo), `main_test.go`, `README.md`.
**Behaviors:** notes reference tags+attachments by digest; attachments are large blobs loaded lazily (`CachedObject.Load` only on access). Own `Resolver` resolves any digest to the right concrete type via `ResolvedObject` (no `any`). `SmartCache.GetWithPrefetch` warms references; metrics show hits after prefetch; a deliberately dangling reference is flagged as broken.
**Acceptance:** notes resolve across all three types; attachments not loaded until accessed; after prefetch the cache reports hits; broken references detected/reported without crashing.

### 3.4 `examples/api` — HTTP-exposure pattern (server over `cas`)

**Goal:** a self-contained HTTP server exposing a `cas` store to other processes — the pattern an app author copies for a network surface (the product ships no network JSON API). Built from the public `cas` library + std-lib `net/http` only.
**Aspects:** versioned prefix (`/api/cas/v1`), bearer-token auth with role matrix, streaming upload/download (large objects never fully buffered), dedup (raw exists-then-put), per-IP rate limiting, digest validation with `sha256.Parse`, JSON errors, OpenAPI self-doc at `/api/cas/v1/openapi.yaml`, and a plain-HTTP demo client (no SDK).
**Structure:** `server/` (main.go net/http + pattern routing + bearer middleware; ratelimit.go per-IP token bucket; hash.go hash-on-write spooling + envelope-type sniffing; openapi.yaml separate `//go:embed`; server_test.go httptest round-trip/roles/streaming/429), `demo/` (demo CLI round-tripping a file with plain net/http), `README.md`.
**Behaviors:** `server` stores/retrieves bytes by digest (no `algo` parameter: the surface is single-format and reports the constant `"algorithm": "sha256"` in list/meta/stats responses, with `/stats` returning `object_count`, `total_size`, `algorithm`), enforces roles (viewer read; operator store/verify; admin delete/gc), rate-limits per IP, returns JSON errors, serves its OpenAPI. `demo` PUTs a file, GETs it back, prints meta/stats — plain `net/http` + `sha256.Parse`, exactly as an SDK-less app would.
**Acceptance:** demo round-trips a file (identical bytes); a viewer-role token gets 403 on `DELETE`; large payloads stream unbuffered; `GET /api/cas/v1/openapi.yaml` served and matches routes. Server imports nothing but `cas` + stdlib.

### 3.5 `examples/viewer` → the product viewer

Covered by the **product viewer** in `internal/web/` (nested Go templates + htmx, dashboard, security); see viewer-design/security.

## 4. Aspect coverage matrix

| Aspect | files | artifacts | notes | api | viewer |
|---|---|:--:|:--:|:--:|:--:|
| `Digest` + client hasher (`sha256`) | ✓ | ✓ | ✓ | ✓ | product |
| `fs` fan-out (`WithFanOut`/`WithFanLevels`) | ✓ | ✓ | ✓ | ✓ | product |
| `Codec[T]` (custom) | ✓ JSON | ✓ gzip | ✓ | ✓ JSON | product |
| `Object[T]`/`Store[T]` | ✓ | ✓ | ✓ | ✓ | product |
| Dedup (`PutDedup`) | ✓ | ✓ | | ✓ | |
| `gitlike` (Repository/Resolver/WalkGraph) | ✓ | | | | |
| Custom app object model (own repo/resolver) | | | ✓ | | |
| Generic `Walker[T]` | ✓ | | ✓ | | |
| Lazy loading (`CachedObject[T]`) | | | ✓ | | product |
| Caching (`memory.CachedStore[T]`/`lru.Cache`) | | ✓ | ✓ | | |
| Prefetch-on-access (`SmartCache`) | | | ✓ | | |
| Cache metrics (`CacheMonitor`) | | ✓ | | | |
| Background `Preloader` | | | ✓ | | |
| `Stats`/`Verify`/`GC` | ✓ | ✓ | | ✓ | product |
| HTTP-exposure pattern | | | | ✓ | |
| Viewer (templates + htmx + dashboard) | | | | | product |
| Security (authn/authz, sessions, CSRF) | | | | ✓ bearer | product |
| Streaming (`io.Reader`/`io.ReadCloser`) | ✓ | ✓ | | ✓ | product |

## 5. Generating a new example on request

1. Identify the aspects of X in the matrix (§4); pick the closest example as base; mirror its structure/conventions.
2. Follow §2 rules (runnable, std-lib only, public APIs only, no `any`, documented, tested where assertable).
3. Add to §3 (or replace an obsolete one); mark aspects in §4, keeping the matrix complete.
4. Verify with `gofmt -l .`, `go vet ./...`, `go test ./...`, `go run ./examples/<name>`.

## 6. Acceptance checklist

- [x] All five proposed examples exist under `examples/` and build
- [x] Each runs standalone with meaningful output
- [x] No external deps; no CSS/JS in the viewer example; no `any` in example APIs
- [x] No example imports another except the shared `gitlike` library (rule 11)
- [x] Examples use only documented public APIs; `cas`/`gitlike` untouched
- [x] Each ships a `README.md` with rule-8 content + package comment; tested where assertable
- [x] Aspect matrix (§4) complete — every implementation aspect demonstrated by ≥ one example
- [x] Viewer example complies with viewer-security/design
