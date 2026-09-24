---
type: Specification
title: Consistency — go-cask
description: The consistency model of the CAS store — broken vs dangling objects, Verify, garbage collection (mark-and-sweep from roots), age-based pruning, and the detection algorithms — informed by Git/IPFS/restic practices, deliberately simple.
version: v17
---

# Consistency — go-cask

The consistency model: what "broken" and "dangling" mean, how to detect them, and how GC + age-based pruning work — Git/IPFS/restic/S3-informed, kept minimal. Four maintenance operations, no machinery beyond that; a fifth, `ScanRefs`, is designed but not implemented (extensions §3). Related: `cas-core.md` §4.11, `operations.md`, `viewer-design.md`, `testing-strategy.md`.

## 1. Consistency model

Content addressing is self-verifying (the address IS the checksum). Two independent failure classes.

| State | Meaning | Detection |
|---|---|---|
| OK | bytes at `h` match `h`; every reference exists | — |
| Broken object | stored bytes no longer hash to the address (corruption/bit rot) | `Verify` (§2) |
| Dangling ref | object references an unstored hash (missing/deleted) | reference scan (§3) |
| Missing root | a pinned/root hash does not exist | `Exists(roots)` |

Store invariants (cas-core §2) rule out torn objects: `Put` is atomic (rename) and idempotent; reads are lock-free. Consistency is therefore **detection** of the two failure classes + **space reclamation** — never repair of torn writes.

## 2. Detecting broken objects (`Verify`)

- `cas.Verify(ctx, raw, d, hasher)` or `cas.NewVerifier(raw, hasher).Verify(ctx, d)` re-reads the bytes and recomputes the digest with the injected `Hasher` (the client owns the algorithm; a digest carries none); mismatch → `ErrDigestMismatch`.
- Variants that exist: **full scan** (`cask verify --all`, `cas.VerifyAll`, or the viewer's Verify control — all on demand) and **on-read** (call `cas.Verify` on the objects that matter, or verify on write-back for critical data). A **sampled scan** and a **scheduled full scan** (e.g. nightly) are designed but not implemented: nothing samples and the product ships no scheduler or cron job (extensions §3).
- Handling: report → audit-log. `cas.Verify` returns `ErrDigestMismatch`; `cas.VerifyAll` collects the mismatching digests in `Report.Bad`; `cask verify --all` prints one `CORRUPT <digest>` line per bad object and exits 1; the viewer records a session-scoped corrupt state and one audit line (`internal/web/verify.go`). **Quarantine** (moving the file aside) and alerting are designed but not implemented (extensions §3). The store never "fixes" a broken object — correct content must be re-`Put` (a new, valid hash).

## 3. Detecting dangling references

- A reference dangles when some object's `References()` contains `h` with `Exists(h) == false` (target deleted, GC'd, or never stored).
- Detection as implemented: there is **no** `ScanRefs` operation. A dangling reference surfaces where a walk cannot resolve it — `cas/repo.Walk`/`Reachable` fail with a wrapped resolution error instead of under-reporting the reachable set (`cas/repo/repo.go`), and `cas.Reachable` propagates the `RefLister` error (`cas/reachability.go`). The viewer's `Orphaned`/`Detached` states come from a host-supplied root-reachability function, not from a reference scan (`internal/web/objects.go`). A pass that reports every reference whose target is not stored is designed but not implemented (extensions §3); it stays O(refs) lock-free lookups when it lands (performance §2).
- Dangling refs are **diagnostics, not errors the core fixes**. The lazy resolver tolerates them (`ResolveAny` → not found); the viewer flags them explicitly. Repair is the application's job: re-`Put` the referencing object (a new hash) or re-pin the target.
- The core MUST NOT auto-delete objects merely because they dangle — that is GC's job, and GC removes only *unreachable* objects (§4).

## 4. Garbage collection (mark-and-sweep from roots)

**Model = Git's + IPFS's:** objects are kept while reachable from **roots**; the rest is reclaimable garbage.

- **Roots** are application-supplied pinned hashes (Git refs/branches, IPFS pins, Docker manifest digests; in `gitlike`, typically commit/tag hashes). `cas/refs.Store.Roots` is the concrete, library-provided source: every ref's current digest, ready to hand to `cas.Reachable` (single-type) or `cas/repo.Reachable` (cross-type, via a `Registry`) for expansion (library-design.md).
- **The reflog is history, not a root source.** `cas/refs.Store.Roots` returns each ref's *current* value only, so a sweep built from the documented root set reclaims every object that only `Log`/`Previous` still names: the reflog keeps handing the digest back while `Get` on it is `ErrNotFound`. That is deliberate — go-cask roots a ref's current value, not its history — and a caller that wants a recovery window MUST feed the `Log`/`Previous` digests into its root set explicitly, exactly the way it feeds `Roots`, before expanding it (Git roots its reflog via `gc.reflogExpire` for this reason; §7 records the divergence).
- **Algorithm** (`GC(ctx, reachable map[string]bool)`, cas-core §4.11): **(1) Mark** — walk `References()` from every root (BFS/DFS with a visited set, robust even against cycles), collect the reachable set (via `cas.Reachable`/`Walker[T]` for one type, or `cas/repo.Walk`/`Reachable` across several registered types); **(2) Sweep** — delete every object whose `h.String()` is not in the reachable set. `fs.Backend.GC` is the fs-native fast path; `cas.Sweep(ctx, raw, reachable, cas.SweepOptions{})` is the generic form that works against any backend, including one with no backend-native GC of its own (`packfs`; go-cask#137).
- **Unreachability and space are two different outcomes, and the sweep guarantees both only on `fs`/`mem`.** Mark-and-sweep's contract is that a swept object is gone from the store: `List` no longer reports it and `Get` is `ErrNotFound`. That holds on every backend. Reclaiming the *disk space* is a backend property, not a GC property: `fs` and `mem` unlink the bytes, while the packfile backend's `Delete` removes the loose object and the index record but leaves the payload in the append-only pack — a packed store grows with every `Put` and never shrinks on its own (cas-core §4.14, performance §9). No pack compaction is implemented or promised: see the de-claim in cas-core §8 d12.
- **When:** explicit only — `cask gc` at the CLI, or a job the operator schedules outside the product. The viewer has no GC route, and nothing in the product schedules or runs a sweep automatically (a store with no roots must not silently delete itself).
- **Concurrency:** sweeping unlinks files; a concurrent `Put` of a swept hash just re-creates it (idempotent, lock-free-safe); a reader holding an open FD keeps the bytes (POSIX). No GC-vs-write coordination within one process. Across OS processes, the **grace model** applies (cas-core §6): a sweep that MAY race a live writer MUST reclaim only objects older than a grace `--min-age` (the `cask` CLI `gc`/`prune` default 1h), so fresh writes survive. A forced `--min-age 0` sweep is the dangerous variant — safe only when no other process writes. Maintenance sweeps never run concurrently with each other (`cask` serializes via `.cask.lock`, cli §2).
- **Why not reference counting:** refcounts need a persisted, updated counter on every write — complexity and a drift source. Mark-and-sweep is stateless, correct by construction, cheap enough for a write-dominated store. (Git and restic both trace, not refcount.)

## 5. Age-based pruning (retention)

Removes **unreachable** objects older than a threshold (restic-retention/S3-lifecycle style).

- **Age source:** creation time ≈ first-`Put` time, from the backend's own per-object time — file mtime on `fs` (zero schema change). No metadata sidecar, no schema migration. **A backend that does not implement `cas.Statter` has no per-object age at all**, and `mem.Backend` is that case: `cas-core.md` §4.11 records that it satisfies neither `Cleaner` nor `Statter`, so an age-gated sweep against a mem-backed store returns `ErrUnsupported` and the unconditional sweep is its only retention option. The packfile backend implements `Statter` but reports its **pack file's** mtime, so a packed object ages by its pack rather than by its own first `Put` (cas-core §4.14) — age-gated retention over a packed store is therefore a coarse, pack-level grace, not a per-object one.
- **Operation:** `Prune(ctx, reachable map[string]bool, minAge time.Duration, dryRun bool)`: `reachable` MUST already be the complete, transitively-closed set of live digests, computed by the caller (`cas.Reachable`/`Walker[T]` for one type, or `cas/repo.Walk`/`Reachable` across several registered types) — Prune never expands references itself, exactly like `GC` (§4); delete objects absent from `reachable` AND older than `minAge`; `dryRun` returns the would-be-deleted set without deleting (default `true`; a real delete needs the explicit flag). `fs.Backend.Prune` is the fs-native fast path; `cas.Sweep(ctx, raw, reachable, cas.SweepOptions{MinAge: minAge, DryRun: dryRun})` is the generic form — it requires the backend to implement `cas.Statter` for age-based retention (`ErrUnsupported` otherwise; a backend without per-object modification times has no age to prune by).
- **Grace period is the point:** unreachable-young objects are kept, giving a recovery window after a bad unpin/delete (restic "keep recent even if unreachable"; S3 noncurrent-version expiration). Where the window comes from is backend-specific and worth saying plainly: `fs`/`packfs` get it from `--min-age` (per-object on `fs`, per-pack on `packfs`), while a mem-backed store has no age to gate on — there the only retention option is an unconditional sweep whose root set the app keeps, or a grace the app implements itself (`cas/refs` + `Roots` for what must survive).
- **Dangerous variant** (designed, not implemented — extensions §3): prune ALL objects older than T regardless of reachability, with an explicit all-objects mode, a confirmation step, and a role gate. It exists for legal/temp-data eviction and is the one op that can destroy reachable data, so it removes history and can break references. What exists today is `cask prune` with a required root list, `--dry-run` on by default, and a warning on `--min-age 0`.
- **Surface:** `cask` CLI `prune --min-age <dur> <roots...> [--dry-run]` (cli §2) — the CLI has no typed object model, so `<roots...>` is treated as the complete reachable set already (the CLI cannot expand a root into what it references; cli §2). The viewer exposes verify only (viewer-design §5); GC and prune have no HTTP surface (§9, backend-architecture §1).

## 6. Detection algorithms — options and chosen defaults

| Concern | Options | Chosen default |
|---|---|---|
| Broken objects | full scan / sample / on-read | on-demand full `Verify` (CLI/viewer) + on-read; sampling and scheduling deferred (extensions §3) |
| Dangling refs | on-write check / periodic scan / lazy only | lazy tolerance only — a typed walk fails on a dangling reference; a periodic scan (`ScanRefs`) is deferred (extensions §3) |
| Reachability | DFS/BFS from roots, refcounts, bloom tracing | BFS/DFS from roots with visited set |
| GC | mark-and-sweep, refcounts, pack rewrite | mark-and-sweep; pack rewrite **de-claimed** (not implemented, not required for correctness — performance §9, cas-core §8 d12) |
| Retention | age-based, keep-N, both | age-based (`minAge`) with dry-run |

Costs: full `Verify` O(bytes); reference scan O(refs) lock-free lookups; GC O(objects) per run. All background at scale (performance §8.1).

## 7. Principles borrowed from other CAS systems

| System | Practice adopted |
|---|---|
| Git | unreachable objects kept until explicit `gc`; refs as roots — but **not** Git's reflog rooting: `cas/refs` history is collectable (§4) |
| IPFS | pins as roots; GC deletes only unpinned objects |
| restic | snapshot roots + retention; keep recent unreachable data |
| S3 lifecycle | age-based object expiration |
| Docker registry | manifest digests as roots; GC walks manifests |

Deliberately **not** adopted (yet): persisted refcounts, bloom filters as a GC or reachability authority, pack compaction/space-reclaiming rewrite (the shipped sweep deletes the loose object and the pack index record but never rewrites a pack — performance §9, cas-core §4.14), distributed GC coordination. Optional advisory bloom pre-checks are allowed as a front-end optimization, but they never replace the store's reachability graph or `Verify`-driven correctness model.

## 8. Anti-over-engineering

The entire consistency surface is **four operations**: `cas.Verify(ctx, raw, d, hasher)` / `(*cas.Verifier).Verify(ctx, d)` (is this object intact?), `GC(reachable)` (delete everything absent from a caller-supplied, already-expanded reachable set), `Prune(reachable, minAge)` (delete objects absent from that same reachable set AND older than minAge, dry-run first), `Stats()` (what is stored: object count and total size — the core cannot group by algorithm, since a digest carries none). A fifth, `ScanRefs()` (which references dangle?), is designed but **not implemented**: what exists today is the abort a dangling reference causes in a typed walk (§3), and the deferral is recorded in extensions §3.

- No persisted refcounts, no incremental GC index, no automatic background GC, no GC-vs-write transactions, no distributed coordination.
- Content addressing + atomic writes remove most consistency problems by construction; the rest is detection + explicit reclamation.
- Future needs (pack rewriting, chunked GC) are added behind the same `Backend`/maintenance contracts — never a parallel model. Compaction in particular is a maintenance operation on one backend, not a change to mark-and-sweep: the reachable set stays the caller's (cas-core §4.14).

## 9. Where these live

- **Core (cas-core §4.11):** fs backend `Verify`/`GC`/`Stats`/`Prune`; retention policy in §5.
- **CLI (cli §2):** `verify`, `gc`, `prune`, `clean` in-process over the library; `prune` defaults to `--dry-run`; `clean` sweeps orphan `*.tmp` older than a threshold (operations §2).
- **Viewer (viewer-design.md):** integrity diagnostics only — `POST /viewer/objects/{hash}/verify` and `POST /viewer/objects/verify`. There is **no** delete, GC, or prune route in `internal/web/web.go`: the viewer inspects and does not destroy, so every operation that removes an object stays CLI-only (`cask gc`, `cask prune`), where it can be scripted, audited, and paired with the root list a sweep needs, and where a dry run means something. Byte-layer tool; does not surface typed references (viewer-design §7).

## 10. Checklist

- [x] `Verify` detects any single flipped byte (`ErrDigestMismatch`)
- [ ] Broken objects **quarantined** + audit-logged, never auto-"fixed": only the report/audit half exists (`ErrDigestMismatch`/`Report.Bad`, CLI `CORRUPT` line, viewer audit); quarantine and alert are deferred (extensions §3)
- [ ] Dangling **scan** (`ScanRefs`) O(refs) lock-free, reported as diagnostics: only the typed walk's abort and the viewer's host-supplied reachability exist today (extensions §3)
- [x] GC mark-and-sweep from app roots; explicit only
- [x] A sweep makes an object unreachable on every backend; space is reclaimed on `fs`/`mem` only, and the unpacked pack bytes of a packed store are documented rather than promised away (§4, performance §9)
- [x] `Prune(reachable, minAge, dryRun)` keeps unreachable-young objects as grace; dry-run default
- [x] Sweeps racing live writers grace-gated (`--min-age`); forced `--min-age 0` is the dangerous variant (cas-core §6)
- [ ] Dangerous all-objects prune with an explicit all-objects mode + confirm + role gate: what exists is `cask prune` with a required root list, `--dry-run` on by default, and a `--min-age 0` warning; the mode, the confirmation, and the gate are deferred (extensions §3)
- [x] No refcounts, no automatic GC, no GC transactions (§8)
- [x] Viewer exposes verify only; object removal (delete/GC/prune) has no viewer route and stays CLI-only (`cask gc`, `cask prune`) per §9, viewer-design §5, viewer-security §8
