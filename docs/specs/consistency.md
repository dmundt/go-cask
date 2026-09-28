---
type: Specification
title: Consistency — go-cask
description: The consistency model of the CAS store — broken vs dangling objects, Verify, garbage collection (mark-and-sweep from roots), age-based pruning, and the detection algorithms — informed by Git/IPFS/restic practices, deliberately simple.
version: v20
---

# Consistency — go-cask

Broken vs dangling objects, detection, GC, pruning — minimal.
Four maintenance operations; fifth `ScanRefs` designed, not implemented (extensions §3).
Related: `cas-core.md` §4.11, `operations.md`, `viewer-design.md`, `testing-strategy.md`.

## 1. Consistency model

Self-verifying addressing (address IS checksum) → two failure classes.

| State | Meaning | Detection |
|---|---|---|
| OK | bytes at `h` match `h`; every reference exists | — |
| Broken object | bytes no longer hash to the address (corruption/bit rot) | `Verify` (§2) |
| Dangling ref | object references an unstored hash (missing/deleted) | reference scan (§3) |
| Missing root | a pinned/root hash does not exist | `Exists(roots)` |

- Invariants (cas-core §2): `Put` atomic (rename), idempotent; reads lock-free.
- Consistency = **detection** + **space reclamation**, never torn-write repair.

## 2. Detecting broken objects (`Verify`)

- `cas.Verify(ctx, raw, d, hasher)` / `cas.NewVerifier(raw, hasher).Verify(ctx, d)`: re-read bytes, recompute digest with injected `Hasher` (client owns algorithm, digest none); mismatch → `ErrDigestMismatch`.
- **On-read**: `cas.Verify` on objects that matter; write-back for critical data.
- Handling: report, audit-log. `cas.Verify` → `ErrDigestMismatch`; `cas.VerifyAll` → `Report.Bad`; `cask verify --all` → one `CORRUPT` line (`CORRUPT <digest>`), exit 1; viewer → session corrupt state, audit line (`internal/web/verify.go`).
- **Quarantine** (move file aside), alerting: unimplemented (extensions §3).
- Store never fixes a broken object — re-`Put` correct content (new, valid hash).

## 3. Detecting dangling references

- Dangles: `References()` names `h` with `Exists(h) == false` (deleted, GC'd, never stored).
- No `ScanRefs`. Dangling refs surface where a walk cannot resolve:
  - `cas/repo.Walk`/`Reachable`: wrapped resolution error, never under-report (`cas/repo/repo.go`).
  - `cas.Reachable`: propagates the `RefLister` error (`cas/reachability.go`).
  - Viewer `Orphaned`/`Detached`: host-supplied reachability function, not a scan (`internal/web/objects.go`).
- Dangling refs = **diagnostics, not errors the core fixes**: lazy resolver tolerates (`ResolveAny` → not found), viewer flags; repair is the app's job: re-`Put` the object (new hash) or re-pin the target.
- The core MUST NOT auto-delete objects merely because they dangle — GC removes only *unreachable* objects (§4).

## 4. Garbage collection (mark-and-sweep from roots)

**Model = Git's + IPFS's:** reachable from **roots** = kept, rest reclaimable.

| Rule | Detail |
|---|---|
| Roots | App pins: Git refs/branches, IPFS pins, Docker manifest digests, `gitlike` commit/tag hashes. `cas/refs.Store.Roots` → each ref's *current* digest, for `cas.Reachable` (one type) / `cas/repo.Reachable` (cross-type, `Registry`) to expand (library-design.md). |
| Reflog | Not a root source: sweeping the documented root set reclaims every object only `Log`/`Previous` names — their `Get` is `ErrNotFound`. Recovery window → caller MUST feed them into its root set, like `Roots`, before expanding (Git roots its reflog via `gc.reflogExpire`; §7 records the divergence). |
| Algorithm | `GC(ctx, reachable map[string]bool)` (cas-core §4.11). **(1) Mark**: walk `References()` from every root (iterative DFS, visited set, cycle-safe) → reachable set; `cas.Reachable`/`Walker[T]` (one type), `cas/repo.Walk`/`Reachable`: four adapters over one traversal `cas.WalkDigests` (cas-core §4.9). **(2) Sweep**: delete every object whose `h.String()` is not in it. `fs.Backend.GC` fs-native fast path; `cas.Sweep(ctx, raw, reachable, cas.SweepOptions{})` generic, any backend, even without native GC (`packfs`; go-cask#137). |
| Space | Swept object gone everywhere — `List` drops it, `Get` is `ErrNotFound`. *Disk space*: `fs`/`mem` unlink bytes; packfs `Delete` drops loose object + index record, payload stays in the append-only pack — a packed store grows per `Put`, never shrinks alone (cas-core §4.14, performance §9). |
| When | Explicit only — `cask gc` or an operator job; nothing automatic. |
| Concurrency | Unlinking is safe: a concurrent `Put` of a swept hash re-creates it (idempotent); open FD holds bytes (POSIX); no in-process coordination. Across processes the **grace model** applies (cas-core §6): a sweep that MAY race a live writer MUST reclaim only objects older than a grace `--min-age` (`cask` `gc`/`prune` default 1h); `--min-age 0` = dangerous, no other writer only. Maintenance sweeps never concurrent (`cask` serializes via `.cask.lock`, cli §2). |

## 5. Age-based pruning (retention)

Removes **unreachable** objects older than a threshold (restic/S3-lifecycle).

| Rule | Detail |
|---|---|
| Age | Creation ≈ first-`Put` time, backend per-object time — `fs` mtime (no schema change, sidecar or migration). No `cas.Statter` → no per-object age; `mem.Backend` is that case — `cas-core.md` §4.11: satisfies neither `Cleaner` nor `Statter`: an age-gated sweep on a mem-backed store returns `ErrUnsupported`. Packfs implements `Statter` but reports its **pack file's** mtime: an object ages by its pack (cas-core §4.14), a coarse pack-level grace. |
| Operation | `Prune(ctx, reachable map[string]bool, minAge time.Duration, dryRun bool)`: `reachable` MUST be the complete, transitively-closed live-digest set, caller-computed (§4); Prune never expands references. Deletes objects absent from `reachable` AND older than `minAge`; `dryRun` returns the would-be-deleted set, no delete (default `true`; delete needs the explicit flag). `fs.Backend.Prune` fs-native fast path; `cas.Sweep(ctx, raw, reachable, cas.SweepOptions{MinAge: minAge, DryRun: dryRun})` generic, requiring `cas.Statter` (`ErrUnsupported` otherwise). |
| Grace | Unreachable-young kept — bad-unpin/delete recovery window. Window per backend: `fs`/`packfs` from `--min-age` (per-object `fs`, per-pack `packfs`); mem-backed store has no age — unconditional sweep with an app-kept root set, or app-implemented grace (`cas/refs` + `Roots`). |
| Dangerous variant | Designed, not implemented (extensions §3): prune ALL objects older than T regardless of reachability, with explicit all-objects mode, confirmation, role gate — legal/temp-data eviction. Today: `cask prune` + required root list only. |
| Surface | `cask` CLI `prune --min-age <dur> <roots...> [--dry-run]` (cli §2) — no typed model: `<roots...>` is the complete reachable set. Viewer exposes verify only (viewer-design §5); GC and prune have no HTTP surface (§9, backend-architecture §1). |

## 6. Detection algorithms — options and chosen defaults

| Concern | Options | Chosen default |
|---|---|---|
| Broken objects | full scan / sample / on-read | on-demand full `Verify` (CLI/viewer) + on-read; sampling deferred (extensions §3) |
| Dangling refs | on-write check / periodic scan / lazy only | lazy tolerance only — typed walk fails on a dangling ref; periodic scan (`ScanRefs`) deferred (extensions §3) |
| Reachability | DFS/BFS from roots, refcounts, bloom tracing | BFS/DFS from roots with visited set |
| GC | mark-and-sweep, refcounts, pack rewrite | mark-and-sweep; pack rewrite **de-claimed** (not implemented, not required for correctness — performance §9, cas-core §8 d12) |
| Retention | age-based, keep-N, both | age-based (`minAge`) with dry-run |

- Costs: full `Verify` O(bytes); reference scan O(refs) lock-free (performance §2); GC O(objects) per run; all background at scale (performance §8.1).

## 7. Principles borrowed from other CAS systems

| System | Practice adopted |
|---|---|
| Git | unreachable objects kept until explicit `gc`; refs as roots — **not** Git's reflog rooting: `cas/refs` history collectable (§4) |
| IPFS | pins as roots; GC deletes only unpinned objects |
| restic | snapshot roots + retention; keep recent unreachable data |
| S3 lifecycle | age-based object expiration |
| Docker registry | manifest digests as roots; GC walks manifests |

- **Not** adopted (yet): bloom filters as a GC or reachability authority; pack compaction/space-reclaiming rewrite (performance §9, cas-core §4.14); persisted refcounts, distributed coordination (§8).
- Advisory bloom pre-checks allowed (front-end optimization), never replacing the reachability graph or `Verify` correctness model.

## 8. Anti-over-engineering

**Four operations**, no more: `cas.Verify(ctx, raw, d, hasher)` / `(*cas.Verifier).Verify(ctx, d)`;
`GC(reachable)`; `Prune(reachable, minAge)`; `Stats()`. Fifth, `ScanRefs()`: designed, **not
implemented** (§3; extensions §3).

- No persisted refcounts, no incremental GC index, no automatic background GC, no GC-vs-write transactions.
- Future needs (pack rewriting, chunked GC) go behind the same `Backend`/maintenance contracts, never a parallel model (cas-core §4.14).

## 9. Where these live

| Layer | Detail |
|---|---|
| Core (cas-core §4.11) | fs backend `Verify`/`GC`/`Stats`/`Prune`; retention policy in §5. |
| CLI (cli §2) | `verify`, `gc`, `prune`, `clean` in-process over the library; `prune` defaults to `--dry-run`; `clean` sweeps orphan `*.tmp` older than a threshold (operations §2). |
| Viewer (viewer-design.md) | `POST /viewer/objects/{hash}/verify` and `POST /viewer/objects/verify` only. No delete/GC/prune route in `internal/web/web.go`: viewer inspects, never destroys; removal stays CLI-only (`cask gc`, `cask prune`). Byte-layer tool; surfaces no typed references (viewer-design §7). |

## 10. Checklist

- [x] `Verify` detects any single flipped byte (`ErrDigestMismatch`)
- [ ] Broken objects quarantined + audit-logged, never auto-"fixed": report/audit half only; quarantine, alert deferred (extensions §3)
- [ ] Dangling scan (`ScanRefs`) O(refs) lock-free, diagnostics only: typed-walk abort + viewer reachability (extensions §3)
- [x] GC mark-and-sweep from app roots; explicit only
- [x] Sweep → unreachable every backend; space reclaimed on `fs`/`mem` only; packed store's unpacked bytes documented, not promised (§4, performance §9)
- [x] `Prune(reachable, minAge, dryRun)` keeps unreachable-young grace; dry-run default
- [x] Sweeps racing live writers grace-gated (`--min-age`); forced `--min-age 0` dangerous (cas-core §6)
- [ ] Dangerous all-objects prune (explicit mode + confirm + role gate) unimplemented: today only `cask prune`, required root list, `--dry-run` default, `--min-age 0` warning (extensions §3)
- [x] No refcounts, no automatic GC, no GC transactions (§8)
- [x] Viewer verify only; delete/GC/prune CLI-only (`cask gc`, `cask prune`) per §9, viewer-design §5, viewer-security §8
