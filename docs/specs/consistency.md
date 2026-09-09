---
type: Specification
title: Consistency — go-cask
description: The consistency model of the CAS store — broken vs dangling objects, Verify, garbage collection (mark-and-sweep from roots), age-based pruning, and the detection algorithms — informed by Git/IPFS/restic practices, deliberately simple.
version: v8
---

# Consistency — go-cask

The consistency model: what "broken" and "dangling" mean, how to detect them, and how GC + age-based pruning work — Git/IPFS/restic/S3-informed, kept minimal. Five maintenance operations, no machinery beyond that. Related: `cas-core.md` §4.11, `operations.md`, `viewer-design.md`, `testing-strategy.md`.

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

- `Verify(ctx, h)` re-reads bytes and recomputes the hash with the address's algorithm; mismatch → `ErrHashMismatch`.
- Variants (pick by cost): **full scan** (every object; scheduled nightly or on demand; definitive), **sampled scan** (random subset on `List`; cheap coverage), **on-read** (verify while streaming; strongest but most expensive; critical objects only).
- Handling (operations §4): report → **quarantine** (move the file aside) → audit-log → alert. The store never "fixes" a broken object — correct content must be re-`Put` (a new, valid hash).

## 3. Detecting dangling references

- A reference dangles when some object's `References()` contains `h` with `Exists(h) == false` (target deleted, GC'd, or never stored).
- Detection: one pass over all objects; for each reference, a lock-free `Exists` check. O(refs) lookups, cheap because reads are lock-free (performance §2).
- Dangling refs are **diagnostics, not errors the core fixes**. The lazy resolver tolerates them (`ResolveAny` → not found); the viewer flags them explicitly. Repair is the application's job: re-`Put` the referencing object (a new hash) or re-pin the target.
- The core MUST NOT auto-delete objects merely because they dangle — that is GC's job, and GC removes only *unreachable* objects (§4).

## 4. Garbage collection (mark-and-sweep from roots)

**Model = Git's + IPFS's:** objects are kept while reachable from **roots**; the rest is reclaimable garbage.

- **Roots** are application-supplied pinned hashes (Git refs/branches, IPFS pins, Docker manifest digests; in `gitlike`, typically commit/tag hashes).
- **Algorithm** (`GC(ctx, reachable map[string]bool)`, cas-core §4.11): **(1) Mark** — walk `References()` from every root (BFS/DFS with a visited set, robust even against cycles), collect the reachable set (via an app-side `Walker[T]`/`WalkGraph`); **(2) Sweep** — delete every object whose `h.String()` is not in the reachable set.
- **When:** explicit only — `POST /gc` (admin), CLI, or scheduled job. Never automatic by default (a store with no roots must not silently delete itself).
- **Concurrency:** sweeping unlinks files; a concurrent `Put` of a swept hash just re-creates it (idempotent, lock-free-safe); a reader holding an open FD keeps the bytes (POSIX). No GC-vs-write coordination within one process. Across OS processes, the **grace model** applies (cas-core §6): a sweep that MAY race a live writer MUST reclaim only objects older than a grace `--min-age` (the `cask` CLI `gc`/`prune` default 1h), so fresh writes survive. A forced `--min-age 0` sweep is the dangerous variant — safe only when no other process writes. Maintenance sweeps never run concurrently with each other (`cask` serializes via `.cask.lock`, cli §2).
- **Why not reference counting:** refcounts need a persisted, updated counter on every write — complexity and a drift source. Mark-and-sweep is stateless, correct by construction, cheap enough for a write-dominated store. (Git and restic both trace, not refcount.)

## 5. Age-based pruning (retention)

Removes **unreachable** objects older than a threshold (restic-retention/S3-lifecycle style).

- **Age source:** creation time ≈ first-`Put` time, from file mtime (fs backend — zero schema change) or a per-object timestamp map (mem backend). No metadata sidecar, no schema migration.
- **Operation:** `Prune(ctx, roots []Hash, minAge time.Duration, dryRun bool)`: mark reachable from roots (§4); delete objects that are **unreachable AND older than `minAge`**; `dryRun` returns the would-be-deleted set without deleting (default `true`; a real delete needs the explicit flag).
- **Grace period is the point:** unreachable-young objects are kept, giving a recovery window after a bad unpin/delete (restic "keep recent even if unreachable"; S3 noncurrent-version expiration).
- **Dangerous variant** (explicit, admin, dry-run + confirm): prune ALL objects older than T regardless of reachability — removes history and can break references. Exists for legal/temp-data eviction; the one op that can destroy reachable data.
- **Surface:** `cask` CLI `prune --min-age <dur> <roots...> [--dry-run]` (cli §2). The viewer exposes verify/GC admin actions (viewer-design §6); prune stays CLI-only — dry-run semantics and root-based interface don't fit the hypermedia surface. No HTTP surface (backend-architecture §1).

## 6. Detection algorithms — options and chosen defaults

| Concern | Options | Chosen default |
|---|---|---|
| Broken objects | full scan / sample / on-read | scheduled full `Verify` + sample on `List` |
| Dangling refs | on-write check / periodic scan / lazy only | periodic scan; lazy tolerance always |
| Reachability | DFS/BFS from roots, refcounts, bloom tracing | BFS/DFS from roots with visited set |
| GC | mark-and-sweep, refcounts, pack rewrite | mark-and-sweep; pack rewrite deferred (performance §9) |
| Retention | age-based, keep-N, both | age-based (`minAge`) with dry-run |

Costs: full `Verify` O(bytes); reference scan O(refs) lock-free lookups; GC O(objects) per run. All background at scale (performance §8.1).

## 7. Principles borrowed from other CAS systems

| System | Practice adopted |
|---|---|
| Git | unreachable objects kept until explicit `gc`; refs as roots |
| IPFS | pins as roots; GC deletes only unpinned objects |
| restic | snapshot roots + retention; keep recent unreachable data |
| S3 lifecycle | age-based object expiration |
| Docker registry | manifest digests as roots; GC walks manifests |

Deliberately **not** adopted (yet): persisted refcounts, bloom-filter tracing, chunked pack GC (deferred with packfiles, performance §9), distributed GC coordination.

## 8. Anti-over-engineering

The entire consistency surface is **five operations**: `Verify(h)` (is this object intact?), `ScanRefs()` (which references dangle?), `GC(reachable)` (delete everything not reachable from roots), `Prune(roots, minAge)` (delete unreachable objects older than minAge, dry-run first), `Stats()` (what is stored, per algorithm).

- No persisted refcounts, no incremental GC index, no automatic background GC, no GC-vs-write transactions, no distributed coordination.
- Content addressing + atomic writes remove most consistency problems by construction; the rest is detection + explicit reclamation.
- Future needs (pack rewriting, chunked GC) are added behind the same `Backend`/maintenance contracts — never a parallel model.

## 9. Where these live

- **Core (cas-core §4.11):** fs backend `Verify`/`GC`/`Stats`/`Prune`; retention policy in §5.
- **CLI (cli §2):** `verify`, `gc`, `prune`, `clean` in-process over the library; `prune` defaults to `--dry-run`; `clean` sweeps orphan `*.tmp` older than a threshold (operations §2).
- **Viewer (viewer-design.md):** integrity diagnostics (`Verify`); admin actions for verify/GC/prune with confirm. Byte-layer tool; does not surface typed references (viewer-design §7).

## 10. Checklist

- [x] `Verify` detects any single flipped byte (`ErrHashMismatch`)
- [x] Broken objects quarantined + audit-logged, never auto-"fixed"
- [x] Dangling scan O(refs) lock-free; reported as diagnostics
- [x] GC mark-and-sweep from app roots; explicit only
- [x] `Prune(roots, minAge, dryRun)` keeps unreachable-young objects as grace; dry-run default
- [x] Sweeps racing live writers grace-gated (`--min-age`); forced `--min-age 0` is the dangerous variant (cas-core §6)
- [x] Dangerous all-objects prune is admin + dry-run + confirm
- [x] No refcounts, no automatic GC, no GC transactions (§8)
- [x] CLI + viewer expose verify/GC/prune per cli §2
