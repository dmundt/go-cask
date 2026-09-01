---
title: Extensions — go-cask
description: The simple, minimal requirements every future extension or client built on the cas core must satisfy — use the stable surface, extend don't modify, follow the recipes, stay compatible.
version: v1
---

# Extensions — go-cask

> This file is for **future extensions and clients**: new backends, object
> types, codecs, hash algorithms, services, or apps that build on the `cas`
> core. It is intentionally short — the details live in the referenced specs.
>
> Related: `.github/instructions/cas-core.instructions.md` §7 (the extension
> contract and recipes), `.github/instructions/library-design.instructions.md`
> (lean-core, errors, compatibility), `.github/instructions/coding-guidelines.
> instructions.md` (Go style), `.github/instructions/examples.instructions.md`
> (runnable demonstrations).

---

## 1. Principles

1. **Extend, don't modify.** The `cas` core and the `gitlike` example are
   frozen. Your extension lives in its own package and composes the core.
2. **Use only the stable surface.** The exported identifiers listed in
   cas-core §7.1 are the contract; everything else is internal and MUST NOT be
   relied upon.
3. **One job per extension.** A focused extension is simpler to review, test,
   and maintain. If it does two unrelated things, split it.
4. **Keep it simple.** Prefer the core's existing mechanisms over new ones.
   If an extension needs a feature the core lacks, raise it as a core change —
   do not work around it inside the extension.

---

## 2. Requirements

| # | Requirement                                                              |
| - | ------------------------------------------------------------------------ |
| 1 | Use the documented recipes (cas-core §7.2): implement `RawStore`, `Object[T]`, `Codec[T]`, `HashFunc`, or wrap `CachedStore[T]` — nothing else. |
| 2 | Never add `any`/`interface{}` or reflection to a public API (coding-guidelines §8). |
| 3 | Errors: wrap the core's sentinel errors with `%w` (`ErrNotFound`, `ErrHashMismatch`, …) and use `errors.Is`; map them to your layer's errors (api-design §6 for HTTP). |
| 4 | Compatibility: additive changes only; never break the core's stable surface (library-design §5). |
| 5 | Performance: keep the core's contracts — lock-free reads, streaming (`io.Reader`/`io.ReadCloser`), no full buffering of large objects, bounded allocations (performance spec). |
| 6 | Correctness: cover the relevant CAS laws for your extension's surface (testing-strategy §1); run `-race` tests. |
| 7 | Dependencies: standard library first; any external package MUST be justified and vendored (coding-guidelines §3). |
| 8 | Documentation: doc comments on every exported identifier (coding-guidelines §7); OpenAPI for any HTTP surface (api-design §13). |
| 9 | Demonstrations: if your extension is a good teaching example, propose it in `examples.instructions.md` instead of growing the core. |

---

## 3. Extension Checklist

- [ ] Lives in its own package; `cas`/`gitlike` untouched
- [ ] Uses only the stable surface (cas-core §7.1)
- [ ] Follows the matching recipe (cas-core §7.2); no workarounds for missing core features
- [ ] No `any`/reflection in the public API
- [ ] Sentinel errors wrapped with `%w`; `errors.Is` on read
- [ ] Streaming and lock-free-read contracts honored
- [ ] Tests cover the relevant CAS laws; `-race` green
- [ ] Std-lib only unless justified + vendored
- [ ] Exported identifiers documented; OpenAPI if HTTP
- [ ] Simple: one job, nothing speculative
