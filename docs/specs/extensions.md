---
type: Specification
title: Extensions — go-cask
description: The simple, minimal requirements every future extension or client built on the cas core must satisfy — use the stable surface, extend don't modify, follow the recipes, stay compatible — plus the catalog of designed-but-deferred possible extensions (packfiles, compression layer, chunking).
version: v7
---

# Extensions — go-cask

Requirements for **future extensions and clients** (backends, object types, codecs, hash algorithms, services, apps on the `cas` core). Related: `cas-core.md` §7 (extension contract/recipes), `library-design.md`, `coding-guidelines.md`, `examples.md`.

## 1. Principles

1. **Extend, don't modify.** `cas` core and `gitlike` are frozen; your extension lives in its own package and composes the core.
2. **Use only the stable surface** (cas-core §7.1); everything else is internal and MUST NOT be relied upon.
3. **One job per extension.** If it does two unrelated things, split it.
4. **Keep it simple.** Prefer the core's existing mechanisms; a needed core feature is a core change — do not work around it inside the extension.

## 2. Requirements

1. Use the documented recipes (cas-core §7.2): implement `Backend`, `Object[T]`, `Codec[T]`, or wrap `CachedStore[T]` — nothing else.
2. Never add `any`/`interface{}` or reflection to a public API (coding-guidelines §8).
3. Wrap the core's sentinel errors with `%w` and use `errors.Is`; map them to your layer (api-design §6 for HTTP).
4. Additive changes only; never break the core's stable surface (library-design §5).
5. Keep the core's performance contracts: lock-free reads, streaming (`io.Reader`/`io.ReadCloser`), no full buffering of large objects, bounded allocations (performance spec).
6. Cover the relevant CAS laws for your surface (testing-strategy §1); run `-race`.
7. Std-lib first; any external package MUST be justified and vendored (coding-guidelines §3).
8. Doc comments on every exported identifier (coding-guidelines §7); OpenAPI for any HTTP surface (api-design §13).
9. If your extension is a good teaching example, propose it in `examples.md` instead of growing the core.

## 3. Known possible extensions

Designed but deliberately deferred — not part of the core; SHALL be built as extensions/clients when a real need appears. Entries point at the owning spec, not restating the design (AGENT.md §4). An entry is listed only once a real design exists in an owning spec; a wish without a design is not an entry.

| Extension | What it is | Design lives in |
|---|---|---|
| **Packfiles** | Git-style packing: group small loose objects into immutable `pack-<ts>.pack` + `.idx` index — O(packs) `List`/`Stats`, pack-level GC, streaming reads via `io.SectionReader` | cas-core §8 (follow-up 4); performance §9 |
| **Compression layer** | `CompressedStore` wrapping `Backend` with gzip via `io.Pipe`, transparent above the byte layer | cas-core §8 (follow-up 5) |
| **Encryption layer** | `EncryptedCodec[T]` wrapping `Codec[T]` with AES-256-GCM; app supplies the key — the core never generates/stores keys | cas-core §8 (follow-up 8); §4.6/§7.2 |
| **Content-defined chunking** | Rolling-hash chunking of very large blobs for chunk-granular dedup | performance §10 |

**Deferral decision (2026-09):** every catalog entry stays deferred — no new core surface before v1.0.0. Triggers: build **packfiles** only when a real workload stores ≳10^5–10^6 objects or needs bulk small-object ingest (~1–2 ms per-file write floor is the crossover); **compression** or **encryption** only when an app needs compressible large blobs / encryption at rest; **chunking** only when very large blobs need chunk-granular dedup. Below those triggers the per-object layout is the leaner choice.

## 4. Checklist

- [x] Lives in its own package; `cas`/`gitlike` untouched
- [x] Uses only the stable surface (cas-core §7.1)
- [x] Follows the matching recipe (cas-core §7.2); no workarounds for missing core features
- [x] No `any`/reflection in the public API
- [x] Sentinel errors wrapped `%w`; `errors.Is` on read
- [x] Streaming and lock-free-read contracts honored
- [x] Tests cover the relevant CAS laws; `-race` green
- [x] Std-lib only unless justified + vendored
- [x] Exported identifiers documented; OpenAPI if HTTP
- [x] Simple: one job, nothing speculative
- [x] Catalog entries (§3) reference an owning spec; no design-less wishes
