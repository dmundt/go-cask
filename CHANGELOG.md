# Changelog

Notable user-facing changes only. Routine maintenance, test-only changes,
internal refactors, and release preparation are omitted.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `cas/verify/sidecar` records an optional per-object checksum beside a store's
  bytes, so a cheap check (`crc32`, `adler32` or `crc64`) can run over a store
  whose identity is a strong hash: the record lives at
  `<base>/.meta/<hex>.json`, the object's address is untouched, and a record is
  never part of the hashed bytes. `cask verify --checksums [--checksum <algo>]`
  reads the records, and `cask gc`/`cask prune` reconcile them after a sweep. An
  object with no record is reported as unchecked, never as corrupt.
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
- `cas.EnvelopeVersion` is the envelope format version the current build writes,
  and `cas.PeekVersion`/`Store.Version` report a stored frame's version from its
  leading byte alone — one byte, whatever the payload size, returned **verbatim
  even when this build does not know that version**. A store holding objects of
  more than one envelope layout is therefore navigable without string-matching an
  error: compare the byte with `cas.EnvelopeVersion` to tell "written by a newer
  format" from "damaged bytes".
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
- `cask seed-preview -hash-algo sha256|sha512|sha512_256` (default `sha256`)
  seeds the viewer preview graph under the algorithm the viewer reads it with.
  Seeding was always sha256 before, so a viewer started with any other
  `-hash-algo` silently found no graph and showed no references.

- `gitlike.Resolver` satisfies `cas/repo.Resolver`: its new `Resolve(ctx, d)`
  returns the concrete object, so `cas/repo.Walk` and `cas/repo.Reachable` run
  over a gitlike repository — a gitlike root set now expands with the supported
  cross-type walk instead of a hand-written traversal. `WalkGraph` keeps its
  signature and stricter unknown-type behaviour and is now an adapter over
  `cas/repo.Walk`. `Repository.Close`/`CachedRepository.Close` release the shared
  backend (packfs flushes its active pack there), `CachedRepository.GetTag`
  completes the cached getters, and `(*ResolvedObject).References()` reports the
  union's outgoing references so callers stop re-deriving them.

### Changed

- The `cas/pack` manifest helpers take the caller's `context.Context` first, and
  they never substitute a codec: a nil codec is `pack.ErrNilCodec` instead of a
  silent switch to JSON, so the codec that decides what is on disk is always the
  caller's. The JSON convenience is now explicit — `SaveJSON`/`LoadJSON` (and
  `EncodeJSON`/`DecodeJSON`) replace the old `Save`/`Load`/`Encode`/`Decode`, and
  `Store` is built with `pack.New(path, codec) (*Store[T], error)`. Migration:
  pass a context and name the JSON helpers, or hand your own codec to
  `SaveWith`/`LoadWith`. `cas/pack` is a helper layer outside the frozen surface
  (cas-core §7.1), so the rename ships in `v1`.
- The user-facing documentation reads leaner without losing a rule: the README
  and the normative specs under `docs/` (including `docs/specs/cas-core.md`)
  state the same contracts, defaults, sentinel errors and measured numbers in
  shorter prose, and the README's table of contents matches its sections again.
  Every code fence, table row, heading and inline identifier is unchanged.

- The viewer's one-time login hint is printed to **stdout** — the stream that
  carries command output, not the error stream a supervisor or a log shipper
  retains — and `cask web -show-token` displays it in any run: a bare
  `-show-token` forces the hint without a terminal, `-show-token=false` never
  shows it, and an absent flag keeps the interactive-terminal heuristic, so an
  operator under a supervisor can ask for the hint while an unattended
  deployment keeps using `-token-file`/`CASK_VIEWER_TOKEN`. A non-loopback bind
  no longer prints a `http://` login link that could not hold a session (the
  cookie is always `Secure`): the notice names the bind and the `https://`
  expectation instead.

- gitlike resolves an object's type from the **versioned** envelope name
  (`cas.EnvelopeType` on a bounded header prefix), so an object stored as
  `blob@2` is reported as an unknown type instead of being decoded through the
  `@1` model; an absent major version still reads as `@1`.

- The `artifacts` example stores through the shipped `cas/codec/gzip` wrapper
  (`gzip.New(json.New[T]())`) instead of a bespoke gzip codec, so its objects
  record the codec identity (`gzip+json`) and bound decompression. The envelope
  of newly written artifacts therefore carries the tag, which changes their
  digests — an existing example store keeps reading (v1/v2 objects both load)
  but only new writes get the identity.

- The `files` and `artifacts` examples keep their named pointers outside the
  object store: the example root now holds `objects/` (the `fs` base) and
  `refs/` (`cas/refs`, with an atomic write and a reflog per name), and
  `-store` names that root. Refs previously lived beside the objects as plain
  files written in place; they move because a ref inside a store base is
  reported by `List`/`Stats` (digest-named files) and swept by `Clean`
  (`*.tmp`), which is cas-core §4.4's one-base-one-store rule. An existing
  example store needs its objects moved under `objects/` (each README says so).

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
- `fs.New` and `packfs.New` validate the store directory before creating
  anything: a base that is empty or whitespace, `.`, the filesystem root, a
  volume root (`C:`), or a parent-traversal path (`..`, `../store`) is rejected,
  where it previously opened a backend that owned the caller's whole working
  directory or drive. A nested directory is still a valid base — only the caller
  can tell whether it already belongs to another store — so
  `fs.New(filepath.Join(root, name))` and `packfs`'s own loose sub-store are
  unaffected.
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

- A `cas/pack` manifest write is atomic — a temp file in the target directory,
  fsynced, then renamed — instead of one `os.WriteFile`, so a crash or a full
  disk mid-write leaves the previous manifest intact rather than a truncated
  file that no longer decodes. It is the publish path every other writer in the
  repository already used.
- The portable sweep (`cas.Sweep` — the path `cask -backend packfs gc|prune`
  takes, since the packfile backend has no native sweep) no longer fails halfway
  on a stray digest-named file that its store's layout cannot address: `List`
  reports any lowercase-hex file name, while only the backend knows which names
  it can address, so the sweep asks before it deletes and skips the rest. It
  previously returned `ErrInvalidDigest` after part of the store had already
  been reclaimed, and kept failing until the file was found by hand; a dry run
  now reports the same set a real run reclaims, matching `fs.GC`/`fs.Prune`.
- A persistent Bloom filter (`cas/bloom/persistent`) keeps its hint set across
  processes: the file now carries an index key, and the default Bloom index hash
  is derived from it instead of from a seed drawn per process. A filter reopened
  by the next process previously reported every digest it had recorded as
  absent, and `bloom.Guard` turns a negative into an authoritative absence — so
  a warm cache answered "object missing" for objects that are stored. A file
  written before the header existed, or under a different index hash, is rebuilt
  empty instead of trusted, and `bloom.DefaultIndexHash` is documented as
  process-local and unusable for bits that outlive the process.
- The viewer's login throttle is proxy-aware: `cask web -trusted-proxy
  <ip|cidr,...>` names the reverse proxies whose forwarded client address
  (`X-Forwarded-For`, or RFC 7239 `Forwarded`) the throttle may believe, so
  clients behind a proxy get a bucket each instead of sharing the proxy's and
  letting five failed logins lock every operator out for up to 30 minutes.
  With no trusted proxy configured — still the default — a forwarded header is
  ignored and the direct peer address keys the throttle, so a spoofed header
  can neither evade the throttle nor block another client; a malformed
  `-trusted-proxy` entry fails startup rather than silently trusting nothing.
- The viewer classifies a verification outcome the way `cask verify --all`
  does: only a digest mismatch is `corrupt`, so an object that is missing or
  unreadable is reported as unverified (with `Missing`/`Unreadable` prose)
  instead of as corrupted content. The result badge follows the same state
  instead of always rendering the corrupt style.
- The `files` example no longer writes a `*.crc32` sidecar beside every object.
  Those files were invisible to `List` but unreclaimable, and a missing sidecar
  made `audit` report an intact object (one written by `cask put` or a snapshot
  import) as `corrupt`; integrity is now the core's `Verify`/`VerifyAll`.
- The `artifacts` example resolves its GC roots from the manifests' named refs
  and expands them with `cas.Reachable`, so a manifest that cannot be decoded
  aborts the sweep instead of having its artifacts deleted as unreachable, and
  `gc` reports the number of objects it actually removed.
- `seed-preview` no longer truncates the preview graph at the first missing
  object: a swept (Detached) object drops only its own edges, so the blocks
  after it keep their roots and the viewer stops showing their reachable
  objects as orphaned.
- The `api` example reports an object's size from the backend's physical
  metadata (`cas.Statter`) instead of a process-local index, so `list`/`meta`
  no longer report `size: 0` for objects the running process did not write
  itself (after a restart or a snapshot import).
- The viewer's single-object verify reports a failed reader close instead of
  discarding it, so a close error cannot pass as a clean verification.
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
- A damaged stored object is no longer reported as an unknown type. A truncated
  or otherwise unreadable envelope is now `cas.ErrCorrupt` from every reader
  (`Store.Get`, `EnvelopeFromBytes`/`EnvelopeType`, `PeekType`,
  `cas/repo.Registry.Resolve`), which is what `Store.Type`/`PeekType` already
  reported for those bytes; `cas.ErrUnknownType` now means only that an intact
  object names a type the caller does not handle. Consumers that skip an object
  with `errors.Is(err, cas.ErrUnknownType)` — "not my type, leave it alone" —
  can no longer mistake an unreadable object for one they simply do not decode,
  which in a maintenance path such as a GC sweep meant treating a damaged
  manifest as a leaf and deleting the objects it referenced.

### Security

- A viewer bound to a non-loopback address no longer displays its generated
  startup token, whatever `-show-token` asks for: the one-time hint is permitted
  only for a loopback bind, so that run prints the bind and the `https://`
  expectation instead and logs the reason without the token. The browser launch
  that carries the token deep link is skipped for a non-loopback bind and when
  `-show-token=false` suppresses the display, so the admin credential can no
  longer reach another process's argument vector either.
- The viewer's HTTP responses are hygienic: a throttled login answers `429` with
  the `Retry-After` delay it is actually enforcing, a rejected token is answered
  `401` with no body — the reason appears on the login page, never in the
  refusal — every response is `Cache-Control: no-store` and names `Cookie` in
  `Vary` so a proxy between the browser and the viewer cannot serve one
  session's page to another, and a verification failure renders only the
  viewer's own prose: the underlying error, which can name the store's
  filesystem paths, now goes to the audit line instead of the operator's screen.

- The viewer's startup token is no longer written to the process log: `cask web`
  emitted it with `slog.Warn("viewer startup token", "admin_token", …)`, so
  under systemd/journald, Docker, or a log shipper the admin credential was
  retained and indexed for readers who are not operators. No log level carries
  the token now. An interactive `cask web` still shows the one-time login deep
  link on its terminal; an unattended deployment supplies the token instead with
  the new `-token-file <path>` flag or the `CASK_VIEWER_TOKEN` environment
  variable, and a generated token that cannot be shown is reported as such
  without its value (cli.md §4, viewer-security §5.1, §9, §11).

- The viewer mints a session only from a request it can attribute to its own
  origin: a token-bearing login (the `POST /viewer/login` form and the
  `?token=` deep link) is refused with 403 when the browser reports a
  cross-site or same-site relation, so a cross-site image, link, or navigation
  can no longer pin a victim's browser into the presenter's session. The
  documented token URL, a same-origin form, and a same-origin link still sign
  in. The CSRF token is accepted from the request body or the `X-CSRF-Token`
  header only, so a `?csrf=` query value no longer validates and the token can
  no longer be captured through access logs, bookmarks, proxies, or `Referer`
  chains.

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

[Unreleased]: https://github.com/dmundt/go-cask/compare/v1.6.5...HEAD
[v1.6.5]: https://github.com/dmundt/go-cask/compare/v1.6.4...v1.6.5
[v1.6.4]: https://github.com/dmundt/go-cask/compare/v1.6.3...v1.6.4
[v1.6.3]: https://github.com/dmundt/go-cask/compare/v1.6.2...v1.6.3
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
