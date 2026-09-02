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
  - `cas/extra`: `SmartCache[T]` (prefetch on access), `CacheMonitor[T]`.
- **`examples/gitlike`** — the reference object model (`Blob`/`Tree`/
  `Commit`/`Tag`, `Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`,
  `CachedRepository`, `Preloader`).
- **`client/`** — the public CAS API client SDK (`Put`/`Get`/`Meta`/
  `List`/`Verify`/`Stats`/`GC`/`OpenAPI`, streaming, bearer auth).
- **`cmd/cask`** — the single entry point: CLI store operations (`put`,
  `get`, `cat`, `list`, `meta`, `stats`, `verify`, `gc`, `prune`) in local
  (`-store`) and remote (`-api`) modes, the `web` subcommand (embedded
  server), and `version`.
- **`internal/`** — the server implementation: `api` (CAS API R-01…R-14),
  `auth` (per-role bearer tokens, IP token-bucket rate limiter), `storage`
  (filesystem store service), `index` (pagination/envelope-type helpers),
  `web` (the embedded viewer: sessions, CSRF, roles, htmx templates).
- **The viewer** — dashboard, object list/detail with lazy hexdump,
  references (best-effort via gitlike), graph, stats, GC — secure by
  default (`cask web -viewer`), startup-token login, empty-body 401/403.
- **Examples** — `files` (gitlike miniature), `artifacts` (gzip codec +
  custom hash + cache + GC), `notes` (own types + lazy loading +
  prefetch), `api` (CAS HTTP server + demo), each with a rule-8 README.
- CI: gofmt/tidy/vet, `-race` + per-package coverage gate (cas, cas/extra,
  client, gitlike ≥ 90%), fuzz smoke (4 targets), benchstat, doc integrity.

### Changed

- Layout: `internal/` for all server implementation detail (Go-enforced
  privacy), root `client/` for the public SDK; the viewer lives in
  `internal/web/`; `cas/` stays at the repo root.
- The server became `cask web` (part of the CLI); `cmd/caskd` removed.
- OpenAPI documents MUST live in separate embedded `.yaml` files
  (api-design §13); the CAS API serves the canonical document from
  `internal/api/openapi.yaml`.
- Every example ships a `README.md` covering the cas core used, what it
  extends, a code walkthrough, and a Mermaid diagram (examples §2 rule 8).
- Examples renamed to short single-word names (`files`, `artifacts`,
  `notes`, `api`; proposed `viewer`).
- GitHub Actions bumped to Node-24 majors (`checkout@v5`, `setup-go@v6`);
  module caching disabled (std-lib-only module has no `go.sum`).

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

- `cas.NewStoreWithHasher` — undocumented, unconsumed constructor; custom
  hash algorithms use the documented `RegisterHash` + `NewStore` recipe
  (cas-core §4.2).
- `cmd/caskd` (absorbed into `cmd/cask web`).
- The top-level `viewer/` directory (viewer code moved to `internal/web/`).
