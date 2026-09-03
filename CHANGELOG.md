# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The project is pre-release; the first public tag is `v0.1.0-alpha.1`
(pre-release), heading toward `v1.0.0` once the stable surface is frozen
(`versioning.instructions.md` §1).

## [Unreleased]

### Added

- Core-overview diagram of the `cas` interfaces (byte / typed / caching
  layers and their dependencies) — canonical copy in `cas-core` §3.3, with
  the same diagram embedded in the README; `design/core-overview.md` points
  to the spec (cas-core v13, v14).

### Changed

- Concurrency boundary documented: cas is **multi-client safe within one
  process** (lock-free reads, per-process mutexes, atomic writes — cas-core
  §6). Multiple OS processes MUST NOT share one store directory — there is
  no inter-process locking; keep one store directory ↔ one process and serve
  many clients from it (cas-core v16, backend-architecture v8, consistency
  v5).
- Naming: the acronym expansion is **Content Addressable Store (Kit)** and is
  written ALL-CAPS (`CAS`, `CASK`) everywhere — lowercase `cas` only as the
  Go package — replacing the former "Content Addressed Storage (Kit)" wording
  in README, specs, package comments, and example READMEs; the
  "Cas core parts used" README headings are now "`cas` core parts used"
  (AGENT.md v6).
- `examples/files`: new `audit` command — a derived per-object state report
  (verified / orphaned / corrupt / unverified) built from `List` +
  reachability-from-HEAD marking + per-object `Verify`, with a
  `-no-verify` fast-orphan mode; states are scan results, never stored
  (examples spec v8).
- `cas-core` §3.1 layer diagram converted from a fragile ASCII box to a
  Mermaid flowchart (subgraphs per layer + dependency edges) (cas-core
  v15).
- `cas` typed layer: reads renamed for symmetry — `Store.GetTyped` is now
  `Store.Get` (concrete `T`), and the cached layer's loaded read is
  `CachedStore.Get`/`LRUCache.Get` with the lazy proxy accessor renamed
  `Get` → `Proxy`. `Put`/`Get`/`Delete` now read as a natural trio across
  layers (cas-core v14, library-design v7, testing-strategy v8,
  performance v7).

## [v0.1.0-alpha.1] - 2026-09-03

### Added

- **`cas` core library** — the generic core of a content-addressable store:
  - byte layer: `Hash`/`ParseHash`/`NewHash`/`RegisterHash`/`NewHasher`/
    `HashBytes` (sha1 + sha256), `RawStore` contract, `FSRawStore`
    (fan-out layouts, atomic temp→`Sync()`→rename writes, lock-free reads,
    `Stats`/`Verify`/`GC`/`Prune`), `MemoryRawStore`, sentinel errors;
  - typed layer: `Object[T]`, `Codec[T]`/`JSONCodec[T]`, `Store[T]`
    (one-pass hashing, `PutDedup`, self-describing envelope with
    type-verifying reads), `Walker[T]`;
  - caching: `CachedObject[T]`, `CachedStore[T]`, in-tree `LRUCache[T]`;
  - **`examples/gitlike`** — the reference object model (`Blob`/`Tree`/
  `Commit`/`Tag`, `Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`,
  `CachedRepository`, `Preloader`).
- **`cmd/cask`** — the single entry point: CLI store operations (`put`,
  `get`, `cat`, `list`, `meta`, `stats`, `verify`, `gc`, `prune`) over the
  library in-process, the `web` subcommand (the embedded viewer), and
  `version`.
- **`internal/`** — the viewer implementation: `web` (sessions, CSRF,
  roles, htmx templates), `storage` (filesystem store service), `index`
  (pagination/envelope-type helpers).
- **The viewer** — dashboard, object list/detail with lazy hexdump,
  references (best-effort via gitlike), graph, stats, GC — secure by
  default (`cask web`), startup-token login, empty-body 401/403.
- **Examples** — `files` (gitlike miniature), `artifacts` (gzip codec +
  custom hash + cache + GC), `notes` (own types + lazy loading +
  prefetch), `api` (HTTP-exposure pattern: store server + plain-HTTP demo), each with a
  rule-8 README.
- CI: gofmt/tidy/vet, `-race` + per-package coverage gate (cas, gitlike
  ≥ 90%), fuzz smoke (4 targets), doc integrity, import boundaries, benchmark
  allocs gate.

- cas: `FSRawStore.Size` (per-object size) and `Clean` (orphan `*.tmp`
  sweep).

### Changed

- Layout: `internal/` for all implementation detail (Go-enforced privacy);
  the viewer lives in `internal/web/`; `cas/` stays at the repo root.
- The server became `cask web` — the embedded viewer only, no JSON API
  surface; `cmd/caskd` removed.
- OpenAPI documents MUST live in separate embedded `.yaml` files
  (api-design §13) — JSON example surfaces only (`examples/api`); the
  viewer needs none.
- Every example ships a `README.md` covering the cas core used, what it
  extends, a code walkthrough, and a Mermaid diagram (examples §2 rule 8).
- Examples renamed to short single-word names (`files`, `artifacts`,
  `notes`, `api`; proposed `viewer`).
- GitHub Actions bumped to Node-24 majors (`checkout@v5`, `setup-go@v6`);
  module caching disabled (std-lib-only module has no `go.sum`).

- `internal/storage` removed: `cmd/cask` and the viewer use `cas.FSRawStore`
  directly (the service layer had become a passthrough).
- CLI: the `cat` alias is gone — `get` without `-o` prints to stdout
  (cli spec v7).

- `cas` typed layer: `Store[T Object[T]]` — the constraint makes every
  handled value an object at compile time (no type assertions remain); `Put`
  takes the concrete `T`; new sentinel `ErrCorrupt` for payloads the codec
  cannot decode (cas-core v11).

- `cas` typed layer: `Store[T].Get` retired — reads return the concrete `T`
  via `GetTyped` (type-verified) or the bytes via `GetRaw`; `Walker` visits
  now take `func(T) error`; the cached layer returns concrete `T` from
  `Load`/`GetTyped` (cas-core v12, coding-guidelines v6).
- `cas` typed layer: the `Codec[T]` is now the single serialization
  authority — `Store.Put` builds the envelope from `codec.Encode` + the
  object's `Type()`; `Object[T]` shrank to `{Type, References}`
  (`Serialize`/`Deserialize` removed from the contract; example serializers
  become vestigial and are removed in a follow-up). cas-core v10.

### Fixed

- CI steps that assumed a `go.sum` and a single-package `./cas/...`
  (dependency-free module + `cas/extra`); coverage gating per package;
  doc-integrity false positive for pattern literals.
- `sha256-double` (examples spec) renamed to `sha256double` — the name must
  obey the hash-string validation pattern (defaults §2).
- Fuzz-discovered `encoding/json` lossy invalid-UTF-8 round-trip — the
  codec fuzz target constrains input; regression corpus committed.
- Stale `examples/cas-api/client` reference in backend-architecture §5.

### Removed

- `cas/extra` — `SmartCache`/`CacheMonitor` inlined into `examples/notes` and
  `examples/artifacts` (example recipes, not core; cas-core v9).
- `client/` — the public CAS API client SDK; CLI remote mode (`-api`/
  `-token`) goes with it.
- `internal/api` + `internal/auth` — the CAS JSON API handlers, bearer-token
  auth, and the IP rate limiter.
- The CAS HTTP API surface: the `cas-api` / `viewer-api` specs, `/api/cas/v1`
  routes, the viewer OpenAPI doc + `/swagger/` (never implemented), and the
  `-viewer`/`-rate`/`-burst` flags (`cask web` now IS the viewer).
- Viewer references/graph + `internal/storage.Raw()`: the viewer is a
  byte-layer tool and no longer imports `examples/gitlike` (dependency rule,
  coding-guidelines §9).
- `cas.NewStoreWithHasher` — undocumented, unconsumed constructor; custom
  hash algorithms use the documented `RegisterHash` + `NewStore` recipe
  (cas-core §4.2).
- `cmd/caskd` (absorbed into `cmd/cask web`).
- The top-level `viewer/` directory (viewer code moved to `internal/web/`).
