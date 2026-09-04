# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The project is pre-release; the first public tag is `v0.1.0-alpha.1`
(pre-release), heading toward `v1.0.0` once the stable surface is frozen
(`versioning.md` §1).

## [Unreleased]

### Added

- `docs/benchmarks.md` — operator's guide to the benchmarks: the regular
  perf suite and the on-demand scale probes, with commands, parameters
  (`CASK_SCALE_OBJECTS`), purpose of each benchmark, and how to read the
  output (README points to it).

### Changed

- Fan-out decision recorded (cas-core §4.4): the file-name style is **not
  configurable** — full-hash names are the only layout. A Git-remainder
  option was considered and rejected (no Git interop under either style,
  a second mode in every layout-dependent method, and it loses the
  self-describing "file name = full hash" property `List`/`Stats`/`Verify`
  rely on); revisit only for a real consumer (cas-core v23).
- Fan-out layout clarified (no code change): file names were already always
  the complete digest at any fan-out level (verified on disk) — cas-core §4.4
  now states it explicitly and corrects the Git comparison (Git loose
  objects use the remainder `<38-hex>` as file name, we keep the full hash);
  defaults fan-out row rewording (cas-core v22, defaults v9).
- Examples spec §2 rule 11 records the 2026-09 decision: cache/recipe
  helpers (`SmartCache`, `CacheMonitor`, …) stay inlined per-example
  teaching code — a shared home appears only when a second consumer of the
  same helper exists, chosen deliberately then (examples v10).
- Deferred-extension catalog (`docs/instructions/extensions.md` §3) records
  the 2026-09 deferral decision with data-driven revisit triggers: packfiles
  (incl. its `.idx` index) only past ~10^5–10^6 objects or for bulk
  small-object ingest; compression/encryption only when an app needs them;
  chunking only for chunk-granular dedup of very large blobs (extensions v5).
- Instruction specs relocated to a **host-agnostic home**: the specification
  set moved from `.github/instructions/` to `docs/instructions/`, and the
  `.instructions.md` filename suffix was dropped — plain topic names now
  (`cas-core.md`, `cli.md`, `AGENT.md`, …). The agent aggregator also moved
  to the repo root as `AGENTS.md` (was `.github/copilot-instructions.md`)
  and was de-branded from Copilot: any agent honoring AGENTS.md auto-reads
  it. AGENT.md, the CI doc-integrity gate, and every cross-reference were
  updated for the new home (AGENT.md v8, AGENTS.md v8, cas-core v21; the
  other renamed specs each +1, `defaults.md` v7 unchanged).
- Non-code docs consolidated under `docs/`: the top-level `design/` folder
  moved to `docs/design/` (`core-overview.md`, `viewer-brief.md`, the viewer
  HTML mockup, the object-browser design JSON) — every doc now lives in the
  `docs/` tree (AGENTS.md v9, viewer-design v7).
- The CI allocs regression gate and its committed baseline were removed:
  `benchmarks/` deleted (was `cas.txt` + the `REFRESH` self-refresh marker),
  along with the CI benchmark/refresh/gate steps and `contents: write`;
  benchmarks stay runnable on demand via `go test -bench` (performance v9,
  defaults v8).
- New on-demand **state-scaling probes** (`BenchmarkScale{...}` in
  `cas/scale_bench_test.go`): prefill a store to N objects (N from
  `CASK_SCALE_OBJECTS`), time Put/Get/Exists/Delete/List/Stats at that
  size, and project wall time + FS file bytes for a 10^10-object store.
  Skipped unless the env var is set — never part of CI (performance v10).

## [v0.1.0-alpha.2] - 2026-09-03

### Added

- Core-overview diagram of the `cas` interfaces (byte / typed / caching
  layers and their dependencies) — canonical copy in `cas-core` §3.3, with
  the same diagram embedded in the README; `design/core-overview.md` points
  to the spec (cas-core v13, v14).

### Changed

- `cas` `FSRawStore.Put`: temp files now use **unique per-writer names**
  (created with `O_CREATE|O_EXCL`; a numeric suffix is appended only when
  another process holds `<path>.tmp`) instead of a deterministic
  `<path>.tmp` — concurrent writers of the same hash never share a temp
  inode, so cross-process same-hash writes cannot corrupt each other; on
  POSIX the atomic rename gives last-wins, on Windows a racing Put may
  transiently error but never corrupts (cas-core v18, operations v4).
- `cask`: **grace-based maintenance sweeps, writers lock-free**. Writers
  (`put`) and the viewer (`web`) never lock — object writes are safe across
  processes by construction (unique temps + atomic rename). Maintenance
  sweeps (`gc`, `prune`, `clean`) take the store's exclusive cross-process
  lock (`.cask.lock`, holding the PID) so two sweeps never overlap, and
  reclaim only objects older than `--min-age` (default 24h), so a concurrent
  writer's fresh objects survive. A forced `--min-age 0` sweep is the
  documented dangerous variant (prints a warning; only safe with no other
  writer). `gc` gained the `--min-age` flag (was immediate)
  (cli spec v9, cas-core v19, backend-architecture v10, consistency v6).
- Concurrency model documented: cas is **multi-client safe within one
  process** (lock-free reads, per-process mutexes, atomic writes — cas-core
  §6); across processes, reads and same-hash `Put`s are safe by construction
  while maintenance sweeps must be grace-gated against live writers
  (cas-core v19, backend-architecture v10, consistency v6).
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
