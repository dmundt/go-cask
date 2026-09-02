---
title: Library Design — go-cask
description: The lean-core contract for the cas library — exported-surface budget, sentinel errors with errors.Is, explicit configuration without mutable globals, API shape rules, and a compatibility policy.
version: v5
---

# Library Design — go-cask

> The `cas` package must be the kind of library you reach for without
> thinking: small, obvious, and hard to misuse. This file sets the quality
> bar for "lean but powerful".
>
> Related: `cas-core.instructions.md` (layering),
> `coding-guidelines.instructions.md` (idiomatic Go, no `any`),
> `performance.instructions.md` (fast paths must stay simple).

---

## 1. Lean-Core Budget

- `cas/` (excluding `_test.go`) SHOULD stay ≤ ~1500 LOC and ≤ ~20 exported
  identifiers. Every exported name must earn its place; if it can live in a
  subpackage or an example, it does.
- **Stable core surface** (the API the docs promise):
  `Hash`, `HashFunc`, `RegisterHash`, `ParseHash`, `NewHasher`, `HashBytes`,
  `RawStore`, `FSRawStore` (+ `FSOption`, `WithFanOut`, `WithFanLevels`),
  `MemoryRawStore`, `StoreStats`, `Codec`, `JSONCodec`, `Object`, `Store`,
  `Walker`, `CachedObject`, `CachedStore`, `LRUCache`.
- **Optional machinery stays out of the core.** Prefetch-on-access and
  cache-monitor recipes are demonstrated by `examples/notes` and
  `examples/artifacts` — never part of package `cas`;
  record the decision in the copilot instructions when made.
- The `gitlike` layer is NOT part of `cas` (layering rule from the
  architecture doc).

---

## 2. Error Contract

Sentinel errors, defined in one `errors.go`:

```go
var (
    ErrNotFound         = errors.New("cas: object not found")
    ErrHashMismatch     = errors.New("cas: hash mismatch")
    ErrUnknownAlgorithm = errors.New("cas: unknown hash algorithm")
    ErrInvalidHash      = errors.New("cas: invalid hash")
    ErrUnknownType      = errors.New("cas: unknown object type or version")
)
```

Rules:

- Backends map their "not found" (`os.IsNotExist`) to `ErrNotFound` via
  `%w`.
- `Verify` (and integrity checks on read) return `ErrHashMismatch`.
- `ParseHash` returns `ErrInvalidHash` / `ErrUnknownAlgorithm`, wrapped with
  the offending input in the message.
- Deserializers return `ErrUnknownType` for an unregistered type name or
  major version (object-versioning §4).
- Never compare error strings; always `errors.Is` / `errors.As`.
- Doc comments state which errors each method can return.

---

## 3. No Mutable Global State

- `hashRegistry` is populated **only at init** (or via an explicit,
  documented registration step before any store is constructed). It must be
  safe to read concurrently after startup; runtime mutation requires a lock
  and is discouraged.
- Preferred: `Store[T]` takes its hasher explicitly —
  `NewStore(raw, codec, algo)` resolves the algorithm at construction, and an
  explicit hasher variant exists for custom functions. No hidden global
  dependence in the hot path.
- No other package-level mutable state in `cas`.

---

## 4. API Shape Rules

1. `context.Context` is the first parameter of any I/O-capable function.
2. Functional options for optional configuration (the `FSOption` pattern) —
   never positional `bool`/`int` soup.
3. Zero values are usable where meaningful (zero `Hash`, empty store).
4. Accept interfaces, return concrete types.
5. No `any` / `interface{}` in the exported API (coding-guidelines §8).
6. Names: no stutter (`cas.Store`, never `cas.CasStore`); initialisms correct
   (`URL`, `ID`, `HTTP`).
7. Minimal method sets; prefer functions over methods when no state is
   involved.
8. Streaming types (`io.Reader` / `io.ReadCloser`) used consistently;
   ownership ("caller MUST Close") documented.

---

## 5. Compatibility Policy

- Library baseline: **Go 1.21+** (generics); built and tested with the repo
  toolchain (1.27).
- Semver discipline: only additive, non-breaking changes inside the current
  major version; breaking changes require a major version and a migration
  note.
- Example HTTP surfaces version independently (`/api/cas/v1` →
  `/api/cas/v2`, per api-design §12).
- Deprecations: keep deprecated symbols for at least one minor release with a
  doc-comment pointer to the replacement.

---

## 6. Lean Checklist

- [ ] `cas/` ≤ ~1500 LOC and ≤ ~20 exported identifiers
- [ ] sentinel errors + `errors.Is` everywhere; no string-compared errors
- [ ] no mutable globals; registry init-only or per-store hasher
- [ ] functional options; zero values usable; `context.Context` first
- [ ] compatibility policy documented and honored
