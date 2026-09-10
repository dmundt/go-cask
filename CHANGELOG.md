# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The project is pre-release; the first public tag is `v0.1.0-alpha.1`
(pre-release), heading toward `v1.0.0` once the stable surface is frozen
(`versioning.md` §1).

## [Unreleased]

**BREAKING: the hash algorithm is fixed at compile time — the runtime registry is
gone.** `cas` implements exactly one algorithm (`sha256`), and an address still
carries its name, so the stored format and every object address are unchanged;
what disappears is the ability to register or select an algorithm at runtime.

### Removed

- **`cas.RegisterHash`, `cas.LookupHash`, `cas.LookupStreamHash` and the
  `cas.HashFunc` type** — there is no algorithm registry. The algorithm is
  `sha256` (`cas.SHA256`) for the whole build. The one-shot/streaming duality
  existed only to serve registered algorithms, so it is gone with them:
  `HashBytes` is the one-shot entry point, `NewHasher()` the streaming one, and
  `fs.Backend.Verify` streams through the latter without a lookup or a
  buffering fallback.
- **Every `algo` parameter that selected an algorithm**: `cas.New(raw, codec)`
  (no longer returns an error — nothing can fail), `HashBytes(data)`,
  `NewHasher()`, `NewHash(digest)`, and `gitlike.NewRepository(raw)` (also
  error-free). The `examples/api` `POST /objects?algo=` parameter and the CLI's
  `put -algo` flag go with them; the CLI's `list --algo` remains as a layout
  filter over `Backend.List(ctx, algo)`, which is unchanged.
- **`examples/artifacts/hasher.go`** — the `sha256double` custom-algorithm seam.
  The example keeps its custom `Codec[T]` seam (gzip) and now stores under
  `sha256`.

### Changed

- **An address is validated completely.** `NewHash` and `ParseHash` require a
  `sha256.Size`-byte digest (64 hex digits), since with one algorithm any other
  width cannot name a stored object; `ErrUnknownAlgorithm` now means exactly one
  thing — a well-formed address naming an algorithm this build does not
  implement (a store written by another build), which `List`/`Stats` skip rather
  than misreport.
- **Layout and walker simplifications that follow from one fixed-width digest**:
  `fs.hashPath` no longer clamps chunks (`WithFanOut` × `WithFanLevels` is
  bounded by the digest width), and `gitlike`'s `shortHash` no longer carries a
  non-truncating branch. A cycle is no longer constructible through the public
  API (an address depends on the bytes that would contain it), so the walker's
  cycle test is replaced by a keys-by-address test; the visited set remains for
  shared subgraphs.
- Docs re-aligned with the code: `cas-core.md` v40→v41 (§3.1/§3.2 diagrams,
  §4.1/§4.2 one algorithm and no registry, §4.8 `Store` without a hasher field,
  §4.9 walker note, §5 write path, §6 concurrency table, §7.1 surface, §7.2
  recipe), `library-design.md` v19→v20, `coding-guidelines.md` v13→v14,
  `defaults.md` v16→v17, `examples.md` v15→v16, `extensions.md` v6→v7,
  `operations.md` v6→v7 (algorithm migration is a format transition),
  `testing-strategy.md` v12→v13, `versioning.md` v13→v14, `AGENTS.md` v15→v16,
  `README.md`, `examples/artifacts/README.md`, `examples/api/README.md`.

## [v1.2.0] - 2026-09-10

Code audit of the `v1.1.0` tree: a full read of `cas`, the backends, caches,
codecs, CLI, viewer, the `gitlike` reference model and the examples, with fixes
for the defects it found, plus a hash-type consolidation described below. The
on-disk format is unchanged — existing stores stay readable and writable, and
every stored object keeps its address.

### Added

- **`jsoncodec.Hash`** — the JSON codec's hash *field type*
  (`cas/codec/json`), the only place that renders and validates a hash as text:
  `NewHash(cas.Hash) Hash`, `Hash() cas.Hash`, `IsZero()`, `MarshalJSON`
  (present → `"algo:hexdigest"`, absent → `""`), `UnmarshalJSON` (`""`/`null` →
  absent, else `ParseHash`). Object types declare this type for reference
  fields; the byte layer stays format-free.
- **`cas.CheckHash`** — the guard the store and every backend apply to a hash
  argument: an absent (zero) address returns `ErrInvalidHash` instead of being
  used as a store key.
- **`Validate() error` on the `gitlike` object types** (`TreeEntry`, `Tree`,
  `Commit`, `Tag`) for objects built in code: a tree entry and a tag need a
  name, a commit needs a tree, an absent reference is valid where absence is
  legal. Advisory — `Store.Put` marshals, it does not validate — except the
  tree-less commit, which `Commit.MarshalJSON` still rejects at write time.

### Changed

- **BREAKING: `cas.Hash` is a concrete value type, not an interface, and it
  carries no JSON code.** `Hash` was an interface over an unexported
  implementation, which meant object fields could never be decoded by
  `encoding/json` (it cannot allocate into an interface field) and "no hash" had
  two spellings: a nil `Hash` in the byte layer and an absent wrapper in object
  fields. `Hash` is now a struct with unexported fields and one meaning for
  absence:

  ```go
  type Hash struct{ /* algorithm + digest */ }   // zero value = absent

  func (h Hash) IsZero() bool                    // the one "no hash"
  ```

  Serialization moved out of the core into the codec that owns a wire format.
  `cas/codec/json` defines the field type object types use:

  ```go
  type Hash struct{ /* wraps cas.Hash */ }        // jsoncodec.Hash
  func NewHash(h cas.Hash) Hash                   // wrap for a field or literal
  func (x Hash) Hash() cas.Hash                   // unwrap for the byte layer
  func (x Hash) MarshalJSON() ([]byte, error)     // present → "algo:hexdigest", absent → ""
  func (x *Hash) UnmarshalJSON([]byte) error      // ""/null → absent, else ParseHash
  ```

  `cas` no longer imports `encoding/json` at all: the byte layer knows nothing
  about any wire format, and only the codec that defines one renders and
  validates hashes as text. Object types declare `jsoncodec.Hash` fields
  (`omitzero` where absence is optional), so they still contain no JSON code
  themselves.
  **Migration from v1.1.x:**
  - `var h cas.Hash` / `cas.Hash{}` now means *absent* instead of nil — replace
    `h == nil` / `h != nil` with `h.IsZero()` / `!h.IsZero()`, and `x.Equal(nil)`
    with `!x.IsZero()` (an absent address compares equal to nothing).
  - `cas.ParseHash`, `NewHash` and `HashBytes` return the zero `Hash` on error
    (unchanged in shape: still `(Hash, error)`); check the error as before.
  - Reference *fields* change type to the codec's field type:
    `Ref cas.Hash` → `Ref jsoncodec.Hash`, literals `Ref: h` →
    `Ref: jsoncodec.NewHash(h)`, slices → `jsoncodec.NewHash` per element. Reads
    that need the byte-layer type (`ResolveTree(ctx, c.Tree)`,
    `x.Equal(y)`) unwrap with `.Hash()`; `IsZero()`, `omitzero`/`omitempty`
    tags and `References()` skipping behave as before.
  - Hand-written `Hash` implementations are no longer possible — build addresses
    with `NewHash`/`ParseHash`/`HashBytes` in a registered `HashFunc`. This
    closes the hole that let an unvalidated address (e.g. a hostile algorithm
    name) reach a backend, so `fs`'s `safeAlgo` path sanitizer is gone.
  - The absent wrapper type `cas.HashRef` — added in this same cycle and never
    released — is deleted; `cas.Hash` covers both
    roles.
  - `Validate()`-style checks now read `Tree.IsZero()` on the field type.
- **Stored bytes are unchanged.** `json` output for a present reference is the
  same `"algo:hexdigest"` string, an absent optional reference is still omitted
  (`omitzero`), and an absent always-present field still encodes as `""` — so no
  object is re-addressed and no migration is required. Pinned by
  `TestStoredAddressesPinned`, `TestNoteJSONPayloadPinned` and
  `TestManifestJSONPayloadPinned`.
- **All hash JSON code is in one place: the JSON codec.** Repository-wide, hash
  serialization went from nine methods in five packages to the one field type
  above — `jsoncodec.Hash`'s marshaler/unmarshaler in `cas/codec/json` — plus
  `gitlike.Commit`'s two guards for its one mandatory-field rule (write refuses a
  tree-less commit; decode rejects a missing, empty, or null tree). `gitlike`'s
  `TreeEntry` and `Tag` carry no JSON methods at all, and the hand-written
  marshallers *and* unmarshallers are gone from `internal/test/types.go`
  (`Node`), `examples/notes/types.go` (`Note`), `examples/artifacts/main.go`
  (`Manifest`), `cas/cache/prefetch/prefetch_test.go` and
  `cas/cache/mem/cached_test.go` (`testObject`), along with the now-unused
  `hashStrings`/`parseHashes`/`HashRefs` helpers.
- **Library baseline raised to Go 1.24** (`go.mod`, `library-design.md`,
  `defaults.md`, `coding-guidelines.md`, `versioning.md`, `AGENTS.md`,
  `README.md`). Optional hash fields rely on the `omitzero` JSON tag option,
  which an older standard library silently ignores — that would change the
  stored bytes, so the floor is now enforced by `go.mod` (a consumer on Go
  1.22/1.23 gets a clear build error instead of a different wire format).
- Docs and agent instructions updated for the unified type: `cas-core.md`
  (v35→v39: §4.2 the concrete `Hash` and the single meaning of absence, §4.6 the
  JSON codec's `jsoncodec.Hash` field type, §4.12 gitlike serialization and
  advisory `Validate()`, §7.1 surface adds `CheckHash`, §7.2 object-type recipe),
  `library-design.md` v18, `coding-guidelines.md` v13, `defaults.md` v16,
  `versioning.md` v12 (records this as a one-off ratified breaking change inside
  v1 — later breaking changes still need a MAJOR with the `/v2` mirror),
  `AGENTS.md` v15 (object-type recipe step and a type-checked usage example),
  `gitlike/README.md`, `examples/notes/README.md`,
  `examples/artifacts/README.md`. A godoc `ExampleHash` in `cas/codec/json`
  shows the field pattern from `go doc`.

### Fixed

- **Viewer: the maintenance GC form was unusable.** `templates/gc.html` shipped
  no CSRF field, so every submit was rejected with 403 even though the handler
  passed a token; the form now carries it.
- **Viewer: a template error left a half-written 200.** `render` executes into a
  buffer and answers 500 on failure instead of streaming a partial page.
- **Viewer: an empty token could authenticate.** `resolveToken` rejects an empty
  submission and compares tokens in constant time; `cask web -tokens` skips
  empty token pairs (`-tokens "admin="` no longer creates a `""` key).
- **Viewer: login-throttle TOCTOU and missing backoff.** The budget check and
  the attempt record now share one critical section, an exhausted budget blocks
  for an exponentially growing backoff (capped at 30 min), and stale per-IP
  state is swept so a rotating caller cannot grow the map without bound.
- **Viewer/CLI: type sniffing failed for large objects.** The TLV header is
  parsed without requiring the payload, so objects larger than the 256 KiB
  preview limit report their type; the object page shows the real size from the
  backend, and the raw view marks a truncated preview.
- **CLI:** the global `-store` is honored by `cask web` (it was dropped, so the
  viewer served `./objects`); `cask meta` reports the real object size instead
  of the bounded read length; `cask list -limit`/`-offset` reject out-of-range
  values (exit 2) instead of silently clamping; `put` implements the documented
  `-json` output.
- **`lru.Cache`:** `Clear`/`Evict`/`EvictKey` drop the recency bookkeeping, so
  cleared entries no longer retain objects or cause phantom evictions.
- **`CacheMetrics.Loads`** was exported but never incremented; `CachedObject.Load`
  now counts its store fetch.
- **`cas`:** `Put` rejects an object whose `Type()` is empty (it previously wrote
  an envelope that `Get` could never read); payload-decode errors keep their
  cause (`%w`); `Walker.Walk` uses an explicit stack plus a visited set, so a
  cyclic or deeply nested graph terminates instead of exhausting the goroutine
  stack; `RegisterHash` validates the algorithm name (`^[a-z0-9]+$`) and drops a
  stale streaming hasher, keeping `HashBytes`/`Store`/`Verify` in agreement.
- **fs backend:** `Clean` reclaims the `<hex>.tmp.<n>` collision fallbacks it
  missed, returns walk/removal errors, and tolerates a missing base; `Put`
  observes cancellation while streaming and treats an existing regular file as
  idempotent success (Windows rename-over-open); `Get` retries briefly while a
  concurrent rename makes the file unopenable; `hashPath` maps a non-conforming
  algorithm name to a safe in-root element.
- **mem backend:** `WithMaxSize` bounds buffering, so an oversized `Put` is
  rejected without allocating past the cap.
- **gitlike:** `Commit.MarshalJSON` returns an error for a nil tree instead of
  panicking on the nil `cas.Hash` interface.
- **examples:** `examples/artifacts` no longer panics on `-store <dir>` with no
  command; `examples/api/server` guards its size map with a mutex and verifies
  objects by streaming the hash (it previously hashed only the first 1 MiB, so
  every intact object above that was reported invalid).
- **benchmarks:** the FS `Put` benchmark varies 8 bytes of content per
  iteration instead of 2, so its hashes no longer repeat every 65536 iterations.

### Changed

- Docs re-aligned with the code: `cas-core.md` (v34→v35: `RegisterHash`
  validation, walker visited set, `CacheMetrics.Loads` semantics, fs
  rename/`Clean`/listing scope, mem buffering), `cli.md` (v12→v13: `gc`/`prune`
  grace default is 1h — the text said 24h while the code and `defaults.md` said
  1h — plus the exact `-json` shapes), `backend-architecture.md` (v14→v15) and
  `versioning.md` (v9→v10: grace default), `cas/store.go`'s stale pre-TLV
  on-disk-format comment, and `cas/backend.go`'s integrity note (bytes are not
  re-hashed on read unless `Verify` runs).
- `internal/index.Paginate` computes its window without overflow; callers
  validate the bounds first.
- Tests added: backend cancellation and size bounds, fs concurrency (`-race`),
  walker cycle/shared subgraph, hash `Equal` across algorithms, legacy envelope
  round-trip, cache `Loads` and LRU bookkeeping, and viewer CSRF/throttle/
  large-object coverage.

## [v1.1.0] - 2026-09-09

Minor release: an **additive viewer feature** plus docs/tests/CI work. No change
to the public `cas` core API, its semantics, or the on-disk format.

### Added

- **Viewer direct-token login.** `GET /viewer/?token=<startup-or-role-token>`
  establishes the same session cookie as `POST /viewer/login` (the `cask web`
  "open viewer" deep link). It is throttled and audit-logged like a form login,
  is never logged/echoed, and its response sends `Referrer-Policy:
  no-referrer` so the token cannot leak via `Referer`. When unauthenticated,
  `/viewer/` now redirects (303) to `/viewer/login`; data endpoints still
  return 401/403 empty. Specs (`viewer-security.md` v5→v7, `viewer-design.md`
  v9→v10) updated to authorize and document this behavior.
- **Scale economics probe** `BenchmarkScaleStoreEconomics`
  (`benchmarks/scale_bench_test.go`): reports the FS on-disk layout cost at N
  (object files, dirs, leaf-dir spread, bytes) at `(2,1)` vs `(4,1)`.

### Changed

- `docs/specs/performance.md` (v12→v13): the on-demand and `CASK_SCALE_OBJECTS`
  run commands now point at `./benchmarks/` (the suite moved there), fixing
  commands that previously targeted `./cas/`.

## [v1.0.2] - 2026-09-09

Patch release: documentation and CI additions on top of the frozen `v1.0.0`
surface. No change to the public `cas` API, semantics, or the on-disk format.

### Docs & tests

- Added runnable, `// Output`-verified godoc `Example` functions for the `cas`
  core and `gitlike` (package-level and per-symbol: `ExampleHashBytes`,
  `ExampleRepository`), so `go test` keeps the documented examples correct.
- Improved the `MarshalJSON`/`UnmarshalJSON` doc comments on the gitlike
  object types (they note they implement `json.Marshaler`/`json.Unmarshaler`
  and document nil behavior).
- Reverted `docs/specs/index.md` and `docs/design/index.md` to their
  pre-condensation originals.

### CI

- Added a nightly workflow (`.github/workflows/nightly.yml`) that runs each
  fuzz target for 60 s and the regular benchmark suite — the long-running
  checks the testing-strategy spec expects beyond the CI smoke.

## [v1.0.1] - 2026-09-09

Patch release: documentation and test-only additions on top of the frozen
`v1.0.0` surface. No change to the public `cas` API, semantics, or the on-disk
format.

### Docs & tests

- Expanded the package doc comments for `cas/cache/{mem,lru,prefetch}` and
  `cas/codec/{json,gob}` (godoc), and added a runnable, `// Output`-verified
  `Example` per package so `go test` keeps the documented examples correct.
- Fixed two remaining references to the old `docs/instructions` spec path
  (`cas/errors.go`, `docs/specs/AGENT.md`).

## [v1.0.0] - 2026-09-09

**First stable release.** The `cas` stable surface (cas-core §7.1) is frozen:
semver is now `v1.x.y`, and within a major version only additive, non-breaking
changes are allowed (library-design §5). This release reaches the versioning.md
§6 Definition-of-Done: every spec's acceptance checklist is fully ticked, the
CI gates (race, ≥90% coverage, fuzz smoke, doc-integrity) hold, and the four
runnable examples plus the `gitlike/` shared reference library and the embedded
viewer are in place.

### Added

- Fuzz targets `FuzzPathRoundTrip` and `FuzzVerify` (fs backend) and
  `FuzzCodecRoundTrip` (JSON codec, restricted to valid UTF-8). The CI fuzz
  smoke now runs each target against its real package — previously three of
  the four runs targeted `./cas/` where the targets did not exist, so they
  were vacuous.

## [v0.3.0] - 2026-09-09

Layout, documentation, and stale-code release. No change to the generic `cas`
core API or the on-disk format.

### Changed

- `gitlike` moved out of `examples/` to the module root: imported as
  `github.com/dmundt/go-cask/gitlike`, and declared a **reference / copy-source**
  shared library — importable for convenience but NOT part of the stable `cas`
  surface (cas-core §7.1); apps with their own object model copy the pattern and
  never extend gitlike. Its "example" framing was removed across the docs and
  comments.
- `benchmark/` renamed to `benchmarks/` (test package path
  `github.com/dmundt/go-cask/benchmarks`); referring docs/comments aligned.
- Docs/code stale claims fixed: `sha1` no longer described as a built-in hash
  (only `sha256` ships; others via `RegisterHash`); removed nonexistent
  `repo.go`/`manifest.go` and old JSON-envelope references from the examples.

### Fixed

- Type sniffing from stored bytes now reads the **TLV** envelope instead of
  the pre-TLV JSON envelope: `internal/index.EnvelopeType` (used by the viewer
  and `cask meta`) and the `examples/api` server's `envelopeType` now call
  `cas.EnvelopeFromBytes`. A legacy unversioned type name reads back as `@1`.
  Removed an obsolete local JSON envelope struct and orphaned `Deserialize`
  comments in `gitlike/types.go`.

## [v0.2.0] - 2026-09-09

Documentation and editorial release. **No public API or on-disk-format change.**

### Changed

- Condensed every file under `docs/` (27 specs/design docs + the three folder
  `AGENT.md` meta-guides) and all repo READMEs (`README.md`, the `benchmark`
  guide, and the `examples/*` READMEs) while preserving 100% of requirements,
  contracts, invariants, and compatibility notes; mermaid/fence blocks kept
  balanced. Instruction-doc frontmatter versions bumped by one per file
  (`benchmark/README.md` v4→v5); `cas-core` now v31.
- Swept stale pre-refactor identifiers from the example READMEs and a few
  source doc-comments: `FSRawStore`→`fs.Backend`, `RawStore`→`cas.Backend`,
  `JSONCodec[T]`/`GobCodec[T]`→`json.New[T]()`/`gob.New[T]()`,
  `StoreStats`→`cas.Stats`, `Encode`/`Decode`→`Marshal`/`Unmarshal`. Comment-
  only; no behavior change (4 `.go` files, `gofmt`/`go vet` clean).

## [v0.1.1] - 2026-09-09

The `cas.Backend` contract gained a `Stats` method returning the shared
`cas.Stats` summary (renamed from the fs-local `StoreStats`); the memory
backend implements it, and both backends carry a compile-time interface
assertion. Public API and on-disk format are otherwise unchanged.

### Performance

- Core hot-path allocation reduction (on-disk format and public API
  unchanged): `marshalEnvelope` now writes the TLV envelope into a single
  pre-sized allocation (no growing `bytes.Buffer`, no final copy), and the
  internal envelope parser returns the payload as a zero-copy slice of the
  read buffer instead of allocating `type`/`payload` copies
  (`cas/envelope.go`). `Store.Get` uses the zero-copy parser; the public
  `EnvelopeFromBytes` still returns an independent payload copy. The memory
  backend `Put` stores its `io.ReadAll` buffer directly instead of making a
  second copy. Measured (64 B object): `Store.Put` 13→11 allocs, `Store.Get`
  13→10 allocs, B/op and ns/op down (e.g. `Store.Get` 1 KiB 3.7 µs→2.4 µs).

### Added

- `Backend.Stats` is now part of the `cas.Backend` interface, returning the
  shared `cas.Stats` type (`cas/stats.go`) so the fs and memory backends report
  the same summary interchangeably. The memory backend (`cas/backend/mem`)
  gained `Stats(ctx)`, recomputing per-algorithm counts, total bytes, and
  object count from its object map (no desynchronized counter). Both backends
  carry a compile-time `var _ cas.Backend` assertion so a dropped method breaks
  their own package's build. Spec/docs updated to the six-method backend
  contract.

### Changed

- `cas.StoreStats` renamed to `cas.Stats` (`cas/storestats.go` →
  `cas/stats.go`); it stays in `package cas` (it is the `Backend.Stats`
  return type, so it cannot live in `cas/backend` without an import cycle).
- Both backends carry a compile-time `var _ cas.Backend = (*Backend)(nil)`
  assertion so a dropped method breaks the backend's own package build.
- Added unit coverage for `cas.Stats.String()`.

## [v0.1.0] - 2026-09-08

This release restructures the public API and the on-disk format ahead of
`v1.0.0`. **It is a breaking release**: the byte-layer storage contract was
renamed (its former name in the `cas` root package no longer exists) and
re-homed under a pluggable `cas/backend` package, codecs moved to `cas/codec/*`
subpackages with their serialization methods renamed to the standard
`Marshal`/`Unmarshal` idiom, the caching layer was split into `cas/cache/*`
subpackages, and stored objects switched to a versioned TLV envelope. No
migration path is provided — data written by earlier alphas is incompatible.

### Added

- `cas/backend` (`cas/backend/config.go`): the generic `type Option func(any)`
  so each backend owns its own config and `With*` helpers; backend config and
  option plumbing moved out of the `cas` root package.
- `cas/backend/fs` (package `fs`): type `Backend`, `fs.New(base, opts...)`,
  the `WithFanOut`/`WithFanLevels`/`WithDirSync` options, and `StoreStats` —
  the filesystem backend.
- `cas/backend/mem` (package `memory`): type `Backend`, `mem.New(opts...)`,
  and `mem.WithMaxSize(n)` to cap total stored bytes (`0` = unbounded) — the
  in-memory backend.
- `cas/codec/json` (package `json`) and `cas/codec/gob` (package `gob`):
  `json.New[T]()` and `gob.New[T]()`, each exposing `Marshal`/`Unmarshal`; a
  stdlib `encoding/gob` binary codec joins the relocated JSON one.
- The versioned **TLV envelope** as the stored-object format
  (`[version u8][uvarint typeLen][type][uvarint payloadLen][payload]`, in
  `cas/envelope.go`, cas-core §8 decision 1) with the public accessor
  `cas.EnvelopeFromBytes`; a missing `@major` reads as `@1` and the leading
  version byte future-proofs the format.
- `cas/cache/mem` (package `memory`: `CachedStore`, `CachedObject`,
  `New(store)`), `cas/cache/lru` (package `lru`: `New(store, maxSize)` LRU
  eviction over a `memory.CachedStore`), and `cas/cache/prefetch` (package
  `prefetch`: `NewSmartCache`) — the caching layer split into three packages,
  with `SmartCache` factored out of the mem cache.
- A top-level `benchmark/` directory (`bench_test.go` + `scale_bench_test.go`)
  hosting the suite moved out of `cas/`; the on-demand scale probes honor
  `CASK_SCALE_OBJECTS`.
- `internal/test/` shared test types/fixtures reused across packages.
- `docs/index.md` (the path→spec rule index, read-first per AGENTS.md) plus
  per-directory `index.md` and `AGENT.md` governance files under `docs/`,
  `docs/design/`, and `docs/perf/`.
- All docs frontmatter converted to OKF format; a constructor-naming rule
  (`New()` vs `NewType()`) documented in `docs/AGENT.md`.
- CI now gates each `cas/backend/*`, `cas/cache/*`, and `cas/codec/*` package
  individually at ≥ 90% coverage and runs the doc-integrity gate against the
  new `docs/specs/` home.

### Changed

- **The byte-layer storage contract is now the `Backend` interface** (package
  `cas`, `cas/backend.go`), with concrete implementations living in the
  `cas/backend/*` subpackages — a breaking rename of the core interface.
- **`Codec[T]` serialization methods renamed to the standard
  `Marshal`/`Unmarshal` names** used across the `encoding/*` packages — a
  breaking API change.
- **Stored-object serialization switched to the versioned TLV envelope** — a
  breaking change to on-disk bytes and therefore to the content hashes.
- Backend constructors/options and codecs moved out of the `cas` root package;
  callers now use `fs.New`/`mem.New`/`json.New[T]`/`gob.New[T]` with the
  `cas/backend` `Option` type (see Added).
- `Store[T]`'s marshal/read paths rebuilt around the codec as the single
  serialization authority plus the TLV envelope; reads verify the stored type
  name against the decoded value's type.
- The caching layer moved from the `cas` root package into `cas/cache/*`
  (memory/LRU/prefetch split) with `SmartCache` promoted from `examples/notes`
  to `cas/cache/prefetch`.
- Examples (`files`, `notes`, `artifacts`, `gitlike`), `cmd/cask`, and the
  viewer updated to the new API; codec wrappers now compose `json.New[T]`
  with `Marshal`/`Unmarshal`.
- Tests reorganized per package (cached/LRU/smartcache split; `cas` corner and
  external tests distributed to their owning packages) and coverage lifted
  across the tree (mem backend ~97%, fs backend ~90%, `cas` ~95%, `cas/codec`
  ~92%); shared test types centralized in `internal/test/`.
- `examples/files`'s `main` refactored for testability with error-path and
  command-execution tests added.

### Removed

- The pre-TLV `type\npayload` serialization and the earlier JSON envelope,
  both superseded by the versioned TLV envelope.
- The in-`cas` byte-layer backend and codec files/constructors that the
  `cas/backend/*` and `cas/codec/*` packages replace.
- Stray generated coverage artifacts (`cachecover`, `cas/cache/mem/cover.out`).

### Fixed

- Full benchmark suites that were dropped when benchmarks moved to
  `benchmark/` were restored.
- `examples/files`'s `cat` now resolves blobs to raw bytes and other types to
  a description.
- Doc-integrity path updated to the `docs/specs/` home and all docs swept to
  the current API after the refactors; README Mermaid diagram parse error
  fixed; `envelope.go` consistency fixes and TLV doc alignment.

## [v0.1.0-alpha.2] - 2026-09-03

### Added

- Core-overview diagram of the `cas` interfaces (byte / typed / caching
  layers and their dependencies) — canonical copy in `cas-core` §3.3, with
  the same diagram embedded in the README; `design/core-overview.md` points
  to the spec (cas-core v13, v14).

### Changed

- Audit decisions (core lib, 2026-09): prune now defaults to --min-age 24h (was 1h) with the forced --min-age 0 warning and a required root argument, matching gc/cli/consistency; FSRawStore.Size takes context.Context first (every I/O method does); new optional WithDirSync FSOption fsyncs the parent directory after the publish rename (best-effort, no-op on Windows, operations §1); lean-core budget re-baselined to ≤ ~1600 LOC / ≤ ~40 exports with the full stable surface enumerated (library-design v10); library baseline declared Go 1.27 (defaults v10, versioning v5, AGENTS v10, README). Checklist ticks: cli/prune grace item, operations fsync item, library-design budget item (cli v10, operations v5 unchanged).
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

- Audit decisions (core lib, 2026-09): prune now defaults to --min-age 24h (was 1h) with the forced --min-age 0 warning and a required root argument, matching gc/cli/consistency; FSRawStore.Size takes context.Context first (every I/O method does); new optional WithDirSync FSOption fsyncs the parent directory after the publish rename (best-effort, no-op on Windows, operations §1); lean-core budget re-baselined to ≤ ~1600 LOC / ≤ ~40 exports with the full stable surface enumerated (library-design v10); library baseline declared Go 1.27 (defaults v10, versioning v5, AGENTS v10, README). Checklist ticks: cli/prune grace item, operations fsync item, library-design budget item (cli v10, operations v5 unchanged).
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
