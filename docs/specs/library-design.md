---
type: Specification
title: Library Design — go-cask
description: The lean-core contract for the cas library — exported-surface budget, sentinel errors with errors.Is, explicit configuration without mutable globals, API shape rules, and a compatibility policy.
version: v23
---

# Library Design — go-cask

The `cas` package must be small, obvious, and hard to misuse. Related: `cas-core.md` (layering), `coding-guidelines.md` (idiomatic Go, no `any`), `performance.md` (fast paths stay simple).

## 1. Lean-core budget

- `cas/` (excluding `_test.go`) SHOULD stay ≤ ~1600 LOC and ≤ ~40 exported identifiers (re-baselined 2026-09 to the frozen surface after the pre-v1.0.0 audit). Every exported name must earn its place; if it can live in a subpackage or an example, it does. Advisory ceiling for additions, not a shrinking target.
- **Stable core surface** (the API docs promise — cas-core §7.1): `Digest`, `NewDigest`, `ParseDigest`, `CheckDigest`, `Hasher` (the client's algorithm seam), `Backend` (byte interface), `Stats`, `Codec[T]` (interface), `Object`, `Validator` (the optional object-invariant contract the store enforces), `Store[T]`, `New[T]`, `Walker[T]`, `NewWalker`, `Envelope`, `EnvelopeFromBytes`, and the five sentinel `Err*` values — all in `package cas`.
- Byte backends, typed codecs, the shipped hasher and caches live in subpackages, never in `package cas`: filesystem `fs.Backend` (`fs.New(base, opts...)`; `fs.WithFanOut`, `fs.WithFanLevels`, `fs.WithDirSync`; constants `fs.DefaultFanOut`, `fs.DefaultFanLevels`, `fs.MaxFanDepth`) and in-memory `memory.Backend` (`memory.New(opts...)`; `memory.WithMaxSize`); the client hasher `sha256.New()` / `sha256.Of` / `sha256.Parse` / `sha256.Format` / `sha256.Short` (`cas/hash/sha256` — the default go-cask's own clients wire in, and nothing in `cas` imports it); codecs `json.New[T]()` and `gob.New[T]()` (there is no `JSONCodec`/`GobCodec` type) — objects declare plain `cas.Digest` reference fields, which render themselves through `encoding.TextMarshaler` (`MarshalText`/`UnmarshalText`), so no hash JSON code lives anywhere; caches `memory.CachedStore[T]` / `memory.CachedObject[T]` (`memory.New(store)`), `lru.Cache[T]` (`lru.New(store, maxSize)`), and `prefetch.NewSmartCache`.
- Optional machinery stays out of the core: prefetch-on-access and cache-monitor recipes are demonstrated by `examples/notes` and `examples/artifacts` — never part of `package cas`; record the decision in `AGENTS.md` when made.
- The `gitlike` layer is NOT part of `cas`.

## 2. Error contract

Sentinel errors, defined in one `errors.go`:

```go
var (
    ErrNotFound       = errors.New("cas: object not found")
    ErrDigestMismatch = errors.New("cas: digest mismatch")
    ErrInvalidDigest  = errors.New("cas: invalid digest")
    ErrUnknownType    = errors.New("cas: unknown object type or version")
    ErrCorrupt        = errors.New("cas: corrupt object")
)
```

- Backends map their "not found" (`os.IsNotExist`) to `ErrNotFound` via `%w`.
- `Verify` (and integrity checks on read) return `ErrDigestMismatch`.
- `ParseDigest` / `Digest.UnmarshalText` return `ErrInvalidDigest`, wrapped with the offending input in the message — a legacy `"sha256:hexdigest"` reference is rejected, not reinterpreted.
- `Store.Get` returns `ErrCorrupt` when the stored payload cannot be decoded by the store codec.
- Deserializers return `ErrUnknownType` for an unregistered type name or major version.
- Never compare error strings; always `errors.Is` / `errors.As`. Doc comments state which errors each method can return.

## 3. No mutable global state

- There is no hash registry and no algorithm table: the core names no algorithm and implements none. A `Digest` is raw bytes, and the client injects a `cas.Hasher` (`cas/hash/sha256` ships the default).
- Preferred: `Store[T]` takes its hasher explicitly — `New[T](raw, codec, hasher)` stores the client's choice; nothing is resolved at construction and no hidden global dependence exists in the hot path.
- No other package-level mutable state in `cas`.

## 4. API shape rules

1. `context.Context` is the first parameter of any I/O-capable function.
2. Functional options for optional configuration (the `backend.Option` pattern) — never positional `bool`/`int` soup.
3. Zero values are usable where meaningful (the zero `Digest` is the absent reference, an empty store).
4. Accept interfaces, return concrete types.
5. No `any`/`interface{}` in the exported API.
6. Names: no stutter (`cas.Store`, never `cas.CasStore`); initialisms correct (`URL`, `ID`, `HTTP`).
7. Minimal method sets; prefer functions over methods when no state is involved.
8. Streaming types (`io.Reader`/`io.ReadCloser`) used consistently; ownership ("caller MUST Close") documented.

## 5. Compatibility policy

- Library baseline **Go 1.24+** (generics, enhanced routing, `omitzero` JSON tags, stdlib-only); built/tested with the repo toolchain (1.27).
- Only additive, non-breaking changes inside the current major; breaking changes require a major version and a migration note — **except** for changes explicitly ratified while the surface is in its first release cycle and adoption is negligible, each shipped as a documented `BREAKING CHANGE` with a migration note (versioning §4) rather than waiting for a `/v2` mirror: `cas.Hash` became a concrete value type (giving up its JSON marshalling to the then-`jsoncodec.Hash` field type) in `v1.2.0`; the address type became the hash-agnostic `cas.Digest` with a client-injected `cas.Hasher` in the unreleased cycle after it; and in that same cycle `gitlike.NewRepository` takes the caller's `gitlike.Codecs` set, with `Commit`'s required-tree rule moved out of its JSON methods into `Commit.Validate()` enforced by the core (`cas.Validator`). All three are recorded in `versioning.md` §1; the digest change is a loud, un-migrated break (object type names stay `@1`, previously stored reference payloads fail to decode — operations §5, cas-core §4.2), and the gitlike change is confined to the reference layer, which is NOT part of the stable `cas` surface (§1). The third exception is the last: any further breaking change follows the ordinary rule again.
- Example HTTP surfaces version independently (`/api/cas/v1` → `/api/cas/v2`, api-design §12).
- Deprecations: keep deprecated symbols ≥ one minor release with a doc-comment pointer to the replacement.

## 6. Lean checklist

- [x] `cas/` ≤ ~1600 LOC and ≤ ~40 exported identifiers (§1 budget — the checklist previously said ~1500/~20, which contradicted it)
- [x] sentinel errors + `errors.Is` everywhere; no string-compared errors
- [x] no mutable globals (no algorithm registry and no algorithm table; the client injects a `Hasher`)
- [x] functional options; zero values usable; `context.Context` first
- [x] compatibility policy documented and honored
