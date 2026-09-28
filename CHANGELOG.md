# Changelog

Notable user-facing changes only. Routine maintenance, test-only changes,
internal refactors, and release preparation are omitted.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Envelope header census — `cas.PeekHeader` reads a frame's version, codec tag and type name in one
  pass; `cask list -type`/`-codec` and `-json` objects carry `type`/`version`/`codec`; `cask meta` and
  `cask stats -json` report them per type, version and codec, and the viewer's filters and URLs too.
- Census facts — each axis sums to the object count minus `unreadable` and `headerless` (the raw
  objects `put` writes); a frame with no codec tag reads `unspecified`, never blank; every read is
  header-only.
- `cas.EncodeEnvelope(codec, typeName, payload)` — the envelope writer `Store.Put` frames through,
  for producing stored bytes without a store.
- `cask seed-preview` — format-version 2 frames tagged `json`, from deterministic JSON payloads
  encoded by `cas/codec/json`; `cask get` round-trips a seeded object and `cask list -codec`,
  `cask meta`, `cask stats` and the viewer report the format really there.
- **`cask seed-preview` — breaking:** every seeded address changes (payload and tag live inside the
  frame). Migration: re-run `cask seed-preview`; old v1 objects stay beside the new ones and the
  viewer finds no graph in the old set — `cask gc` them.
- `cask seed-preview -hash-algo sha256|sha512|sha512_256` (default `sha256`) — seeds the preview
  graph under the algorithm the viewer reads it with.
- `cas/verify/sidecar` — an optional per-object checksum record at `<base>/.meta/<hex>.json`
  (`crc32`, `adler32`, `crc64`); the address is untouched, the record is never hashed.
- `cask verify --checksums [--checksum <algo>]` reads the records; `cask gc`/`cask prune` reconcile
  them; an object with no record is unchecked, never corrupt.
- `cas.CapabilitiesOf` — the optional maintenance operations a backend supports (`Cleaner`,
  `Statter`).
- `cas.VerifyAll`/`cas.Sweep` — integrity check and mark-and-sweep reclamation for every backend,
  including `packfs`, which has no native GC; `fs.Backend.Verify`/`GC`/`Prune` stay the faster path.
- `cas/repo` — `Registry` maps a `cas.Digest` to its typed object; `Walk` visits each reachable
  object once; `Reachable` builds the cross-type root set; `LookupStore[T]` returns the typed store
  registered under a name (`*UnknownTypeError`, not a nil store).
- `cas/refs` — named mutable `cas.Digest` pointers: atomic `Set`/`Delete`, `Get`/`List`/`Resolve`
  with ambiguous-prefix detection, a reflog per name (`Log`/`Previous`), `Roots`, and `ValidateName`.
- `cas.Reachable` — the transitively-closed reachable set from root digests, via a `cas.RefLister`.
- `cas.CodecNamer` — a codec declares its wire tag (`json`, `gzip+json`, `""` for none).
- `cas.ErrCodecMismatch` — reading an object written with a different codec: the tag is written into
  every envelope and compared before decoding.
- Stored envelope version 2, adding a codec identity to every frame —
  `[version u8][uvarint codecLen][codec][uvarint typeLen][type][uvarint payloadLen][payload]`; version 1
  objects (no codec field) still load and read as codec unspecified, and writers converge on version 2.
- `gob.NewRaw[T]()` — a gob codec with no inner codec; `gob.New[T](next)` takes the inner codec.
- `flate`, `gzip` and `zlib` export `MaxDecodedBytes` and `ErrDecodedTooLarge`;
  `bloom/persistent.Filter` exports `IsMapped` (false on Windows).
- `lru.Cache.CachedStore()` — the wrapped store, without touching recency bookkeeping.
- `cas.GetMany` — a digest batch in one call, with the optional `cas.BatchGetter` backend interface
  (`packfs` opens each pack once); a non-batching backend falls back to sequential `Get`.
- `Store[T].GetReader(ctx, digest) (io.ReadCloser, error)` — raw bytes streamed to the caller, who
  closes the reader; same guards and backend `Get` as `Store.GetRaw`, without buffering.
- `cas.PhysicalStatter` — `Stat` returns an object's size and modification time from one physical
  read, beside `cas.Statter`'s two calls; `fs` and `packfs` implement it.
- `cas.PutStream` — spools a raw stream while hashing it, then deduplicates and stores it (the CLI
  `put` and the `examples/api` upload).
- `gitlike.Resolver` satisfies `cas/repo.Resolver` (`Resolve(ctx, d)`), so `cas/repo.Walk` and
  `cas/repo.Reachable` run over a gitlike repository; `Repository.Close` releases the backend.
- `fs.CleanTemp(ctx, root, olderThan) (int, error)` — the temp-file sweep behind `fs.Backend.Clean`
  (`<name>.tmp`/`<name>.tmp.<n>`), returning the removed count; `fs.CleanupTemp` fixes the age at 0.

### Changed

- `cask -backend fs|packfs` (default `fs`) — the storage backend for every store subcommand, so
  `verify`, `gc`, `prune` and `clean` maintain a packed store too; `cask web` requires `fs`; every
  command closes its store.
- `cas.ErrUnsupported` — an operation a backend cannot perform, naming the operation and the backend.
- `-store` is resolved once when the store is opened; `clean`, `gc` and `prune` print the
  resolved base (`clean: store <dir>`), `cask web` logs it, and the maintenance lock is taken there.
- `cask web` startup admin token — 128 bits (16 bytes, four dash-separated groups of eight hex
  characters); a supplied token (`-token-file`, `CASK_VIEWER_TOKEN`) must be a regular file within
  4 KiB and at least 16 characters from `A-Z a-z 0-9 - . _ ~`; a rejection never echoes the value.
- Viewer sessions retain at most 50 000 verification results — past the bound a dropped object reads
  `Unverified`, not a stale verdict.
- Viewer frame-version label — `Envelope` in the object table column header and the inspector, value
  `vN` (`v1`, `v2`); the `version` filter and `?version=` stay decimal.
- `cask web -bind` — a numeric loopback: `localhost:8080` listens on `127.0.0.1:8080` and prints
  that origin; `:8080` is refused like `0.0.0.0:8080` unless `-allow-insecure-bind` is set.
- `cask web` login hint prints to stdout; `-show-token` forces it, `-show-token=false` never shows
  it, an absent flag keeps the interactive heuristic.
- `examples/api` — bodies refused with `413` before they are read (objects 64 MiB by default, `-max-size`;
  `/gc` 8 MiB); `ReadTimeout` 60 s, `WriteTimeout` 5 min, `IdleTimeout` 2 min; `-tokens` has no
  default; `-store` names the root, `-trusted-proxy` enables `X-Forwarded-For`.
- `packfs.Clean` sweeps `<base>/packs` through `fs.CleanTemp` — a name outside the convention
  (`notes.tmp.old`, `name.tmp.extra`) is not deleted from `packs/`; `.put-*.tmp`, `index.json.tmp` and
  every `<hex>.tmp`/`<hex>.tmp.<n>` still are.
- `flate.ErrDecodedTooLarge`, `gzip.ErrDecodedTooLarge` and `zlib.ErrDecodedTooLarge` are one value
  behind three names: `errors.Is(err, gzip.ErrDecodedTooLarge)` holds for `flate` and `zlib` too.
- **`cas/pack` — breaking:** helpers take a `codec` and `context.Context`; `Save`/`Load`/`Encode`/`Decode`
  become `SaveWith`/`LoadWith`/`EncodeWith`/`DecodeWith` (codec last) and `pack.New(path, codec)`; a nil
  codec is `pack.ErrNilCodec`. Migration: pass a context and a codec.
- gitlike resolves a type from the **versioned** envelope name (`cas.EnvelopeType`, header prefix):
  `blob@2` is an unknown type; an absent major version still reads `@1`.
- `examples/artifacts` stores through `gzip.New(json.New[T]())`: new objects record the `gzip+json`
  codec identity, so their digests change; existing stores keep reading.
- `examples/files` and `examples/artifacts` keep named pointers outside the object store: the root
  holds `objects/` (the `fs` base) and `refs/` (`cas/refs`). Migration: move existing objects there.
- `fs.Backend.Prune` takes an already-expanded `reachable map[string]bool`, matching `fs.Backend.GC`;
  the `cask` CLI still requires every digest that must survive in `<roots...>`.
- Backend options are typed per backend (`fs.Option`, `mem.Option`, `packfs.Option`); an option
  built for another backend is a compile error.
- `bloom.NewGuard` reports nil arguments as an error; the advisory filter contract is the exported
  `bloom.Filter`.
- `bloom.Parameters` returns an error for a filter past the documented ceiling.
- `bloom.Guard.Exists` rejects an absent digest with `cas.ErrInvalidDigest`.
- `bloom/persistent.Filter.Close` is idempotent; a closed filter's `Contains` reports false.
- `memory.CachedStore.Preload` and `Warmup` report every failure joined with `errors.Join`; `OnNew`
  may be installed at any time.
- `lru.Cache` no longer embeds `memory.CachedStore`; the wrapped store is reached through
  `CachedStore()`.
- `fs.New` and `packfs.New` reject an empty or whitespace base, `.`, the filesystem root, a volume
  root (`C:`) or a parent-traversal path (`..`, `../store`); a nested directory is still valid.
- `gitlike.NewPreloader`, `fs.EnsureBase` and `fs.CleanupTemp` take a `context.Context`.
- Library baseline — Go 1.24.0.
- Viewer reachable state `Head` is renamed `Root` (`reach=root`, the `Root` pill, `objectRow.Root`).
- `cask stats` and a filtered `cask list` walk the store once, not twice.

### Removed

- `bloom.Indices` removed, with `bloom.Parameters` and `bloom.ResolveIndexHash` unchanged;
  `cas/bloom` is outside the frozen surface (cas-core §7.1), so the removal ships in `v1`.

### Fixed

- `cas/bloom/counting`'s `CounterBits` packs its counters and selects the width it documents, capped at
  `counting.MaxCounterBytes = bloom.MaxBits/2` (2 GiB), past which comes `ErrFilterTooLarge`.
- `cas/codec/cbor` returns `cbor.ErrIntegerRange` for an argument above `MaxInt64` on major type 0 and 1;
  `MaxInt64` and `MinInt64` still decode.
- `cask put` and `cask get` honor the `--` end-of-flags marker: `cask put -- -json` stores a
  dash-named file as data.
- `cas/verify/sidecar` reports every unusable record as `cas.ErrCorrupt`, so `errors.Is(err,
  cas.ErrCorrupt)` holds for an unreadable record too; the I/O error stays on the chain.
- `cas/verify/sidecar` cannot write a record its own reader refuses: one past the read cap drops its
  optional `type`/`codec` fields, and one that still does not fit is `sidecar.ErrRecordTooLarge`.
- `cas/verify/sidecar` reports a damaged record in `VerifyReport.Unreadable` while `VerifyAll` checks the
  rest; `cask verify --checksums --all` prints `RECORD UNREADABLE <hash>: ...` on stderr and exits 1.
- `cas/verify/sidecar` `Keys`/`Reconcile` report a foreign `.json` name in `.meta` in
  `ReconcileReport.Foreign`, so it cannot abort `cask gc`/`cask prune` reconciliation.
- `cas/codec/cbor` decodes half-precision floats (major 7, additional information 25) as IEEE 754
  half precision, subnormals, signed zeros, infinities and NaN included; no digest changes.
- `cask web -backend packfs` fails with the operation, the backend and the remedy (a loose store via
  `-backend fs` or `-store`).
- `cask verify` takes `-hash-algo` (`sha256|sha512|sha512_256`): `cask verify -hash-algo sha512 <hash>`,
  `--all` and `--checksums` all use it, and an unknown name is a usage error. The other local
  subcommands still speak `sha256`.
- `cas/codec/cbor`'s `New` reports an inner codec combined with `encode`/`decode` conversion functions
  instead of ignoring it: `New(next, nil, nil)` delegates, `New(nil, encode, decode)` owns the CBOR conversion.
- A `cas/pack` manifest write is atomic (temp file, fsync, rename), so a crash or a full disk leaves
  the previous manifest intact.
- The portable sweep (`cas.Sweep`, the `cask -backend packfs gc|prune` path) asks the backend which
  digest-named files it can address and skips the rest instead of failing with `ErrInvalidDigest` after a
  partial reclaim; a dry run reclaims the same set as a real run.
- `cas/bloom/persistent` keeps its hint set across processes: the file's index key derives the default
  Bloom index hash; an older file, or one under another index hash, is rebuilt empty.
- The viewer's login throttle is proxy-aware: `cask web -trusted-proxy <ip|cidr,...>` names the proxies
  whose forwarded client address (`X-Forwarded-For`, RFC 7239 `Forwarded`) it believes, so clients behind
  a proxy get a bucket each; with none configured — the default — the direct peer keys the throttle,
  where five failed logins lock everyone out for 30 minutes.
- The viewer classifies verification like `cask verify --all`: only a digest mismatch is `corrupt`;
  a missing or unreadable object is unverified (`Missing`/`Unreadable`).
- `examples/files` writes no `*.crc32` sidecar beside an object; integrity is the core's
  `Verify`/`VerifyAll`.
- `seed-preview` drops only a swept (Detached) object's own edges, so later blocks keep their roots
  and the viewer does not show their reachable objects as orphaned.
- `examples/api` reports an object's size from the backend's physical metadata (`cas.Statter`), so
  `list`/`meta` no longer report `size: 0` for objects the running process did not write.
- Snapshot restore derives its payload buffer from the bytes actually present, so a crafted archive
  header cannot demand an enormous allocation.
- Cache prefetching is bounded in concurrency, visits each digest once, and is cut short only by its own
  `prefetchTimeout`.
- `flate`, `gzip` and `zlib` stop inflating at `MaxDecodedBytes` (1 GiB) and return
  `ErrDecodedTooLarge`.
- `fs.ValidateBase` rejects parent-traversal roots (`..`), which let `CleanupTemp` delete files
  outside the store.
- CLI runtime store failures exit 1; usage errors exit 2.
- `cask list` rejects a surplus operand with a usage error (exit 2) instead of printing the store.
- `Store.Close` is idempotent, tolerates a nil receiver and a store with no backend, and returns the
  backend's close error to every caller.
- `fs.Backend.Stats` returns what was there when an object vanishes mid-walk (a concurrent `Delete`),
  instead of an `lstat` error.
- The `packfs` pack index survives a restart: manifest keys are hex, so a packed store reopens with its
  real digests; an index written by an older build is dropped on load (the loose tree keeps every object).
- A truncated or otherwise unreadable envelope is `cas.ErrCorrupt` from every reader (`Store.Get`,
  `EnvelopeFromBytes`/`EnvelopeType`, `PeekType`, `cas/repo.Registry.Resolve`); `cas.ErrUnknownType` means only
  an intact object naming an unhandled type.
- `mem.WithMaxSize(math.MaxInt64)` stores the bytes it was given and reports success; every smaller
  cap is unchanged.
- `cas/codec/cbor` returns an owned byte string: a decoded `[]byte` field is a copy, not a sub-slice
  of the object buffer.
- `cas/backend/packfs` retries a transiently refused index publication; the on-disk index format is
  unchanged.
- `gitlike` resolution reads only the envelope header and reports a failed close.
- The viewer's single-object verify reports a failed reader close.
- The viewer's object browser returns 500 when the store-wide metadata snapshot fails.
- Malformed CBOR payloads (oversized or overflowing declared lengths) return an error.
- The `examples/api` GC handler does not race on its in-memory size index and does not panic when
  collecting stats fails.

### Security

- The backend write path refuses a symbolic link planted inside a store base: `fs.Backend.Put` on the
  way to an object, `packfs` at `<base>/packs`, `<base>/loose` or `<base>/packs/current.pack`; each
  refusal is `fs.ErrUnsafeTarget` and leaves the link's target untouched.
- The pack index scratch file is an exclusive random `index-*.tmp` per rewrite, not the fixed
  `index.json.tmp`.
- `cas/bloom/persistent` checksums the index key in its file header: the seven reserved bytes carry
  a checksum scheme byte plus a six-byte CRC-64/ECMA over `kind || key`, and a header whose scheme or
  checksum does not verify is rebuilt, never indexed.
- `Filter.Rebuilt()` reports that a reopen discarded the stored bits (missing, truncated,
  foreign-kind, failed-checksum, or a file the call just created).
- **On-disk layout change:** a `cas/bloom/persistent` file from an earlier build has zero reserved
  bytes and is rebuilt on first open — a lost hint set, never a wrong answer — then rewritten.
- The viewer's login token is read from the `POST /viewer/login` form body only; `?token=` no longer
  authenticates; the `GET /viewer/?token=` deep link is unchanged.
- An authenticated viewer session cannot monopolize the server: `POST /viewer/objects/verify` runs one
  sweep at a time, a session may start three and then one per 5 seconds, and past that it answers `429`
  with `Retry-After`.
- A viewer bound to a non-loopback address never displays its generated startup token or a `http://`
  login link, whatever `-show-token` asks for; it prints the bind and the `https://` expectation.
- The login deep link percent-encodes its token, and the Windows launch opens the URL through
  `rundll32.exe url.dll,FileProtocolHandler`; a token holding `&`, `#`, `%` or a space now logs in.
- The viewer's HTTP responses: a throttled login answers `429` with the enforced `Retry-After`, a
  rejected token `401` with no body, and every response `Cache-Control: no-store` and `Vary: Cookie`.
- `cask web` writes no startup token to the process log at any level; an unattended deployment supplies
  it with `-token-file <path>` or `CASK_VIEWER_TOKEN`.
- A token-bearing login (`POST /viewer/login`, the `?token=` deep link) is refused with `403` on a
  cross-site or same-site relation; the CSRF token comes from the body or `X-CSRF-Token` only.
- Every viewer request body is capped at 4 KiB in one middleware all routes inherit — a larger body,
  `multipart/form-data` included, is refused `413` before it is parsed.
- `cas/codec/cbor` bounds decode nesting at `cbor.MaxDepth` (128 levels) and reports
  `cbor.ErrTooDeep`, distinct from a truncation error; nested single-element arrays could exhaust the
  stack and abort the process with `fatal error: stack overflow`.
- `cask stats` and `cask meta` replace C0/C1 control characters in every header-derived string, so a
  stored object cannot rewrite the operator's terminal or forge a census line.
- A symbolic link at `-store` is resolved once when the store is opened, and `clean`, `gc`, `prune`
  and `cask web` report the directory they act on, so a planted link cannot redirect a sweep.
- `cas/codec/gob` documents the bound on decode recursion: the one payload-only path is capped by the
  standard library at 10 000 levels, so a **recursive** destination type must not be decoded from
  untrusted bytes.

## [v1.6.5] - 2026-09-22

### Added

- Viewer reference state **Head**: reachable objects with no inbound references (the entry point of
  a reachable subtree), a blue pill, `reach=head` filter.

### Changed

- Viewer status pills carry a translucent light border.

### Fixed

- Preview graph Detached classification: only the last object in each eight-object block is
  detached; the two ordinals misclassified as Detached are orphaned-with-inbound; a block's root
  ordinal is also a Head candidate.

## [v1.6.4] - 2026-09-22

### Added

- Viewer reference state **Detached**: orphaned objects with no inbound references, a violet pill and
  filter.

### Changed

- Viewer status pills: semibold weight, darker per-state text colours.
- Viewer type scale up roughly 10%.

### Fixed

- Opening the viewer on an empty store no longer fails while preview graph metadata is unavailable.
- Pack storage supports digest widths beyond SHA-256 and rejects truncated payload records.
- In-memory snapshot restore rejects trailing data.
- Pack manifests reject unsafe file locations and malformed record bounds.
- Long filesystem maintenance scans respond promptly to context cancellation.
- Typed reads and integrity verification report backend close failures.
- Snapshot imports avoid attacker-controlled up-front map allocation.
- CLI maintenance commands reject negative retention ages; `verify` rejects extra operands; viewer
  startup errors return documented exit codes.

## [v1.6.3] - 2026-09-22

### Changed

- Viewer object-list rendering scales with visible rows for the default hash-ordered view.

## [v1.6.2] - 2026-09-22

### Changed

- Viewer: denser VS Code-style workbench with flat docked panels and compact type and table rhythm.

## [v1.6.1] - 2026-09-22

### Changed

- Viewer object browsing reuses bounded metadata snapshots, reducing repeated filesystem scans.
- Viewer verification reports the mismatched digest from its existing hash pass.

## [v1.6.0] - 2026-09-22

### Added

- `cask web` accepts `-hash-algo sha256|sha512|sha512_256`; the selected algorithm is shown in object
  metadata.

### Changed

- Viewer digest parsing and verification use the configured `cas.Hasher`.
- Viewer object routes: `/dump` for the HTML byte dump, `/verify` for bulk verification.

## [v1.5.0] - 2026-09-21

### Added

- Secure viewer response headers and a restrictive content security policy.
- Viewer-wide object verification with per-object results and audit records.
- Automatic object selection, reference navigation, and inspector history.
- Build version in the viewer top bar.

### Changed

- Viewer object browsing, filtering, sorting, reachability, references and inspection consolidated
  into one object-browser workspace.
- Viewer metadata reads cached for immutable objects.

### Removed

- Viewer dashboard, garbage-collection page, delete action, and custom JavaScript; destructive
  maintenance remains a CLI operation.

## [v1.4.6] - 2026-09-18

### Changed

- Published documentation site: refined navigation, search and responsive layout.

## [v1.4.5] - 2026-09-17

### Fixed

- Hardened verification and release automation.
- Improved pack storage recovery.

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
- Git-like repositories receive codecs explicitly and enforce object invariants independently of
  serialization.

## [v1.2.0] - 2026-09-10

### Added

- Typed object stores, codecs, caching, graph traversal, and filesystem storage capabilities.

## [v1.1.0] - 2026-09-09

### Added

- Initial Git-like object model and repository APIs on top of the generic CAS core.

## [v1.0.0] - 2026-09-09

### Added

- First stable release of the generic, content-addressable storage core, filesystem backend, typed
  object layer, and Git-like reference model.

## [v0.3.0] - 2026-09-09

- Stabilized the core object, codec, backend, and repository APIs ahead of the 1.0 release.

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
