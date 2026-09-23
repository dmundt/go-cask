---
type: Specification
title: Extensions — go-cask
description: The simple, minimal requirements every future extension or client built on the cas core must satisfy — use the stable surface, extend don't modify, follow the recipes, stay compatible — plus the catalog of implemented and designed-but-deferred possible extensions (packfiles, compression layer, encryption layer, chunking) and the specified-but-unimplemented maintenance surface (§3.1).
version: v14
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
5. Keep the core's performance contracts: streaming (`io.Reader`/`io.ReadCloser`), no full buffering of large objects, bounded allocations (performance spec). Reads SHOULD be lock-free where the extension's layout allows it — the shipped packfile backend is the stated exception, because it reads an in-memory index under the mutex that serializes its writes (cas-core §4.14), and any deviation MUST be documented in the owning spec rather than assumed away.
6. Cover the relevant CAS laws for your surface (testing-strategy §1); run `-race`.
7. Std-lib first; any external package MUST be justified and vendored (coding-guidelines §3).
8. Doc comments on every exported identifier (coding-guidelines §7); OpenAPI for any HTTP surface (api-design §13).
9. If your extension is a good teaching example, propose it in `examples.md` instead of growing the core.
10. Build your stores with your own one-line constructor: `package cas` ships no `NewJSON`/`NewCompressedJSON` because it must not import `cas/codec`, and a helper taking a codec saves nothing over `cas.New`. Write it where the type lives — `func newNoteStore(backend cas.Backend, hasher cas.Hasher) *cas.Store[*Note] { return cas.New(backend, json.New[*Note](), hasher) }` — or register each store once with `cas/repo.RegisterStore` and read it back typed with `cas/repo.LookupStore[T]` (never a `map[string]any` or a caller-side assertion). `Store.Close` is idempotent and forwards to a backend that implements `io.Closer`: close the store or the backend once the stores over it are finished, so a backend that holds an append handle open (such as `packfs`, which persists its index on every packed `Put` and only releases the handle at close — no separate flush step exists, cas-core §4.14) is not left writing into a closed process.
11. **Name the wire format your codec produces** (`cas.CodecNamer`, `CodecName() string`, cas-core §4.6). The tag is written into the envelope and compared on read, so a codec change is reported as `cas.ErrCodecMismatch` instead of surfacing as a decode failure — which is what removes the old requirement to hand-bump every type major on a codec change. Declare a stable lowercase tag (`json`, `gzip+json`), compose it from an inner codec's tag when your codec wraps one (`"<name>+" + inner`), and report `""` when the inner codec declares none. Never derive a tag from `%T`, reflection or the payload: a rename or a package move must not read as a format change. Omitting the interface is legal — the envelope then carries an empty tag and no check is made — but it means the codec is invisible to that check.

## 3. Extension catalog

**Implemented** extensions ship in this repo; **deferred** entries are designed but not built and SHALL be built as extensions/clients when a real need appears. Entries point at the owning spec, not restating the design (AGENT.md §4). An entry is listed only once a real design exists in an owning spec; a wish without a design is not an entry.

| Extension | Status | What it is | Owned by |
|---|---|---|---|
| **Packfiles** | Implemented | `cas/backend/packfs` — an opt-in backend that keeps the loose tree and mirrors every `Put` into an append-only pack file plus a JSON index; `cask -backend packfs` selects it. No size threshold, no inode reduction and no O(packs) `List`/`Stats` (the loose mirror stays), and no pack compaction: its implemented win is the batched read | cas-core §4.14, §8 d12; performance §9 |
| **Compression layer** | Implemented | Opt-in `Codec[T]` wrappers in `cas/codec/gzip`, `cas/codec/zlib` and `cas/codec/flate`: compress serialized bytes without changing `Digest`, object types, or the core store semantics | cas-core §4.6 |
| **Pack helpers** | Implemented | `cas/pack` provides fixed-size payload splitting and JSON sidecar metadata for large-object and operational workflows without changing object identity | performance §10 + cas-core §7.2 |
| **Encryption layer** | Deferred | `EncryptedCodec[T]` wrapping `Codec[T]` with AES-256-GCM; app supplies the key — the core never generates/stores keys | cas-core §8 (follow-up 8); §4.6/§7.2 |
| **Content-defined chunking** | Deferred | Rolling-hash chunking of very large blobs for chunk-granular dedup | performance §10 |
| **Pack rewrite/compaction** | Deferred | Rewriting a pack to drop unreachable payloads (index rebuild, atomic swap) so a packed store reclaims space; today a sweep reclaims correctness, not space | performance §9; cas-core §4.14, §8 d12 |
| **Performance scenario harness** | Deferred | Opt-in end-to-end scale suite (`T-01…T-08`) with a runner such as `cmd/perftest` (or a `-tags=perftest` build) printing a `scenario / metric / target / result` table; **aspirational** — no runner, no nightly job and no benchmark CI gate exists | performance §11 |

**Deferral decision (2026-09): the performance scenario harness stays aspirational.** The repository ships only the manual, `CASK_SCALE_OBJECTS`-gated `BenchmarkScale*` and `BenchmarkViewerObjectsScale` probes; there is no scenario runner, no nightly workflow, and no benchmark gate (performance §5). Build it only when a real need appears, and decide separately whether it may become a required check.

**Status decision (2026-09-23):** packfiles are **shipped**, not deferred — `cas/backend/packfs` implements the byte contract with the `Cleaner`/`Statter`/`BatchGetter` capabilities, and `cask -backend packfs` selects it (cas-core §4.14). The earlier "packfiles remain deferred — no new core surface before v1.0.0" decision is withdrawn. The one genuinely unmet piece of the packfile design is the pack-rewrite GC, and it is **de-claimed** rather than promised: `cas.Sweep` preserves correctness on a packed store while the append-only packs keep the space, so compaction is an open follow-up and no spec claims it exists (cas-core §8 d12). Compression is implemented as a codec-layer optimization and remains opt-in; it is not a semantic change to the store or object model. Fixed-size chunking and manifest sidecars are implemented as helper packages, not new core doctrine. Encryption stays deferred until an app needs at-rest encryption; content-defined chunking is only for very large blobs with chunk-granular dedup. Below those triggers the per-object layout is the leaner choice.

**Rejected (2026-09): a namespace option (`fs.WithNamespace`).** Putting several stores in one root as `<base>/<namespace>/<fan-out>/<hex>` was rejected on four grounds: (1) the whole option reduces to `filepath.Join(root, name)` — which callers already have — plus a validator for a client-supplied path element (separators, `..`, absolute paths, Windows reserved names, case/NFC folding); (2) it reintroduces a runtime-chosen name used as a path element, the coupling the hash-agnostic core removed (cas-core §4.2); (3) it isolates nothing a separate base does not, because a store's isolation comes from the one-base exclusivity rule, not from a path segment (`List`/`Stats`/`Clean` walk the base recursively and match on the file name, so a prefix changes only where the walk starts — cas-core §4.4); (4) no consumer needs it — the CLI takes one `-store`, the viewer binds one backend, every example uses one store per base. Several stores under one root are already `fs.New(filepath.Join(root, name))`, and `fs.New` validates that base before creating it (`fs.ValidateBase`: no empty path, `.`, filesystem or volume root, or parent traversal — a nested directory is accepted, cas-core §4.4). Revisit only if a root-level *enumeration* need appears (one sweep or one stats view across many namespaces), which would be an app-layer root front-end over directories — not a byte-layer option.

### 3.1 Maintenance surface: specified, not implemented

These items are specified in an owning spec and have no implementation. Each entry states what exists today and what does not, so that no tick in an owning spec is read as a shipped capability; the design itself stays in the owning spec.

- **Quarantine of a broken object** (consistency §2, operations §4) — exists: `cas.Verify`
  returns `ErrDigestMismatch`, `cas.VerifyAll` collects the mismatching digests in
  `Report.Bad`, `cask verify --all` prints one `CORRUPT` line per bad object and exits 1, and
  the viewer records a session-scoped corrupt state plus one audit line. Does not exist:
  moving the bytes aside into a quarantine directory, and any alerting hook.
- **`ScanRefs`, the dangling-reference scan** (consistency §3, §8) — exists: a dangling
  reference fails `cas/repo.Walk`/`Reachable`, and the viewer's `Orphaned`/`Detached` states
  come from a host-supplied reachability function, not from a scan. Does not exist: a pass
  over the store that reports every reference whose target is not stored.
- **Sampled and scheduled `Verify`** (consistency §2, §6; operations §4) — exists: on-demand
  verification (`cask verify <hash>` or `--all`, and the viewer's Verify control). Does not
  exist: random sampling, a scheduler, or a nightly CI job (there is no nightly workflow).
- **Store-level metric counters and a viewer stats page** (operations §3,
  backend-architecture §7) — exists: `cas.Stats` (object count, total bytes) and the cache
  layer's `CacheStats`. Does not exist: objects/bytes operation counters, a login-throttle
  counter, or any stats route in the viewer.
- **Slow-operation (latency-threshold) logging** (operations §3) — exists: `log/slog` audit
  lines for login, throttle, CSRF, and verify, plus the CLI's plain-text summaries. Does not
  exist: any measurement of an operation's duration against a threshold.
- **Object descriptor + sidecar checksum** (`<base>/.meta/<digest>.json`) (operations §6) —
  exists: the rationale and a design sketch (`docs/design/object-descriptor-checksum.md`);
  `cas/pack` writes an unrelated string-map manifest for chunked payloads, and the old
  `examples/files` `.crc32` sidecar was deleted on purpose, because an object verifies from
  its own stored bytes alone (`examples/files/main_test.go` pins it). Does not exist: a
  descriptor producer, a descriptor reader, or a checksum-validation path.
- **Viewer delete/GC/prune routes** (consistency §4–§5, defaults §4) — deliberately not
  implemented: the viewer inspects and does not destroy (viewer-design §5, viewer-security
  §8), and object removal stays in the CLI (`cask gc`, `cask prune`), where it can be
  scripted and paired with the root list a sweep needs. A destructive route has to be
  designed against that rule before it can ship.
- **Dangerous all-objects prune** (consistency §5) — exists: `cask prune` requires a root
  list, defaults to `--dry-run`, and warns on `--min-age 0`. Does not exist: an explicit
  all-objects mode, a confirmation step, or a role gate.

**Deferral decision (2026-10):** the maintenance surface above stays deferred. Nothing in it
gates v1.0.0 — the store is correct without it (content addressing plus explicit
reclamation), and every item is additive behind the existing `Backend`/`cas` maintenance
contracts — so each item is unticked in its owning spec and recorded here instead of being
implemented now. Revisit per item when an operator needs it; implementing one is additive
work, not a redesign.

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
- [x] Catalog status matches the build: a shipped extension reads as implemented, a de-claimed guarantee is not promised, and a settled but unimplemented one reads as deferred (§3)
- [x] Every deferred maintenance entry (§3.1) states what exists today and what does not, and its owning spec carries no tick for it
