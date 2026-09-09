---
type: Specification
title: Library Design — go-cask
description: The lean-core contract for the cas library — exported-surface budget, sentinel errors with errors.Is, explicit configuration without mutable globals, API shape rules, and a compatibility policy.
version: v13
---

# Library Design — go-cask

The `cas` package must be small, obvious, and hard to misuse. Related: `cas-core.md` (layering), `coding-guidelines.md` (idiomatic Go, no `any`), `performance.md` (fast paths stay simple).

## 1. Lean-core budget

- `cas/` (excluding `_test.go`) SHOULD stay ≤ ~1600 LOC and ≤ ~40 exported identifiers (re-baselined 2026-09 to the frozen surface after the pre-v1.0.0 audit). Every exported name must earn its place; if it can live in a subpackage or an example, it does. Advisory ceiling for additions, not a shrinking target.
- **Stable core surface** (the API docs promise — cas-core §7.1): `Hash`, `HashFunc`, `RegisterHash`, `ParseHash`, `NewHasher`, `NewHash`, `HashBytes`, `Backend` (byte interface), `Stats`, `Codec[T]` (interface), `Object`, `Store[T]`, `New[T]`, `Walker[T]`, `NewWalker`, and the six sentinel `Err*` values — all in `package cas`.
- Byte backends, typed codecs and caches live in subpackages, never in `package cas`: filesystem `fs.Backend` (`fs.New(base, opts...)`; `fs.WithFanOut`, `fs.WithFanLevels`, `fs.WithDirSync`; constants `fs.DefaultFanOut`, `fs.DefaultFanLevels`, `fs.MaxFanDepth`) and in-memory `memory.Backend` (`memory.New(opts...)`; `memory.WithMaxSize`); codecs `json.New[T]()` and `gob.New[T]()` (there is no `JSONCodec`/`GobCodec` type); caches `memory.CachedStore[T]` / `memory.CachedObject[T]` (`memory.New(store)`), `lru.Cache[T]` (`lru.New(store, maxSize)`), and `prefetch.NewSmartCache`.
- Optional machinery stays out of the core: prefetch-on-access and cache-monitor recipes are demonstrated by `examples/notes` and `examples/artifacts` — never part of `package cas`; record the decision in `AGENTS.md` when made.
- The `gitlike` layer is NOT part of `cas`.

## 2. Error contract

Sentinel errors, defined in one `errors.go`:

```go
var (
    ErrNotFound         = errors.New("cas: object not found")
    ErrHashMismatch     = errors.New("cas: hash mismatch")
    ErrUnknownAlgorithm = errors.New("cas: unknown hash algorithm")
    ErrInvalidHash      = errors.New("cas: invalid hash")
    ErrUnknownType      = errors.New("cas: unknown object type or version")
    ErrCorrupt          = errors.New("cas: corrupt object")
)
```

- Backends map their "not found" (`os.IsNotExist`) to `ErrNotFound` via `%w`.
- `Verify` (and integrity checks on read) return `ErrHashMismatch`.
- `ParseHash` returns `ErrInvalidHash` / `ErrUnknownAlgorithm`, wrapped with the offending input in the message.
- `Store.Get` returns `ErrCorrupt` when the stored payload cannot be decoded by the store codec.
- Deserializers return `ErrUnknownType` for an unregistered type name or major version.
- Never compare error strings; always `errors.Is` / `errors.As`. Doc comments state which errors each method can return.

## 3. No mutable global state

- `hashRegistry` is populated only at init (or via an explicit, documented registration step before any store is constructed). Must be safe to read concurrently after startup; runtime mutation requires a lock and is discouraged.
- Preferred: `Store[T]` takes its hasher explicitly — `New[T](raw, codec, algo)` resolves the algorithm at construction; an explicit-hasher variant exists for custom functions. No hidden global dependence in the hot path.
- No other package-level mutable state in `cas`.

## 4. API shape rules

1. `context.Context` is the first parameter of any I/O-capable function.
2. Functional options for optional configuration (the `backend.Option` pattern) — never positional `bool`/`int` soup.
3. Zero values are usable where meaningful (zero `Hash`, empty store).
4. Accept interfaces, return concrete types.
5. No `any`/`interface{}` in the exported API.
6. Names: no stutter (`cas.Store`, never `cas.CasStore`); initialisms correct (`URL`, `ID`, `HTTP`).
7. Minimal method sets; prefer functions over methods when no state is involved.
8. Streaming types (`io.Reader`/`io.ReadCloser`) used consistently; ownership ("caller MUST Close") documented.

## 5. Compatibility policy

- Library baseline **Go 1.22+** (generics, enhanced routing, stdlib-only); built/tested with the repo toolchain (1.27).
- Only additive, non-breaking changes inside the current major; breaking changes require a major version and a migration note.
- Example HTTP surfaces version independently (`/api/cas/v1` → `/api/cas/v2`, api-design §12).
- Deprecations: keep deprecated symbols ≥ one minor release with a doc-comment pointer to the replacement.

## 6. Lean checklist

- [x] `cas/` ≤ ~1500 LOC and ≤ ~20 exported identifiers
- [x] sentinel errors + `errors.Is` everywhere; no string-compared errors
- [x] no mutable globals; registry init-only or per-store hasher
- [x] functional options; zero values usable; `context.Context` first
- [x] compatibility policy documented and honored
