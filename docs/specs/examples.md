---
type: Specification
title: Examples — go-cask
description: Guidance for generating example programs for CASK, plus four runnable examples (files, artifacts, notes, api) and the gitlike shared reference library — the viewer aspect is covered by the product object browser (internal/web). Every example ships a README.md documenting the `cas` core parts used and extended, a code walkthrough, and a Mermaid diagram.
version: v27
---

# Examples — go-cask

- `files` (Git-like store), `artifacts` (artifact cache), `notes` (own object model), `api` (HTTP pattern); `viewer` = product object browser in `internal/web/` (§3.5).
- Runnable reference programs: compile, demonstrate the documented APIs, tested where assertable; NOT part of `cas`/`gitlike`.
- Related: cas-core, coding-guidelines, api-design, viewer-security, viewer-design.

## 1. Purpose

- Three audiences: **doc readers** (runnable program mapped to the demonstrated spec sections), **app authors** (pattern to copy for own object types/repository/server), **the test suite** (assertions keeping the public API honest).

## 2. How to generate an example (rules)

1. **Location:** `examples/<name>/` in the main module, no separate `go.mod` unless required; demo = `package main`, reusable pieces = subpackages; `gitlike/` = module-root support library (`package gitlike`), not an example: object model `files` builds on, NOT part of `cas`.
2. **Runnable:** `go build ./...`, `go run ./examples/<name>`, `go test ./examples/...` MUST pass; demo prints meaningful output (hashes, stats, traversal results).
3. **Std-lib only:** no external deps (coding-guidelines §3); compression via `compress/gzip`; hashing = client's job (`cas/hash/sha256`: `sha256.New()` for a store, `sha256.NewHasher()`/`sha256.Of` for hash-on-write); core names no algorithm, no registry.
4. **Public APIs only:** documented exported API of `cas`/`gitlike`; never unexported internals.
5. **No `any` in example APIs:** own typed objects; cross-type resolution via `cas/repo` (or copy the `gitlike` pattern where it falls short); never extend `cas`/`gitlike`.
6. **One focus per example, real-world shape:** clear primary aspect (§4), small believable program — not a kitchen sink, not a toy.
7. **Idiomatic Go:** `gofmt`, doc comments on exports, `context.Context` first, wrapped errors, table-driven tests (coding-guidelines §2, §7).
8. **`README.md` REQUIRED** in the example folder (plus the package comment). MUST contain:
   - **What it demonstrates** — primary aspect + acceptance, one short paragraph.
   - **`cas` core parts used** — exact components/APIs: `Store[T]`, `json.New[T]()`, `cas.Digest` reference fields, the `sha256.New()`/`sha256.Of` hasher, `fs.WithFanOut`/`WithFanLevels`, `Verify`, `GC`, `cachemem.CachedStore[T]`/`lru.Cache`, `CachedObject[T]`.
   - **What it extends** — custom `Codec[T]`, own `Object[T]`/repo/resolver, or HTTP surface; never a custom hash algorithm (client injects `cas.Hasher`); state what it does NOT modify (`cas`/`gitlike` untouched).
   - **Code walkthrough** — files, roles, key flow.
   - **Mermaid diagram** — balanced (AGENT.md §9).
   - **How to run** — exact commands + expected output shape.
9. **Coverage:** the set MUST keep covering the aspect matrix (§4); a duplicate-aspect example is discouraged unless a better teaching vehicle.
10. **Never modify the libraries for an example's sake:** a missing feature is a spec/library change — raise it separately, never hack around it.
11. **Self-contained:** MUST NOT import another example's package, `internal/**` or `cmd/**`; `cas/**` and `gitlike` are ordinary imports (`gitlike` = 2nd-class application-layer library, so `files` importing it is intended — library-design.md §1.1 has the matrix); examples never depend on `files`/`artifacts`/`notes`/`api` or each other.
    - Decision (2026-09): cache/recipe helpers (`SmartCache` in notes, `CacheMonitor` in artifacts) stay **inlined**, not shared packages; share only when a **second consumer of that same helper** exists.

## 3. Proposed examples

### 3.1 `examples/files` — Git-like versioned file store

- **Goal:** file-tree CLI on content-addressable objects, committed over `gitlike` (miniature Git); derived-state report from `Verify` + `HEAD` reachability: verified/orphaned/corrupt/unverified — scan results, never stored metadata.
- **Aspects:** `gitlike` model (`Blob`/`Tree`/`Commit`/`Tag`), `Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`, `Store[T]`+JSON codec, `fs` fan-out, `cas/refs` for `HEAD`/`INDEX`, `cas.Verify`/`cas.VerifyAll`, `Stats`, derived-state audit (`List` + `cas.Reachable` + per-object `Verify`), CLI (`flag`-based `-store`).
- **Structure:** `main.go` (CLI: add, commit, log, cat, graph, audit, verify, stats), `audit.go` (derived-state report), `main_test.go`, `README.md`.
- **Layout:** `-store` root = two disjoint trees, `objects/` (`fs.Backend` base) and `refs/` (`cas/refs`); refs in a store base get reported by `List`/`Stats` and swept by `Clean` (`*.tmp`) — cas-core §4.4: one base = one store.
- **Behaviors:** `add` = blobs + tree (identical content dedups); `commit -m` = `Commit` → tree + parent head (head = `HEAD` ref in `cas/refs`, plus reflog); `log` walks parents via `WalkGraph`/`References()`; `cat` resolves+prints blob bytes; `graph` prints the reachable graph with types; `audit [-no-verify]` lists objects, expands the `HEAD`-reachable set via `cas.Reachable`, `Verify`s each, prints per-object state — `verified` (intact+reachable), `orphaned` (intact, unreachable: GC candidate), `corrupt` (Verify failed), `unverified` (reachable, skipped under `-no-verify`); states derived at scan time, never persisted (consistency §8); `verify` recomputes every digest via `cas.VerifyAll`; `stats` prints `N objects, M bytes`.
- **Acceptance:** add→commit→log→cat round-trips; identical content across commits doesn't duplicate blobs; `verify` passes after a clean commit and reports a mismatch after on-disk corruption; `audit` reports clean=all `verified`, uncommitted add=`orphaned`, corrupted=`corrupt`, `-no-verify` reachable=`unverified`.

### 3.2 `examples/artifacts` — content-addressable build artifact cache

- **Goal:** build-output cache keyed by content digest: opt-in gzip codec wrapper, bounded caching, metrics, mark-and-sweep GC.
- **Aspects:** `cas/codec/gzip` (the shipped gzip wrapper over the JSON codec), `PutDedup`, `lru.Cache`, `CacheMonitor`, named manifest refs (`cas/refs`), `cas.Reachable`/`cas.Sweep` for GC, `Stats`.
- **Structure:** `main.go` (`Artifact`/`Manifest` types + put/get/gc/stats/monitor CLI), `fuzz_test.go` (codec round-trip over the real stack), `main_test.go`, `README.md`.
- **Layout:** as `examples/files`: `objects/` (`fs` base) + `refs/` (`cas/refs`) under the `-store` root; a manifest name is a ref, not a manifest object found by decoding the store.
- **Behaviors:** `put <name> <file>` stores the artifact under the client's `sha256` digest with `deduplicated: true/false`, writes the manifest, points the `name` ref at it, then deletes the replaced manifest [ref published first: a crash leaves a live pointer, not a dangling one]; manifests reference artifact digests (`[]cas.Digest`); `get <hash|name>` serves from `lru.Cache`; `CacheMonitor` prints hit rate on exit; `gc` takes `refs.Roots()`, expands it with `cas.Reachable` over the manifest store, reclaims the rest with `cas.Sweep` (reporting its own deleted count); a manifest whose stored type cannot be read **aborts** the sweep instead of losing its artifacts; `stats` before/after shows it.
- **Acceptance:** same bytes → same digest → `deduplicated: true`; second `get` hits cache (hit rate > 0); `gc` deletes only unreferenced artifacts, leaves manifest-referenced intact, fails without deleting anything when a manifest is damaged.

### 3.3 `examples/notes` — document graph with its own object types

- **Goal:** app with **its own** object model (`Note`, `Tag`, `Attachment`) resolved through the `cas/repo` registry, without `gitlike`, plus lazy loading and prefetching.
- **Aspects:** custom `Object[T]` types on the generic core, `cas/repo.Registry`/`RegisterStore`/`Resolve` (cross-type resolution), `cas/repo.Reachable` (cross-type root set), generic `Walker[T]`, lazy loading via `CachedObject[T]`, prefetch-on-access (`SmartCache`), broken-reference detection.
- **Structure:** `types.go` (Note/Tag/Attachment), `repo.go` (Repository + `cas/repo` registry + typed Resolver/ResolvedObject), `main.go` (demo), `main_test.go`, `README.md`.
- **Behaviors:** notes reference tags+attachments by digest; attachments = large blobs loaded lazily (`CachedObject.Load` only on access); the registry resolves any digest to the right concrete type via the app's `ResolvedObject` (no `any`); `cas/repo.Reachable` expands a root across all three types; `SmartCache.GetWithPrefetch` warms references; metrics show hits after prefetch; a deliberately dangling reference is flagged broken.
- **Acceptance:** notes resolve across all three types; attachments not loaded until accessed; after prefetch the cache reports hits; broken references detected/reported without crashing.

### 3.4 `examples/api` — HTTP-exposure pattern (server over `cas`)

- **Goal:** HTTP server exposing a `cas` store to other processes — the copyable network-surface pattern (the product ships no network JSON API); public `cas` + std-lib `net/http` only.
- **Aspects:** versioned prefix (`/api/cas/v1`), bearer-token auth with role matrix, streaming upload/download (large objects never fully buffered), dedup (raw exists-then-put), per-IP rate limiting, digest validation with `sha256.Parse`, JSON errors, OpenAPI self-doc at `/api/cas/v1/openapi.yaml`, plain-HTTP demo client (no SDK).
- **Structure:** `server/` (`main.go` net/http + pattern routing + bearer middleware; `ratelimit.go` per-IP token bucket; `hash.go` hash-on-write spooling + envelope-type sniffing; `openapi.yaml` separate `//go:embed`; `server_test.go` httptest round-trip/roles/streaming/429), `demo/` (CLI round-tripping a file with plain net/http), `README.md`.
- **Behaviors:** `server` stores/retrieves bytes by digest (no `algo` parameter: single-format surface reporting the constant `"algorithm": "sha256"` in list/meta/stats responses, `/stats` returning `object_count`, `total_size`, `algorithm`), enforces roles (viewer read; operator store/verify; admin delete/gc), rate-limits per IP, returns JSON errors, serves its OpenAPI; `demo` PUTs a file, GETs it back, prints meta/stats — plain `net/http` + `sha256.Parse`.
- **Acceptance:** demo round-trips a file (identical bytes); a viewer-role token gets 403 on `DELETE`; large payloads stream unbuffered; `GET /api/cas/v1/openapi.yaml` served and matches routes; server imports nothing but `cas` + stdlib.

### 3.5 `examples/viewer` → the product viewer

- Covered by the **product object browser** in `internal/web/` (nested Go templates + htmx, security); see viewer-design/security.

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

1. Identify X's aspects in the matrix (§4); pick the closest example as base, mirror its structure/conventions.
2. Follow §2 rules (runnable, std-lib only, public APIs only, no `any`, documented, tested where assertable).
3. Add to §3 or replace an obsolete entry; mark aspects in §4, keeping the matrix complete.
4. Verify: `gofmt -l .`, `go vet ./...`, `go test ./...`, `go run ./examples/<name>`.

## 6. Acceptance checklist

- [x] All five proposed examples exist under `examples/` and build
- [x] Each runs standalone with meaningful output
- [x] No external deps; no CSS/JS in the viewer example; no `any` in example APIs
- [x] No example imports another example, `internal/**` or `cmd/**` (rule 11)
- [x] Examples use only documented public APIs; `cas`/`gitlike` untouched
- [x] Each ships a `README.md` with rule-8 content + package comment; tested where assertable
- [x] Aspect matrix (§4) complete — every implementation aspect demonstrated by ≥ one example
- [x] Viewer example complies with viewer-security/design
