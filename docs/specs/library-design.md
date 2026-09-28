---
type: Specification
title: Library Design — go-cask
description: The lean-core contract for the cas library — exported-surface budget, the citizen classes and the dependency-layer matrix, sentinel errors with errors.Is, explicit configuration without mutable globals, API shape rules, and a compatibility policy.
version: v57
---

# Library Design — go-cask

The `cas` package must be small, obvious, hard to misuse. Related: `cas-core.md` (layering), `coding-guidelines.md` (idiomatic Go, no `any`), `performance.md` (fast paths stay simple).

## 1. Lean-core budget

- **Ceiling**: `cas/` (excluding `_test.go`) SHOULD stay ≤ ~1600 **code lines** and ≤ ~51 exported identifiers — 21 functions, 22 types, 1 exported constant, 7 sentinels.
- **Metric**: *code lines* = lines neither blank nor comment-only, across `cas/*.go` without `_test.go` (go-cask#271).
- **Measure**: `find cas -maxdepth 1 -name '*.go' ! -name '*_test.go' | xargs grep -hvE '^[[:space:]]*(//|$)' | wc -l` → **906** today; 1669 non-blank; 1765 raw.
- **Enforced against** the package's exported declarations in go-cask#191, go-cask#319 and go-cask#322.
- **Advisory** ceiling for additions, not a shrinking target. Every exported name must earn its place; one that can live in a subpackage or an example does.
- **Stable core surface** (the API docs promise — cas-core §7.1) — 51 identifiers, all in `package cas`, same list as cas-core §7.1:

| Group | Identifiers |
| --- | --- |
| Address | `Digest`, `NewDigest`, `ParseDigest`, `CheckDigest`, `Hasher` |
| Byte layer | `Backend`, `Stats`, `PutStream` |
| Codec seam | `Codec[T]`, `CodecNamer` (codec identity the store resolves once and writes into the envelope), `Object`, `Validator` (optional object invariant the store enforces) |
| Store | `Store[T]`, `New[T]`, `Walker[T]`, `NewWalker`, `WalkDigests`, `Node`, `NodeResolver` (the one graph traversal `Walker[T]` and `cas/repo.Walk` are adapters over — go-cask#319), `Reachable`, `RefLister`, `RefListerFunc` (the reachable-set expansion `Backend.GC`/`Backend.Prune` require), `BatchGetter` (byte-layer opt-in: a backend serves a batch its own way), `GetMany` (package-level batch read, sequential `Get` fallback, so every `Backend` already satisfies it — go-cask#173) |
| Maintenance | `Verify`, `Verifier`/`NewVerifier`, `VerifyAll`, `Report`, `Sweep`, `SweepOptions`, `Capabilities`, `CapabilitiesOf`, `Cleaner`, `Statter` (generic, backend-agnostic maintenance layer — go-cask#137) |
| Envelope | `Envelope`, `EnvelopeFromBytes`, `EncodeEnvelope` (the writer `Store.Put` frames through), `EnvelopeType`, `PeekHeader` (version, codec tag and type in one pass — `PeekType`/`PeekVersion` resolve the layout through the same walk), `Header` (the one bounded header read: `(ctx, backend, digest)`; shared by `cas/repo`, gitlike, `internal/index`, the viewer — go-cask#319), `HeaderType`, `PeekType` (streaming header-only peek `Store.Type` is built on), `PeekVersion`, `EnvelopeVersion` (the frame's leading version byte, readable alone) |
| Sentinels | `ErrNotFound`, `ErrDigestMismatch`, `ErrInvalidDigest`, `ErrUnknownType`, `ErrCorrupt`, `ErrCodecMismatch`, `ErrUnsupported` |

- **Everything else lives in a subpackage, never in `package cas`:**

| Tree | Exports |
| --- | --- |
| `cas/backend/fs` | `fs.Backend`, `fs.Option`, `fs.New(base, opts...)` (validates `base` via `fs.ValidateBase` first), `fs.WithFanOut`, `fs.WithFanLevels`, `fs.WithDirSync`, `fs.DefaultFanOut`/`fs.DefaultFanLevels`/`fs.MaxFanDepth`, `fs.ValidateBase`/`fs.EnsureBase`/`fs.CleanupTemp`/`fs.CleanTemp`, `Verify`/`GC`/`Prune`, `Clean`/`Size`/`ModTime` |
| `cas/backend/mem` | `backmem.Backend`, `backmem.Option`, `backmem.New(opts...)`, `backmem.WithMaxSize`, `Snapshot`/`Restore` |
| `cas/backend/packfs` | `packfs.Backend`, `packfs.Option`, `packfs.New`, `packfs.WithEnabled`/`WithPackMaxBytes`/`WithPackMaxEntries`, `GetMany`/`Close`/`Clean`/`Size`/`ModTime` |
| `cas/backend` | `WriteAll`/`ReadAll`/`ReadPayload`, `ContextReader` (its `WriteTo` is the ctx-checked, pooled-buffer copy loop `io.Copy` takes, so a small `Put` allocates no 32 KiB scratch — go-cask#368; a method on a subpackage type moves no count in the budget above) |
| `cas/backend/snapshot` | `Export`/`Import` (raw-digest archives) |
| `cas/hash/*` | `cas/hash/sha256`, `cas/hash/sha512`, `cas/hash/sha512_256`; `Hasher`, `New`, `NewHasher`, `Of`, `Parse`, `Format`, `Name`, `Size` — sha256 is the default go-cask's clients wire in; nothing in `cas` imports it |
| `cas/verify/*` | `cas/verify/adler32`/`crc32`/`crc64` |
| `cas/hash` | `FormatDigest`/`ParseDigest`/`ValidateDigestSize`; short display form from `cas.Digest.Prefix(n)`, not a client helper |
| codecs | `json.New[T]()`, `gob.NewRaw[T]()`, `gob.New[T](next)`, `binary.New[T](next, transform, restore)`, `binary.NewRaw[T](encode, decode)`, `cbor.New[T]`/`cbor.NewRaw[T]`/`cbor.NewValue`/`cbor.NewMap`, `flate.New[T]`/`gzip.New[T]`/`zlib.New[T]` |
| caches | `cache.ValidateMaxSize` (`cas/cache`), `cachemem.CachedStore[T]`/`cachemem.CachedObject[T]`/`cachemem.CacheMetrics`/`cachemem.CacheStats`, `cachemem.New(store)`, `lru.Cache[T]`, `lru.New(store, maxSize)`, `prefetch.SmartCache[T]`, `prefetch.NewSmartCache` |

- The three compression wrappers each carry `MaxDecodedBytes` and `ErrDecodedTooLarge` — three names, one shared sentinel value, one bounded body in `cas/codec/internal/bounded`. There is no `JSONCodec`/`GobCodec`/`BinaryCodec` type.
- Objects declare plain `cas.Digest` reference fields, rendered through `encoding.TextMarshaler` (`MarshalText`/`UnmarshalText`) — no hash JSON code lives anywhere.
- `package memory` is declared by two subpackages: `cas/backend/mem` (byte layer) and `cas/cache/mem` (cache). Both are imported under the canonical aliases `backmem` and `cachemem`, never unaliased and never `mem`, `memory`, `membackend` or `memcache` (go-cask#269). Both clauses are named in the frozen surface (cas-core §7.1), so changing one is a breaking change and waits for a major version. `internal/design` enforces the aliases in `go test ./...`.
- Optional machinery stays out of the core: prefetch-on-access and cache-monitor recipes live in `examples/notes` and `examples/artifacts`; record the decision in `extensions.md` §3 when made.
- `cas/refs` (never `package cas`) holds the mutable half — named, atomically-written pointers to a `cas.Digest`, with a reflog: `refs.Open(dir, opts...)`, `refs.WithClock`, `Store.Get`/`Set`/`Delete`/`List`/`Resolve`/`Roots`/`Previous`/`Log`, `Ref`/`Entry`, `Option`, `ValidateName`, `ErrNotFound`/`ErrAmbiguous`/`ErrInvalidName`.
- `cas/repo` (never `package cas`) holds the typed, cross-type registry promoted from gitlike's example `Codecs`/`Repository`/`Resolver`/`WalkGraph` pattern: `Object`, `Decoder`, `Resolver`, `Registry`/`NewRegistry`, `Register`, `RegisterStore[T]`, `LookupStore[T]` (typed, `any`-free way back to a registered store), `Walk`, `Reachable`, `UnknownObject`, `UnknownTypeError` (`Unwrap() == cas.ErrUnknownType`).
- `cas/verify/sidecar` (never `package cas`) holds the optional recorded-checksum layer: `Backend` (a `cas.Backend` decorator), `New`, `WithBase`/`WithChecksum`/`WithDirSync`/`WithMaxRecordBytes`, `Record`/`RecordVersion`/`DefaultMaxRecordBytes`, `Verifier`, `VerifyReport`, `ReconcileReport`, three sentinels. It adds no identifier to `package cas`.

| Sidecar sentinel | Distinction it names |
| --- | --- |
| `ErrUnrecorded` | "no record" vs "no object" (both `cas.ErrNotFound`) |
| `ErrChecksumAlgorithm` | a reader change vs damage (as `cas.ErrCodecMismatch` does one layer down) |
| `ErrRecordTooLarge` | a write whose record cannot fit the reader's own cap, so it never publishes a record its reader would call `cas.ErrCorrupt` |

A record already in a store that cannot be read is a finding (`VerifyReport.Unreadable`, `ReconcileReport.Foreign`), not a reason to abandon a pass (go-cask#362).

- **`gitlike` is NOT part of `cas`, and not an example**: a **2nd-class reference library at the application layer** — package `github.com/dmundt/go-cask/gitlike` in this module, shipped and coverage-gated, importable by apps and examples, deliberately outside the frozen surface above. A breaking change to it may ride a MINOR with a changelog note (the `gitlike.Codecs` change is the precedent, versioning §1). The product (`cmd/**`, `internal/**`) never imports it; `examples/**` (3rd class: teaching code with no compatibility surface) may.

### 1.1 Classes and layers

**Class** = who may rely on a change and where it is recorded; **layer** = what may import what. `internal/build/layers` carries the matrix below as data, `scripts/verify.sh` runs it as the layer-matrix check, `internal/build/depgraph` draws `gitlike` in its own `REFERENCE` layer.

| Class | Trees | Promise | Change record |
| --- | --- | --- | --- |
| 1st | `cas/**` | the frozen core surface (cas-core §7.1, §1 above): additive-compatible — a breaking change needs a major version or a recorded exception (versioning §1) | `CHANGELOG.md` |
| 2nd | `gitlike`, `cmd/cask`, `internal/**` | shipped and gated, no frozen API: a breaking change may ride a MINOR with a changelog note (the `gitlike.Codecs` change is the precedent, versioning §1); the CLI is a program governed by `cli.md` | `CHANGELOG.md` |
| 3rd | `examples/**` | none — teaching code a consumer copies, changed freely | the example's `README.md` |

Dependency layers: `cas/` → `gitlike/` → `examples/`. The product (`cmd/cask`, `internal/**`) sits *beside* the chain, never above it.

| From, may import | `cas/**` | `gitlike` | `internal/**` | `cmd/**` | `examples/**` |
| --- | --- | --- | --- | --- | --- |
| `cas/**` | yes | no | no | no | no |
| `gitlike` | yes | — | no | no | no |
| `internal/**`, `cmd/**` | yes | no | yes | yes | no |
| `examples/**` | yes | yes | no | no | no |
| `benchmarks` | yes | yes | no | no | no |

`gitlike` is a library *at* the application layer — the shape an app's own object model takes, shipped as a reference — not an application and not an example. `cmd/cask` and `gitlike` are 2nd-class peers with no dependency between them.

## 2. Error contract

Sentinel errors, defined in one `errors.go`:

```go
var (
    ErrNotFound       = errors.New("cas: object not found")
    ErrDigestMismatch = errors.New("cas: digest mismatch")
    ErrInvalidDigest  = errors.New("cas: invalid digest")
    ErrUnknownType    = errors.New("cas: unknown object type or version")
    ErrCorrupt        = errors.New("cas: corrupt object")
    ErrCodecMismatch  = errors.New("cas: codec mismatch")
    ErrUnsupported    = errors.New("cas: operation not supported by backend")
)
```

| Sentinel | Returned when |
| --- | --- |
| `ErrNotFound` | a backend's "not found" (`os.IsNotExist`), mapped via `%w` |
| `ErrDigestMismatch` | `Verify`, and integrity checks on read |
| `ErrInvalidDigest` | `ParseDigest` / `Digest.UnmarshalText`, wrapped with the offending input; a legacy `"sha256:hexdigest"` reference is rejected, not reinterpreted |
| `ErrCorrupt` | `Store.Get` finds bytes that are not readable data: an envelope that does not parse (a truncated or oversized header field, an empty type name, a frame version this build cannot read, a payload length that does not fit the frame), or a payload the store codec cannot decode, decodes to nil, or whose object fails `Validate`. Structural failure is `ErrCorrupt` in **every** envelope reader — `Store.Get`, `EnvelopeFromBytes`, `EnvelopeType`, `PeekType`, `cas/repo.Registry.Resolve` — the answer `Store.Type` and `PeekType` already gave for those bytes |
| `ErrCodecMismatch` | `Store.Get` finds the envelope's codec identity tag and the reading codec's own tag both present and differing — bytes intact, type known, hence deliberately distinct from `ErrCorrupt` and `ErrUnknownType`. A tagless object (a version 1 envelope, or a codec without `CodecNamer`) is never a mismatch — no identity to compare, and `ErrCorrupt` stays the answer if the payload then fails to decode |
| `ErrUnknownType` | a dispatch answer about **intact** bytes, never a parse failure: an envelope naming a type nothing registered (`cas/repo.UnknownTypeError`), one outside a caller's fixed model (`gitlike`), or a value the store's own codec decoded under a different `Type()` than the envelope records. A consumer skipping unknown types with `errors.Is(err, cas.ErrUnknownType)` therefore cannot skip a damaged object by accident — go-cask#202 |
| `ErrUnsupported` | `Sweep` when `SweepOptions.MinAge > 0` is requested against a backend that does not implement `Statter` |

- Never compare error strings; always `errors.Is` / `errors.As`. Doc comments state which errors each method can return.
- **A sentinel is added only when the distinction it names is not better carried by a value.** An unknown *envelope version* deliberately has none: `PeekVersion`/`Store.Version` return the byte verbatim for comparison against `EnvelopeVersion` — data, not an error. A reader that must parse the whole frame cannot proceed on an unknown version and reports `ErrCorrupt` like any other unusable header (`Store.Get`); the verbatim byte keeps "written by a newer format" distinguishable from "damaged bytes", so no caller matches an error string.

## 3. No mutable global state

- No hash registry, no algorithm table: the core names no algorithm and implements none. A `Digest` is raw bytes; the client injects a `cas.Hasher` (`cas/hash/sha256` ships the default).
- Preferred: `Store[T]` takes its hasher explicitly — `New[T](raw, codec, hasher)` stores the client's choice; nothing is resolved at construction.
- No other package-level mutable state in `cas`.

## 4. API shape rules

1. `context.Context` is the first parameter of any I/O-capable function.
2. Functional options for optional configuration — each backend declares its own `Option func(*itsConfig)` (`fs.Option`, `mem.Option`, `packfs.Option`), so an option built for one backend is a compile error against another instead of a silent no-op. Never positional `bool`/`int` soup.
3. Zero values are usable where meaningful (the zero `Digest` is the absent reference, an empty store).
4. Accept interfaces, return concrete types.
5. No `any`/`interface{}` in the exported API: no exported value, parameter or result type may be `any`. An unconstrained type parameter (`Codec[T any]`, `Object[T any]`) is Go's constraint syntax, not a value type. One recorded exception (go-cask#191): the low-level CBOR codec is a dynamic value model by design, so `cas/codec/cbor` exports `NewValue() Codec[any]` and `NewMap() Codec[map[string]any]`; every other exported signature in the repo is `any`-free, and new `any` in an exported API needs the same explicit ratification. `internal/design` walks the module's exported declarations and fails on a value-position `any` outside its allow-list, which names exactly those two symbols; a stale entry fails too, so an exemption cannot outlive the `any` it was granted for.
6. Names: no stutter (`cas.Store`, never `cas.CasStore`); initialisms correct (`URL`, `ID`, `HTTP`).
7. Minimal method sets; prefer functions over methods when no state is involved.
8. Streaming types (`io.Reader`/`io.ReadCloser`) used consistently; ownership ("caller MUST Close") documented.

## 5. Compatibility policy

- Library baseline **Go 1.24+** (generics, enhanced routing, `omitzero` JSON tags); built/tested with the repo toolchain (1.27). Stdlib except the approved `golang.org/x/sys` dependency in `cas/bloom/persistent` for portable mmap flushing (coding-guidelines §3).
- Only additive, non-breaking changes inside the current major. Breaking changes require a major version and a migration note — **except** changes ratified while the surface was in its first release cycle with negligible adoption, each shipped as a documented `BREAKING CHANGE` with a migration note (versioning §4) rather than waiting for a `/v2` mirror. All three are recorded in `versioning.md` §1:

| Version | Change |
| --- | --- |
| `v1.2.0` | `cas.Hash` became a concrete value type, giving up its JSON marshalling to the then-`jsoncodec.Hash` field type |
| `v1.3.0` | the address type became the hash-agnostic `cas.Digest` with a client-injected `cas.Hasher` — a loud, un-migrated break: object type names stay `@1`; previously stored reference payloads fail to decode (operations §5, cas-core §4.2) |
| `v1.3.0` | `gitlike.NewRepository` takes the caller's `gitlike.Codecs` set; `Commit`'s required-tree rule moved out of its JSON methods into `Commit.Validate()`, enforced by the core (`cas.Validator`) — confined to the reference layer, NOT part of the stable `cas` surface (§1) |

The third exception is the last: any further breaking change follows the ordinary rule again.

- Example HTTP surfaces version independently (`/api/cas/v1` → `/api/cas/v2`, api-design §12).
- Deprecations: keep deprecated symbols ≥ one minor release with a doc-comment pointer to the replacement.

## 6. Lean checklist

- [x] `cas/` ≤ ~1600 code lines (906 today; blank and comment-only lines do not count) and ≤ ~51 exported identifiers — 51 today (`EncodeEnvelope` by go-cask#187, `PeekHeader` by go-cask#322, then `Header`, `HeaderType`, `WalkDigests`, `Node` and `NodeResolver` by go-cask#319), re-checked against the package's exported declarations in go-cask#191, go-cask#319 and go-cask#322 (§1 budget, metric stated in go-cask#271)
- [x] sentinel errors + `errors.Is` everywhere; no string-compared errors
- [x] no mutable globals (no algorithm registry and no algorithm table; the client injects a `Hasher`)
- [x] functional options; zero values usable; `context.Context` first
- [x] compatibility policy documented and honored
- [x] the two `package memory` subpackages imported as `backmem`/`cachemem` only — checked by `internal/design` (§1, go-cask#269)
