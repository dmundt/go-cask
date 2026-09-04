---
title: Extensions — go-cask
description: The simple, minimal requirements every future extension or client built on the cas core must satisfy — use the stable surface, extend don't modify, follow the recipes, stay compatible — plus the catalog of designed-but-deferred possible extensions (packfiles, compression layer, chunking).
version: v5
---

# Extensions — go-cask

> This file is for **future extensions and clients**: new backends, object
> types, codecs, hash algorithms, services, or apps that build on the `cas`
> core. It is intentionally short — the details live in the referenced specs.
>
> Related: `docs/instructions/cas-core.md` §7 (the extension
> contract and recipes), `docs/instructions/library-design.md`
> (lean-core, errors, compatibility), `docs/instructions/coding-guidelines.md`
> (Go style), `docs/instructions/examples.md` (runnable demonstrations).

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
| 9 | Demonstrations: if your extension is a good teaching example, propose it in `examples.md` instead of growing the core. |

---

## 3. Known Possible Extensions

A catalog of extensions the specification set has **designed but deliberately
deferred**. They are not part of the core and SHALL be built as extensions —
their own package, or a client — when a real need appears.
Nothing in this catalog is committed work; the entries point at the owning
spec instead of restating the design (AGENT.md §4: no duplicated drift).

| Extension | What it is | Design lives in |
| --------- | ---------- | --------------- |
| **Packfiles** | Git-style packing: group small loose objects into immutable `pack-<ts>.pack` files plus a `.idx` index — O(packs) `List`/`Stats`, pack-level GC, streaming reads via `io.SectionReader`. | cas-core §8 (follow-up 4); performance §9 (format, write policy, acceptance criteria) |
| **Compression layer** | `CompressedStore` wrapping `RawStore` with gzip via `io.Pipe`, transparent to everything above the byte layer. | cas-core §8 (follow-up 5) |
| **Encryption layer** | `EncryptedCodec[T]` wrapping `Codec[T]` with authenticated encryption (AES-256-GCM); the application supplies the key — the core never generates or stores keys. | cas-core §8 (follow-up 8); cas-core §4.6/§7.2 (codec recipe) |
| **Content-defined chunking** | Rolling-hash chunking of very large blobs for chunk-granular dedup. | performance §10 |

Rule for extending this catalog: an extension is listed only once a real
design exists in an owning spec (like performance §9/§10); a wish without a
design is not an entry.

**Deferral decision (2026-09):** every catalog entry stays deferred — no new
core surface before v1.0.0. The scale-probe anchors (benchmarks §6,
Windows/NTFS, per-object layout) set the data-driven revisit triggers:
build **packfiles** (with its `.idx` index) only when a real workload stores
≳10^5–10^6 objects or needs bulk small-object ingest — the ~1–2 ms per-file
write floor is the crossover; build **compression** or **encryption** only
when an actual app needs compressible large blobs or encryption at rest;
build **content-defined chunking** only when very large blobs need
chunk-granular dedup. Below those triggers the current per-object layout is
the leaner choice.

---

## 4. Extension Checklist

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
- [ ] Catalog entries (§3) reference an owning spec; no design-less wishes
