# Changelog

Notable user-facing changes only. Routine maintenance, test-only changes,
internal refactors, and release preparation are omitted.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `cas.CapabilitiesOf` reports which optional maintenance operations a backend
  supports (`Cleaner`, `Statter`), and the new `cas.VerifyAll`/`cas.Sweep`
  functions give every backend — including `packfs`, which has no
  backend-native GC of its own — a working integrity-check and
  mark-and-sweep reclamation path using only the minimal `Backend` interface.
  `fs.Backend`'s existing `Verify`/`GC`/`Prune` remain the faster,
  backend-native path where available.
- `cas/repo` promotes gitlike's example-only `Codecs`/`Repository`/`Resolver`/
  `WalkGraph` pattern into a supported package: a `Registry` resolves a
  `cas.Digest` to its typed object across however many caller-registered
  types, `Walk` visits every reachable object exactly once regardless of type
  and reports an unregistered type to the caller instead of aborting,
  `Reachable` is the cross-type root-set builder `Backend.GC`/`Backend.Prune`
  require for a multi-type object graph, and `LookupStore[T]` hands back the
  `*cas.Store[T]` registered under a type name — typed rather than `any`, so a
  lookup returns an `*UnknownTypeError` for a name nothing registered (or an
  error naming both types for a name registered under another `T`) instead of a
  nil store or a caller-side type assertion.
- `cas/refs` adds named, mutable pointers to a `cas.Digest` ("refs"): atomic
  `Set`/`Delete`, `Get`/`List`/`Resolve` (with ambiguous-prefix detection),
  an append-only reflog per name (`Log`/`Previous`), and `Roots` — the ready
  root set for `cas.Reachable` and `Backend.GC`/`Backend.Prune`. `ValidateName`
  rejects any name that is unsafe as a cross-platform path component (Win32
  reserved characters/device names, a trailing space or `.`, backslash, and
  control characters), verified by a fuzz test exercising real `Set`/`Get`
  round-trips.
- `cas.Reachable` computes the transitively-closed reachable set from a list
  of root digests, using a caller-supplied `cas.RefLister` to expand
  each object's references. This is the documented, correct way to build the
  set `Backend.GC`/`Backend.Prune` require before calling them.
- `cas.EnvelopeType` returns an object's versioned type name from its envelope
  header alone, so callers that only need to know what an object is (the viewer
  index, `gitlike` resolution) read a bounded prefix instead of buffering the
  whole object.
- `cas.PeekType` and `Store.Type` report an object's versioned type name from the
  envelope header on a stream: the payload is neither read nor allocated,
  whatever its size, so enumerating a store by type is `List` plus `Type`
  instead of a decode per object.
- `cas.CodecNamer` lets a codec declare the wire format it produces (`json`,
  `gzip+json`, `""` for none), and `cas.ErrCodecMismatch` reports reading an
  object that was written with a different codec. The store resolves the tag
  once at construction, writes it into every envelope, and compares it before
  decoding, so changing a codec is reported as a format change instead of a
  decode failure and no longer requires hand-bumping every type's major version.
- `gob.NewRaw[T]()` builds a gob codec with no inner codec; `gob.New[T](next)`
  now takes the inner codec explicitly.
- `cas/backend` shares `WriteAll`, `ReadAll` and `ReadPayload` between backend
  implementations; `ReadPayload` sizes its buffer from the bytes that are
  actually present rather than from a declared header length.
- `flate`, `gzip` and `zlib` expose `MaxDecodedBytes` and `ErrDecodedTooLarge`,
  and `bloom/persistent.Filter` exposes `IsMapped` (Windows never memory-maps).
- `lru.Cache.CachedStore()` reaches the wrapped lazy-loading store for observers
  (metrics, key lookups) without touching the cache's recency bookkeeping.
- `cas.GetMany` streams a batch of digests in one call, and the optional
  `cas.BatchGetter` interface lets a backend serve that batch its own way:
  `packfs` now groups the requested objects by pack file and opens each pack
  once per batch instead of once per object. `GetMany` closes every reader it
  hands to the callback after the callback returns, and falls back to a
  sequential `Get` loop for backends that do not batch. The `Backend`/`Store`
  docs and cas-core §4.13 record the contract and the prefetch recipe for the
  typed or parallel path (`lru.Cache`/`prefetch.SmartCache`, sized from
  `Stats`, fed by `Reachable`).
- `cask -backend fs|packfs` (default `fs`) selects the storage backend for every
  store subcommand, so `verify`, `gc`, `prune` and `clean` maintain a packed
  store as well as a loose one: a backend without a native implementation runs
  through the portable `cas.VerifyAll`/`cas.Sweep`/`cas.Cleaner` layer, and an
  operation a backend cannot perform fails with an error naming the operation
  and the backend (`cas.ErrUnsupported`) instead of reporting success. Every
  command closes the store it opened, so a writer releases the backend's
  resources; `cask web` requires the `fs` backend and refuses `packfs` the same
  way.

### Changed

- The stored envelope is version 2 —
  `[version u8][uvarint codecLen][codec][uvarint typeLen][type][uvarint payloadLen][payload]`
  — so every object records the codec identity that wrote it. Version 1 objects
  (no codec field) still load and read as "codec unspecified", and an object
  whose codec declares no tag is read unchanged. Because the stored bytes
  changed, the same value now hashes to a new address: re-storing it under
  version 2 writes a second object instead of deduplicating against its
  version 1 copy, and stores converge as objects are rewritten.
- `fs.Backend.Prune` now takes an already-expanded `reachable map[string]bool`,
  matching `fs.Backend.GC`, instead of a bare `roots []cas.Digest` slice.
  Previously `Prune` treated the given roots as the complete reachable set and
  never followed their references, so an object referenced only by a root
  (and not passed explicitly) was silently deleted — the opposite of what its
  documentation claimed. Callers that have a typed object graph to expand
  should build the set with `cas.Reachable` (or an equivalent typed walk)
  before calling `Prune`; the `cask` CLI's `prune`/`gc` subcommands do this
  internally, but still require every digest that must survive to be listed
  in `<roots...>` since the CLI has no typed model to expand it with.
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
- The viewer's zero-inbound reachable reference state is renamed from `Head`
  to `Root`, to avoid colliding with Git's HEAD concept in a store that already
  uses Git-like terminology (Blob/Tree/Commit/Tag) elsewhere: the `reach=head`
  filter value, the `Head` pill, and the `objectRow.Head`/`HasHead` fields are
  now `reach=root`, `Root`, and `objectRow.Root`/`HasRoot`.

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
- `Store.Close` is idempotent, tolerates a nil receiver or a store with no
  backend, and returns the backend's close error to every caller instead of
  reporting a different result per call.
- `fs.Backend.Stats` no longer fails when an object vanishes mid-walk — a
  concurrent `Delete` — so a report gathered while objects are being removed
  returns what was there instead of an `lstat` error; `List` already tolerated
  the same race.
- The `packfs` pack index survives a restart again: manifest keys were raw
  digest bytes, and `encoding/json` replaces invalid UTF-8 in a map key with
  U+FFFD, so reopening a packed store came back with corrupted keys — `List`
  reported phantom digests and `Stats` counted every object twice. The index is
  keyed by the digest's hex form now, and an index written by an older build is
  dropped on load (the loose tree still holds every object).

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
