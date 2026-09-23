---
type: Specification
title: Extensions — go-cask
description: The simple, minimal requirements every future extension or client built on the cas core must satisfy — use the stable surface, extend don't modify, follow the recipes, stay compatible — plus the catalog of designed-but-deferred possible extensions (packfiles, compression layer, chunking).
version: v11
---

# Extensions — go-cask

Requirements for **future extensions and clients** (backends, object types, codecs, hashers, services, apps on the `cas` core). Related: `cas-core.md` §7 (extension contract/recipes), `library-design.md`, `coding-guidelines.md`, `examples.md`.

## 1. Principles

1. **Extend, don't modify.** `cas` core and `gitlike` are frozen; your extension lives in its own package and composes the core.
2. **Use only the stable surface** (cas-core §7.1); everything else is internal and MUST NOT be relied upon.
3. **One job per extension.** If it does two unrelated things, split it.
4. **Keep it simple.** Prefer the core's existing mechanisms; a needed core feature is a core change — do not work around it inside the extension.

## 2. Requirements

1. Use the documented recipes (cas-core §7.2): implement `Backend`, `Object[T]`, `Codec[T]` or `Hasher`, or wrap `CachedStore[T]` — nothing else. A custom algorithm is a `cas.Hasher` (`Digest(io.Reader)` + `Validate(Digest)`) injected into `cas.New`/`gitlike.NewRepository`; there is no `HashFunc`, no registry, and no core change involved.
2. Never add `any`/`interface{}` or reflection to a public API (coding-guidelines §8).
3. Wrap the core's sentinel errors with `%w` and use `errors.Is`; map them to your layer (api-design §6 for HTTP).
4. Additive changes only; never break the core's stable surface (library-design §5).
5. Keep the core's performance contracts: lock-free reads, streaming (`io.Reader`/`io.ReadCloser`), no full buffering of large objects, bounded allocations (performance spec).
6. Cover the relevant CAS laws for your surface (testing-strategy §1); run `-race`.
7. Std-lib first; any external package MUST be justified and vendored (coding-guidelines §3).
8. Doc comments on every exported identifier (coding-guidelines §7); OpenAPI for any HTTP surface (api-design §13).
9. If your extension is a good teaching example, propose it in `examples.md` instead of growing the core.
10. Build your stores with your own one-line constructor: `package cas` ships no `NewJSON`/`NewCompressedJSON` because it must not import `cas/codec`, and a helper taking a codec saves nothing over `cas.New`. Write it where the type lives — `func newNoteStore(backend cas.Backend, hasher cas.Hasher) *cas.Store[*Note] { return cas.New(backend, json.New[*Note](), hasher) }` — or register each store once with `cas/repo.RegisterStore` and read it back typed with `cas/repo.LookupStore[T]` (never a `map[string]any` or a caller-side assertion). `Store.Close` is idempotent and forwards to a backend that implements `io.Closer`: close the store or the backend once the stores over it are finished, so a flush-on-close backend such as `packfs` is not left with unwritten state.
11. **Name the wire format your codec produces** (`cas.CodecNamer`, `CodecName() string`, cas-core §4.6). The tag is written into the envelope and compared on read, so a codec change is reported as `cas.ErrCodecMismatch` instead of surfacing as a decode failure — which is what removes the old requirement to hand-bump every type major on a codec change. Declare a stable lowercase tag (`json`, `gzip+json`), compose it from an inner codec's tag when your codec wraps one (`"<name>+" + inner`), and report `""` when the inner codec declares none. Never derive a tag from `%T`, reflection or the payload: a rename or a package move must not read as a format change. Omitting the interface is legal — the envelope then carries an empty tag and no check is made — but it means the codec is invisible to that check.

## 3. Known possible extensions

Designed but deliberately deferred — not part of the core; SHALL be built as extensions/clients when a real need appears. Entries point at the owning spec, not restating the design (AGENT.md §4). An entry is listed only once a real design exists in an owning spec; a wish without a design is not an entry.

| Extension | What it is | Design lives in |
|---|---|---|
| **Packfiles** | Git-style packing: group small loose objects into immutable `pack-<ts>.pack` + `.idx` index — O(packs) `List`/`Stats`, pack-level GC, streaming reads via `io.SectionReader` | cas-core §8 (follow-up 4); performance §9 |
| **Compression layer** | Implemented as opt-in `Codec[T]` wrappers in `cas/codec/gzip`, `cas/codec/zlib`, and `cas/codec/flate`: compress serialized bytes without changing `Digest`, object types, or the core store semantics | cas-core §8 (follow-up 5); §4.6 |
| **Pack helpers** | `cas/pack` provides fixed-size payload splitting and JSON sidecar metadata for large-object and operational workflows without changing object identity | performance §10 + cas-core §7.2 |
| **Encryption layer** | `EncryptedCodec[T]` wrapping `Codec[T]` with AES-256-GCM; app supplies the key — the core never generates/stores keys | cas-core §8 (follow-up 8); §4.6/§7.2 |
| **Content-defined chunking** | Rolling-hash chunking of very large blobs for chunk-granular dedup | performance §10 |

**Deferral decision (2026-09):** packfiles remain deferred — no new core surface before v1.0.0. Compression is implemented as a codec-layer optimization and remains opt-in; it is not a semantic change to the store or object model. Fixed-size chunking and manifest sidecars are implemented as helper packages, not new core doctrine. Encryption stays deferred until an app needs at-rest encryption; content-defined chunking is only for very large blobs with chunk-granular dedup. Below those triggers the per-object layout is the leaner choice.

**Rejected (2026-09): a namespace option (`fs.WithNamespace`).** Putting several stores in one root as `<base>/<namespace>/<fan-out>/<hex>` was rejected on four grounds: (1) the whole option reduces to `filepath.Join(root, name)` — which callers already have — plus a validator for a client-supplied path element (separators, `..`, absolute paths, Windows reserved names, case/NFC folding); (2) it reintroduces a runtime-chosen name used as a path element, the coupling the hash-agnostic core removed (cas-core §4.2); (3) it isolates nothing a separate base does not, because a store's isolation comes from the one-base exclusivity rule, not from a path segment (`List`/`Stats`/`Clean` walk the base recursively and match on the file name, so a prefix changes only where the walk starts — cas-core §4.4); (4) no consumer needs it — the CLI takes one `-store`, the viewer binds one backend, every example uses one store per base. Several stores under one root are already `fs.New(filepath.Join(root, name))`. Revisit only if a root-level *enumeration* need appears (one sweep or one stats view across many namespaces), which would be an app-layer root front-end over directories — not a byte-layer option.

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
