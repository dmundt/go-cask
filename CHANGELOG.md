# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The project is pre-release; the first public tag will be `v1.0.0`
(`versioning.instructions.md` §1).

## [Unreleased]

### Added

- **`cas` core library** — the generic, content-addressed storage core:
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
