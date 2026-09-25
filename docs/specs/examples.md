---
type: Specification
title: Examples — go-cask
description: Guidance for generating example programs for CASK, plus four runnable examples (files, artifacts, notes, api) and the gitlike shared reference library — the viewer aspect is covered by the product object browser (internal/web). Every example ships a README.md documenting the `cas` core parts used and extended, a code walkthrough, and a Mermaid diagram.
version: v23
---

# Examples — go-cask

Which example programs exist, and how they are written: `files` (Git-like file store), `artifacts` (artifact cache), `notes` (own object model), `api` (HTTP-exposure pattern); `viewer` is the product object browser in `internal/web/` (§3.5). Examples are runnable reference programs that compile, demonstrate the documented APIs in real use, and are tested where behavior is assertable. They are NOT part of `cas`/`gitlike`. Related: cas-core, coding-guidelines, api-design, viewer-security, viewer-design.

## 1. Purpose

Three audiences: **doc readers** (a runnable program beats API signatures and maps to the spec sections it demonstrates), **app authors** (copy the pattern for your own object types/repository/server), **the test suite** (assertions keep the public API honest).

## 2. How to generate an example (rules)

1. **Location:** `examples/<name>/` in the main module (no separate `go.mod` unless genuinely required). Runnable demo = `package main`; reusable pieces = subpackages. **`gitlike/` is the reference support library**, and it is not an example: it is a package at the module root (`package gitlike`), so it is not under `examples/` and needs no exception from the runnable rule. The reference object model apps (and `files`) build on; NOT part of `cas`.
2. **Runnable:** `go build ./...`, `go run ./examples/<name>`, `go test ./examples/...` MUST pass (except `gitlike`, a library). The demo prints meaningful output (hashes, stats, traversal results).
3. **Std-lib only:** no external deps (coding-guidelines §3). Compression via `compress/gzip`; hashing is the client's job (`cas/hash/sha256`: `sha256.New()` for a store, `sha256.NewHasher()`/`sha256.Of` for hash-on-write) — the core names no algorithm, and no registry exists to extend.
4. **Public APIs only:** documented exported API of `cas`/`gitlike`; never reach into unexported internals.
5. **No `any` in example APIs:** define your own typed objects; for cross-type resolution use the supported `cas/repo` registry (or copy the `gitlike` pattern into your own package when you need something it does not express) — never extend `cas`/`gitlike`.
6. **One focus per example, real-world shape:** clear primary aspect (§4), a small believable program — not a kitchen sink, not a toy.
7. **Idiomatic Go:** `gofmt`, doc comments on exports, `context.Context` first, wrapped errors, table-driven tests (coding-guidelines §2, §7).
8. **`README.md` is REQUIRED** in the example folder (plus the package comment), teaching the example. It MUST contain: **What it demonstrates** (primary aspect + acceptance, one short paragraph); **`cas` core parts used** (exact components/APIs, e.g. `Store[T]`, `json.New[T]()`, `cas.Digest` reference fields, the `sha256.New()`/`sha256.Of` hasher, `fs.WithFanOut`/`WithFanLevels`, `Verify`, `GC`, `cachemem.CachedStore[T]`/`lru.Cache`, `CachedObject[T]`); **What it extends** (a custom `Codec[T]`, an own `Object[T]`/repo/resolver, an HTTP surface — never a custom hash algorithm: the client merely injects `cas.Hasher`) and what it does NOT modify (stated explicitly) (`cas`/`gitlike` untouched); **Code walkthrough** (files and roles, key flow); **A Mermaid diagram** (balanced, AGENT.md §9); **How to run** (exact commands + expected output shape). Focused, concrete — docs for app authors.
9. **Coverage:** the example set MUST keep covering the aspect matrix (§4); a duplicate-aspect example is discouraged unless it is a better teaching vehicle.
10. **Never modify the libraries for an example's sake:** a missing feature is a spec/library change — raise it separately, never hack around it in the example.
11. **Self-contained:** an example MUST NOT import another example's package, `internal/**` or `cmd/**`. The module's libraries — `cas/**` and `gitlike` — are ordinary imports, not exceptions: `gitlike` is a 2nd-class library at the application layer, so `files` importing it is the intended direction (AGENTS.md, "Layers and citizen classes", carries the full matrix). Examples never depend on `files`/`artifacts`/`notes`/`api`, and those never on each other.
    - Decision (2026-09): cache/recipe helpers (`SmartCache` in notes, `CacheMonitor` in artifacts) stay **inlined** teaching code in their own example, not shared packages. Create a shared home only when a **second consumer of that same helper** exists; decide that home deliberately then.

## 3. Proposed examples

### 3.1 `examples/files` — Git-like versioned file store

**Goal:** a small CLI storing file trees as content-addressable objects, committing them over `gitlike` end-to-end (a miniature Git). It also demonstrates the derived object-state report: every object classified verified/orphaned/corrupt/unverified from existing ops (`Verify` + reachability from `HEAD`) — states are scan results, never stored metadata.

**Aspects:** `gitlike` model (`Blob`/`Tree`/`Commit`/`Tag`), `Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`, `Store[T]`+JSON codec, `fs` fan-out, `cas/refs` for `HEAD`/`INDEX`, `cas.Verify`/`cas.VerifyAll`, `Stats`, derived-state audit (`List` + `cas.Reachable` + per-object `Verify`), CLI (`flag`-based `-store` parsing).
**Structure:** `main.go` (CLI: add, commit, log, cat, graph, audit, verify, stats), `audit.go` (derived-state report), `main_test.go`, `README.md`.
**Layout:** the `-store` root holds two disjoint trees, `objects/` (the `fs.Backend` base) and `refs/` (`cas/refs`): a ref inside a store base would be reported by `List`/`Stats` (digest-named files) and swept by `Clean` (`*.tmp`). The split teaches cas-core §4.4's "one base = one store" rule.
**Behaviors:** `add` stores blobs + builds a tree (identical content dedups); `commit -m` creates a `Commit` pointing at the tree + parent head (head is the `HEAD` ref in `cas/refs`, which also records a reflog); `log` walks parents via `WalkGraph`/`References()`; `cat` resolves+prints blob bytes; `graph` prints reachable graph with types; `audit [-no-verify]` lists all objects, expands the set reachable from `HEAD` with `cas.Reachable`, `Verify`s each, prints per-object state — `verified` (intact+reachable), `orphaned` (intact, unreachable — GC candidate), `corrupt` (Verify failed), `unverified` (reachable, skipped under `-no-verify`); states derived at scan time, never persisted (consistency §8); `verify` recomputes every digest through `cas.VerifyAll`; `stats` prints `N objects, M bytes`.
**Acceptance:** add→commit→log→cat round-trips; identical content across commits doesn't duplicate blobs; `verify` passes after a clean commit and reports a mismatch after on-disk corruption; `audit` reports clean=all `verified`, an uncommitted add's objects=`orphaned`, corrupted=`corrupt`, and under `-no-verify` reachable=`unverified`.

### 3.2 `examples/artifacts` — content-addressable build artifact cache

**Goal:** cache build outputs under their content digest with an opt-in gzip codec wrapper, bounded caching, metrics, mark-and-sweep GC.
**Aspects:** `cas/codec/gzip` (the shipped gzip wrapper over the JSON codec), `PutDedup`, caching (`lru.Cache`), cache metrics (`CacheMonitor`), named manifest refs (`cas/refs`), `cas.Reachable`/`cas.Sweep` for GC, `Stats`.
**Structure:** `main.go` (the `Artifact`/`Manifest` types + put/get/gc/stats/monitor CLI), `fuzz_test.go` (codec round-trip over the real stack), `main_test.go`, `README.md`.
**Layout:** like `examples/files`: `objects/` (the `fs` base) and `refs/` (`cas/refs`) under the `-store` root — a manifest name is a ref, not a manifest object found by decoding the store.
**Behaviors:** `put <name> <file>` stores the artifact under the client's `sha256` digest with `deduplicated: true/false`, writes the manifest, points the `name` ref at it, then deletes the replaced manifest (the ref is published before that delete, so a crash leaves a live pointer, not a dangling one). Manifests reference artifact digests (`[]cas.Digest`). `get <hash|name>` serves from `lru.Cache`; `CacheMonitor` prints hit rate on exit. `gc` takes `refs.Roots()`, expands it with `cas.Reachable` over the manifest store, reclaims the rest with `cas.Sweep`, reporting the sweep's own deleted count; a manifest whose stored type cannot be read **aborts** the sweep instead of losing its artifacts; `stats` before/after shows it.
**Acceptance:** same bytes → same digest → `deduplicated: true`; second `get` hits cache (hit rate > 0); `gc` deletes only unreferenced artifacts, leaves manifest-referenced intact, and fails without deleting anything when a manifest is damaged.

### 3.3 `examples/notes` — document graph with its own object types

**Goal:** an app with **its own** object model (`Note`, `Tag`, `Attachment`) resolved through the supported `cas/repo` registry — the "apps build their own object model on the core APIs" pattern without `gitlike`, plus lazy loading and prefetching.
**Aspects:** custom `Object[T]` types on the generic core, `cas/repo.Registry`/`RegisterStore`/`Resolve` for cross-type resolution, `cas/repo.Reachable` for the cross-type root set, generic `Walker[T]`, lazy loading via `CachedObject[T]`, prefetch-on-access (`SmartCache`), broken-reference detection.
**Structure:** `types.go` (Note/Tag/Attachment), `repo.go` (the app's Repository + `cas/repo` registry, plus its typed Resolver/ResolvedObject), `main.go` (demo), `main_test.go`, `README.md`.
**Behaviors:** notes reference tags+attachments by digest; attachments are large blobs loaded lazily (`CachedObject.Load` only on access). The registry resolves any digest to the right concrete type via the app's `ResolvedObject` (no `any`); `cas/repo.Reachable` expands a root across all three types. `SmartCache.GetWithPrefetch` warms references; metrics show hits after prefetch; a deliberately dangling reference is flagged broken.
**Acceptance:** notes resolve across all three types; attachments not loaded until accessed; after prefetch the cache reports hits; broken references detected/reported without crashing.

### 3.4 `examples/api` — HTTP-exposure pattern (server over `cas`)

**Goal:** a self-contained HTTP server exposing a `cas` store to other processes — the pattern an app author copies for a network surface (the product ships no network JSON API). Built from the public `cas` library + std-lib `net/http` only.
**Aspects:** versioned prefix (`/api/cas/v1`), bearer-token auth with role matrix, streaming upload/download (large objects never fully buffered), dedup (raw exists-then-put), per-IP rate limiting, digest validation with `sha256.Parse`, JSON errors, OpenAPI self-doc at `/api/cas/v1/openapi.yaml`, and a plain-HTTP demo client (no SDK).
**Structure:** `server/` (main.go net/http + pattern routing + bearer middleware; ratelimit.go per-IP token bucket; hash.go hash-on-write spooling + envelope-type sniffing; openapi.yaml separate `//go:embed`; server_test.go httptest round-trip/roles/streaming/429), `demo/` (demo CLI round-tripping a file with plain net/http), `README.md`.
**Behaviors:** `server` stores/retrieves bytes by digest (no `algo` parameter: the surface is single-format and reports the constant `"algorithm": "sha256"` in list/meta/stats responses, with `/stats` returning `object_count`, `total_size`, `algorithm`), enforces roles (viewer read; operator store/verify; admin delete/gc), rate-limits per IP, returns JSON errors, serves its OpenAPI. `demo` PUTs a file, GETs it back, prints meta/stats — plain `net/http` + `sha256.Parse`, as an SDK-less app would.
**Acceptance:** demo round-trips a file (identical bytes); a viewer-role token gets 403 on `DELETE`; large payloads stream unbuffered; `GET /api/cas/v1/openapi.yaml` served and matches routes. Server imports nothing but `cas` + stdlib.

### 3.5 `examples/viewer` → the product viewer

Covered by the **product object browser** in `internal/web/` (nested Go templates + htmx, security); see viewer-design/security.

## 4. Aspect coverage matrix

| Aspect | files | artifacts | notes | api | viewer |
|---|---|:--:|:--:|:--:|:--:|
| `Digest` + client hasher (`sha256`) | ✓ | ✓ | ✓ | ✓ | product |
| `fs` fan-out (`WithFanOut`/`WithFanLevels`) | ✓ | ✓ | ✓ | ✓ | product |
| `Codec[T]` (JSON, or the shipped gzip wrapper) | ✓ JSON | ✓ gzip+JSON | ✓ JSON | ✓ JSON | product |
| `Object[T]`/`Store[T]` | ✓ | ✓ | ✓ | ✓ | product |
| Dedup (`PutDedup`) | ✓ | ✓ | | ✓ | |
| `gitlike` (Repository/Resolver/WalkGraph) | ✓ | | | | |
| Custom app object model + `cas/repo` registry | | | ✓ | | |
| Named refs (`cas/refs`: atomic pointers + reflog) | ✓ | ✓ | | | |
| Root-set closure (`cas.Reachable` / `cas/repo.Reachable`) | ✓ | ✓ | ✓ | | |
| Generic `Walker[T]` | ✓ | | ✓ | | |
| Lazy loading (`CachedObject[T]`) | | | ✓ | | product |
| Caching (`cachemem.CachedStore[T]`/`lru.Cache`) | | ✓ | ✓ | | |
| Prefetch-on-access (`SmartCache`) | | | ✓ | | |
| Cache metrics (`CacheMonitor`) | | ✓ | | | |
| Background `Preloader` | | | ✓ | | |
| `Stats`/`Verify`/`GC` | ✓ | ✓ | | ✓ | product |
| HTTP-exposure pattern | | | | ✓ | |
| Viewer (templates + htmx + object browser) | | | | | product |
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
- [x] No example imports another example, `internal/**` or `cmd/**` (rule 11)
- [x] Examples use only documented public APIs; `cas`/`gitlike` untouched
- [x] Each ships a `README.md` with rule-8 content + package comment; tested where assertable
- [x] Aspect matrix (§4) complete — every implementation aspect demonstrated by ≥ one example
- [x] Viewer example complies with viewer-security/design
