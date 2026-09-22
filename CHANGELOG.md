# Changelog

Notable user-facing changes only. Routine maintenance, test-only changes,
internal refactors, and release preparation are omitted.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `cas.EnvelopeType` returns an object's versioned type name from its envelope
  header alone, so callers that only need to know what an object is (the viewer
  index, `gitlike` resolution) read a bounded prefix instead of buffering the
  whole object.
- `gob.NewRaw[T]()` builds a gob codec with no inner codec; `gob.New[T](next)`
  now takes the inner codec explicitly.
- `cas/backend` shares `WriteAll`, `ReadAll` and `ReadPayload` between backend
  implementations; `ReadPayload` sizes its buffer from the bytes that are
  actually present rather than from a declared header length.
- `flate`, `gzip` and `zlib` expose `MaxDecodedBytes` and `ErrDecodedTooLarge`,
  and `bloom/persistent.Filter` exposes `IsMapped` (Windows never memory-maps).
- `lru.Cache.CachedStore()` reaches the wrapped lazy-loading store for observers
  (metrics, key lookups) without touching the cache's recency bookkeeping.

### Changed

- Backend options are typed per backend (`fs.Option`, `mem.Option`,
  `packfs.Option`). The shared `backend.Option` accepted any configuration
  struct, so an option built for one backend compiled against another and
  silently did nothing; that is now a compile error.
- `bloom.NewGuard` reports nil arguments as an error instead of panicking, and
  the advisory filter contract is the exported `bloom.Filter`.
- `bloom.Parameters` returns an error and refuses a filter larger than the
  documented ceiling instead of panicking inside `make`.
- `bloom.Guard.Exists` rejects an absent digest with `cas.ErrInvalidDigest`, like
  the concrete backends, instead of reporting it as absent.
- `bloom/persistent.Filter.Close` is idempotent, and using a closed filter is a
  safe no-op (`Contains` reports false) rather than touching unmapped memory.
- `memory.CachedStore.Preload` and `Warmup` report every failure joined with
  `errors.Join`, and `OnNew` may now be installed at any time.
- `lru.Cache` no longer embeds `memory.CachedStore`: it exposes the methods it
  owns rather than the wrapped type's entire method set, and reaches the wrapped
  store only through `CachedStore()`.
- `gitlike.NewPreloader` and `fs.EnsureBase`/`fs.CleanupTemp` take a
  `context.Context`, so background preloading and large temporary-file sweeps
  honour the caller's cancellation.
- The library baseline is the documented Go 1.24 again: `go.mod` declares
  `go 1.24.0`, which required pinning the approved `golang.org/x/sys` dependency
  to the last release that does not itself require a newer toolchain.

### Fixed

- The viewer's object browser returns 500 when the store-wide metadata snapshot
  fails instead of panicking on a nil row slice.
- Malformed CBOR payloads (oversized or overflowing declared lengths) return an
  error rather than panicking or attempting an impossible allocation.
- Snapshot restore derives its payload buffer from the bytes actually present,
  so a crafted archive header can no longer demand an enormous allocation.
- `gitlike` resolution reads only the envelope header and reports a failed
  close; a large blob is no longer buffered twice just to learn its type.
- Cache prefetching is bounded in concurrency, visits each digest at most
  once, and no longer inherits a request-scoped caller context's
  cancellation — its own `prefetchTimeout` is the only thing that can cut it
  short, so a prefetch launched from a handler is no longer killed the
  instant the handler returns.
- `flate`, `gzip` and `zlib` stop inflating at `MaxDecodedBytes` (1 GiB) and
  return `ErrDecodedTooLarge`, so a small stored payload can no longer expand
  without limit.
- `fs.ValidateBase` rejects parent-traversal roots (`..`), which previously let
  `CleanupTemp` delete temporary files outside the store.
- The example HTTP surface's GC handler no longer races on its in-memory size
  index and no longer panics when collecting stats fails.
- CLI runtime store failures exit with code 1 instead of being reported as usage
  errors (exit code 2).

## [v1.6.5] - 2026-09-22

### Added

- Viewer reference state now identifies **Head** objects: reachable objects
  with no inbound references (the entry point of a reachable subtree), shown
  with a distinct blue pill and `reach=head` filter.

### Changed

- Viewer status pills now carry a translucent light border, so a pill never
  blends into a same-colored row background (hover or selection).

### Fixed

- The deterministic preview graph's Detached classification now matches its
  actual structure: only the last object in each eight-object block is truly
  detached (no later sibling references it back); the two ordinals previously
  misclassified as Detached are orphaned-with-inbound instead. The block's
  root ordinal is now also a Head candidate.

## [v1.6.4] - 2026-09-22

### Added

- Viewer reference state now identifies **Detached** objects: orphaned objects
  with no inbound references, shown with a distinct violet pill and filter.

### Changed

- Viewer status pills use semibold weight and darker per-state text colors
  for improved contrast against their tinted backgrounds.
- Viewer type scale increased roughly 10% (UI text, labels, monospace data,
  brand text, and search icon) for improved legibility.

### Fixed

- Opening the viewer on an empty store no longer fails while preview graph
  metadata is unavailable.
- Pack storage now supports digest widths beyond SHA-256 and rejects truncated
  payload records instead of returning zero-padded data.
- In-memory snapshot restore now rejects trailing data.
- Pack manifests reject unsafe file locations and malformed record bounds.
- Long filesystem maintenance scans respond promptly to context cancellation.
- Typed reads and integrity verification report backend close failures.
- Snapshot imports avoid attacker-controlled up-front map allocation.
- CLI maintenance commands reject negative retention ages, `verify` rejects
  extra operands, and viewer startup errors return documented exit codes.

## [v1.6.3] - 2026-09-22

### Changed

- Viewer object-list rendering now scales with visible rows for the default
  hash-ordered view, avoiding per-object formatting and sorting work for
  large stores.
- Added opt-in viewer scale benchmarks for 100 through 100,000 stored objects.

## [v1.6.2] - 2026-09-22

### Changed

- Refined the embedded viewer into a denser VS Code-style workbench with flat
  docked panels, compact type and table rhythm, quieter inspector/status
  presentation, and consistent interactive states.

## [v1.6.1] - 2026-09-22

### Changed

- Viewer object browsing reuses bounded metadata snapshots, reducing repeated
  filesystem scans during filtering, sorting, pagination, and htmx refreshes.
- Viewer verification reports the mismatched digest from its existing hash pass
  instead of reading corrupt objects a second time.

## [v1.6.0] - 2026-09-22

### Added

- `cask web` accepts `-hash-algo sha256|sha512|sha512_256`; the selected
  algorithm is shown in object metadata.

### Changed

- Viewer digest parsing and verification use the configured `cas.Hasher`
  instead of assuming SHA-256.
- Viewer object routes use `/dump` for the HTML byte dump and `/verify` for
  bulk verification.
- Added a subtle gray separator between the object table and inspector,
  matching the viewer's existing border system.

## [v1.5.0] - 2026-09-21

### Added

- Secure viewer response headers and a restrictive content security policy.
- Viewer-wide object verification with per-object results and audit records.
- Automatic object selection, reference navigation, and inspector history.
- Build version in the viewer top bar.

### Changed

- Viewer object browsing, filtering, sorting, reachability, references, and
  inspection were consolidated into one object-browser workspace.
- Viewer metadata reads are cached for immutable objects.
- Viewer controls and inspector layout were tightened for consistent sizing
  and keyboard-accessible navigation.

### Removed

- Viewer dashboard, garbage-collection page, delete action, and custom
  JavaScript. Destructive maintenance remains a CLI operation.

## [v1.4.6] - 2026-09-18

### Changed

- Refined the published documentation site navigation, search, and responsive
  layout.

## [v1.4.5] - 2026-09-17

### Fixed

- Hardened verification and release automation.
- Improved pack storage recovery and documentation.

## [v1.4.4] - 2026-09-16

### Added

- Optional packfile storage for large object stores.
- Persistent Bloom-filter improvements and cross-platform mapped-file support.

## [v1.4.3] - 2026-09-15

### Changed

- Consolidated codec APIs and refreshed performance-sensitive paths.

## [v1.4.2] - 2026-09-15

### Added

- Gzip codec and expanded codec documentation.

## [v1.4.1] - 2026-09-15

### Added

- Packfile backend and additional fuzz coverage.

## [v1.4.0] - 2026-09-15

### Added

- Advisory Bloom layer for faster object-set membership checks.
- Focused codec and hasher benchmarks.

## [v1.3.0] - 2026-09-10

### Added

- Binary codec and digest-prefix support.
- Consumer-facing examples covering the core and Git-like APIs.

### Changed

- Core storage is hash-algorithm agnostic through client-injected hashers.
- Git-like repositories receive codecs explicitly and enforce object
  invariants independently of serialization.

## [v1.2.0] - 2026-09-10

### Added

- Typed object stores, codecs, caching, graph traversal, and filesystem
  storage capabilities.

## [v1.1.0] - 2026-09-09

### Added

- Initial Git-like object model and repository APIs on top of the generic CAS
  core.

## [v1.0.0] - 2026-09-09

### Added

- First stable release of the generic, content-addressable storage core,
  filesystem backend, typed object layer, and Git-like reference model.

## [v0.3.0] - 2026-09-09

- Stabilized the core object, codec, backend, and repository APIs ahead of
  the 1.0 release.

## [v0.2.0] - 2026-09-09

- Added the typed object and codec layers above the byte backend.

## [v0.1.1] - 2026-09-09

- Improved initial storage performance and examples.

## [v0.1.0] - 2026-09-08

- Initial filesystem-backed content-addressable store and typed API.

## [v0.1.0-alpha.2] - 2026-09-03

- Added the first Git-like object and repository examples.

## [v0.1.0-alpha.1] - 2026-09-03

- Initial public design and prototype APIs.

[Unreleased]: https://github.com/dmundt/go-cask/compare/v1.6.2...HEAD
[v1.6.2]: https://github.com/dmundt/go-cask/compare/v1.6.1...v1.6.2
[v1.6.1]: https://github.com/dmundt/go-cask/compare/v1.6.0...v1.6.1
[v1.6.0]: https://github.com/dmundt/go-cask/compare/v1.5.0...v1.6.0
[v1.5.0]: https://github.com/dmundt/go-cask/compare/v1.4.6...v1.5.0
[v1.4.6]: https://github.com/dmundt/go-cask/compare/v1.4.5...v1.4.6
[v1.4.5]: https://github.com/dmundt/go-cask/compare/v1.4.4...v1.4.5
[v1.4.4]: https://github.com/dmundt/go-cask/compare/v1.4.3...v1.4.4
[v1.4.3]: https://github.com/dmundt/go-cask/compare/v1.4.2...v1.4.3
[v1.4.2]: https://github.com/dmundt/go-cask/compare/v1.4.1...v1.4.2
[v1.4.1]: https://github.com/dmundt/go-cask/compare/v1.4.0...v1.4.1
[v1.4.0]: https://github.com/dmundt/go-cask/compare/v1.3.1...v1.4.0
[v1.3.0]: https://github.com/dmundt/go-cask/compare/v1.2.0...v1.3.0
[v1.2.0]: https://github.com/dmundt/go-cask/compare/v1.1.0...v1.2.0
[v1.1.0]: https://github.com/dmundt/go-cask/compare/v1.0.2...v1.1.0
[v1.0.0]: https://github.com/dmundt/go-cask/compare/v0.3.0...v1.0.0
[v0.3.0]: https://github.com/dmundt/go-cask/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/dmundt/go-cask/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/dmundt/go-cask/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/dmundt/go-cask/releases/tag/v0.1.0
[v0.1.0-alpha.2]: https://github.com/dmundt/go-cask/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[v0.1.0-alpha.1]: https://github.com/dmundt/go-cask/releases/tag/v0.1.0-alpha.1
