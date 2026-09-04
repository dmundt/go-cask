---
title: Consistency — go-cask
description: The consistency model of the CAS store — broken vs dangling objects, Verify, garbage collection (mark-and-sweep from roots), age-based pruning, and the detection algorithms — informed by Git/IPFS/restic practices, deliberately simple.
version: v7
---

# Consistency — go-cask

> How the store stays consistent: what "broken" and "dangling" mean, how to
> detect them, how garbage collection and age-based pruning work — built on
> the best practices of Git, IPFS, restic, and S3 lifecycle policies, kept to
> the simplest set that is still powerful. This is the document that makes
> CASK "the last CAS you need": one consistent model, five maintenance
> operations, no machinery beyond that.
>
> Related: `cas-core.md` §4.11 (Maintenance: `Verify`, `GC`),
> `operations.md` (integrity cadence, quarantine),
> `viewer-design.md` (diagnostics UI),
> `testing-strategy.md` (the CAS laws).

---

## 1. Consistency Model

Content addressing makes the store **self-verifying**: the address IS the
checksum. Two independent failure classes exist:

| State             | Meaning                                                        | Detection            |
| ----------------- | -------------------------------------------------------------- | -------------------- |
| **OK**            | bytes at `h` match `h`; every reference exists                 | —                    |
| **Broken object** | stored bytes no longer hash to the address (corruption, bit rot) | `Verify` (§2)       |
| **Dangling ref**  | an object references a hash that is not stored (missing/deleted) | reference scan (§3) |
| **Missing root**  | a pinned/root hash does not exist                              | `Exists(roots)`      |

Store invariants (from cas-core §2) guarantee there are no torn objects:
`Put` is atomic (rename) and idempotent, reads are lock-free. Consistency
work is therefore about **detecting** the two failure classes and **reclaiming
space** — never about repairing torn writes.

---

## 2. Detecting Broken Objects (`Verify`)

- `Verify(ctx, h)` re-reads the bytes and recomputes the hash with the
  algorithm from the address; mismatch → `ErrHashMismatch`.
- **Variants** (pick by cost):
  - **full scan** — every object; scheduled (nightly) or on demand; the
    definitive check;
  - **sampled scan** — random subset on `List`; cheap ongoing coverage;
  - **on-read** — verify while streaming a read; the strongest guarantee and
    the most expensive; use for critical objects only.
- **Handling** (operations §4): report → **quarantine** (move the file
  aside) → audit-log → alert. The store never "fixes" a broken object —
  the correct content must be re-`Put` (a new, valid hash).

---

## 3. Detecting Dangling References

- A reference dangles when `References()` of some object contains `h` with
  `Exists(h) == false` — the target was deleted, GC'd, or never stored.
- **Detection**: one pass over all objects; for each reference, a lock-free
  `Exists` check. Cost is O(refs) lookups, cheap because reads are lock-free
  (performance §2).
- **Handling**: dangling refs are **diagnostics, not errors the core fixes**.
  The lazy resolver already tolerates them (`ResolveAny` → not found). The
  viewer flags them explicitly (broken-link diagnostics, viewer-design §7).
  Repair is the application's job: re-`Put` the referencing object (a new
  hash) or re-pin the target.
- The core MUST NOT auto-delete objects just because they dangle — that is
  GC's job, and GC only removes *unreachable* objects (§4).

---

## 4. Garbage Collection (mark-and-sweep from roots)

**The model is Git's + IPFS's:** objects are kept while reachable from
**roots**; everything else is garbage the store may reclaim.

- **Roots** are pinned hashes the application supplies — the analogue of Git
  refs/branches, IPFS pins, or Docker manifest digests. In `gitlike` terms,
  roots are typically commit/tag hashes.
- **Algorithm** (the existing `GC(ctx, reachable map[string]bool)` contract,
  cas-core §4.11):
  1. **Mark** — walk `References()` from every root (BFS/DFS; a visited set
     is cheap and makes the walk robust even if a graph ever cycles);
     collect the reachable set (an app-side `Walker[T]`/`WalkGraph` helper
     produces this).
  2. **Sweep** — delete every object whose `h.String()` is not in the
     reachable set.
- **When**: explicit only — `POST /gc` (admin), CLI, or a scheduled job.
  Never automatic by default: a store with no roots must not silently
  delete itself.
- **Concurrency**: sweeping unlinks files; a concurrent `Put` of a swept hash
  simply re-creates it (idempotent, lock-free-safe); a reader holding an open
  FD keeps the bytes until it closes (POSIX). No GC-vs-write coordination is
  needed **within one process**. Across OS processes, the **grace model**
  applies (cas-core §6): a sweep that may race a live writer MUST reclaim
  only objects older than a grace `--min-age`, so recent writes survive the
  sweep — this is what the `cask` CLI does (`gc`/`prune` default 1h). A
  forced `--min-age 0` sweep is the dangerous variant: only safe when no
  other process is writing. Maintenance sweeps never run concurrently with
  each other (`cask` serializes via `.cask.lock`, cli §2).
- **Why not reference counting**: refcounts require a persisted, updated
  counter on every write — complexity and a source of drift. Mark-and-sweep
  is stateless, correct by construction, and cheap enough for a store where
  writes dominate deletes. (restic and Git both use tracing, not refcounts.)

---

## 5. Age-Based Pruning (retention)

Pruning removes **unreachable** objects older than a threshold — the store's
retention policy, in the spirit of restic's retention rules and S3 lifecycle
expiration.

- **Age source**: the object's **creation time ≈ first-`Put` time**, taken
  from the file mtime (`FSRawStore` — zero schema change) or a per-object
  timestamp map (`MemoryRawStore`). No metadata sidecar, no schema migration.
- **Operation**: `Prune(ctx, roots []Hash, minAge time.Duration,
  dryRun bool)`:
  1. mark reachable from roots (§4);
  2. delete objects that are **unreachable AND older than `minAge`**;
  3. `dryRun` returns the would-be-deleted set without deleting (default
     `true` for safety; a real delete requires the explicit flag).
- **The grace period is the point**: unreachable objects younger than
  `minAge` are kept, giving applications a recovery window after a bad
  unpin/delete. This is restic's "keep recent even if unreachable" and
  S3's noncurrent-version expiration.
- **Dangerous variant** (explicit, admin, dry-run + confirm): prune ALL
  objects older than T regardless of reachability — this removes history and
  can break references. Documented as the one operation that can destroy
  reachable data; it exists for legal/temp-data eviction.
- **Surface**: exposed as a `cask` CLI operation (`prune --min-age <dur>
  <roots...> [--dry-run]`, cli §2). The viewer exposes verify and GC
  admin actions (viewer-design §6); prune stays CLI-only — its dry-run
  semantics and root-based interface don't fit the hypermedia surface.
  There is no HTTP surface (backend-architecture §1).

---

## 6. Detection Algorithms — options and chosen defaults

| Concern          | Options                                        | Chosen default (simple)              |
| ---------------- | ---------------------------------------------- | ------------------------------------ |
| Broken objects   | full scan / sample / on-read                   | scheduled full `Verify` + sample on `List` |
| Dangling refs    | on-write check / periodic scan / lazy only     | periodic scan; lazy tolerance always |
| Reachability     | DFS/BFS from roots, refcounts, bloom tracing   | BFS/DFS from roots with visited set  |
| GC               | mark-and-sweep, refcounts, pack rewrite        | mark-and-sweep; pack rewrite deferred to performance §9 |
| Retention        | age-based, keep-N, both                        | age-based (`minAge`) with dry-run    |

Costs: full `Verify` is O(bytes); reference scan is O(refs) lock-free
lookups; GC is O(objects) per run. All are background operations at scale
(performance §8.1: `List`/`Stats`/scans are not hot paths).

---

## 7. Principles Borrowed from Other CAS Systems

| System          | Practice we adopt                                             |
| --------------- | ------------------------------------------------------------- |
| **Git**         | unreachable objects are kept until explicit `gc`; refs as roots |
| **IPFS**        | pins as roots; GC deletes only unpinned objects               |
| **restic**      | snapshot roots + retention rules; keep recent unreachable data |
| **S3 lifecycle**| age-based expiration of objects                               |
| **Docker registry** | manifest digests as roots; GC walks manifests              |

What we deliberately do **not** adopt (yet): persisted refcounts, bloom-filter
tracing, chunked pack GC (deferred with packfiles, performance §9),
distributed GC coordination.

---

## 8. Anti-Over-Engineering (the simplicity manifesto)

The entire consistency surface of the store is **five operations**:

```text
Verify(h)               # is this object intact?
ScanRefs()              # which references dangle?
GC(reachable)           # delete everything not reachable from roots
Prune(roots, minAge)    # delete unreachable objects older than minAge (dry-run first)
Stats()                 # what is stored, per algorithm
```

- No persisted refcounts, no incremental GC index, no automatic background
  GC, no GC-vs-write transactions, no distributed coordination.
- Content addressing + atomic writes remove most consistency problems by
  construction; the rest is detection + explicit reclamation.
- If a future problem genuinely needs more (pack rewriting, chunked GC), it
  is added behind the same `RawStore`/maintenance contracts — never as a new
  parallel model.

---

## 9. Where These Live

- **Core (cas-core §4.11)**: `FSRawStore.Verify`, `GC`, `Stats`; `Prune` is
  the age-based maintenance operation defined here.
- **CLI (cli §2)**: `verify`, `gc`, `prune`, `clean` operate in-process
  over the library; `prune` defaults to `--dry-run`; `clean` sweeps orphan
  `*.tmp` files older than a threshold (operations §2).
- **Viewer (viewer-design.md)**: integrity diagnostics
  (`Verify`); admin actions for verify/GC/prune with confirm. The viewer is
  a byte-layer tool and does not surface typed references (viewer-design
  §7).

---

## 10. Checklist

- [x] `Verify` detects any single flipped byte (`ErrHashMismatch`)
- [x] Broken objects are quarantined + audit-logged, never auto-"fixed"
- [x] Dangling-reference scan is O(refs) lock-free; reported as diagnostics
- [x] GC is mark-and-sweep from app-supplied roots; explicit only
- [x] `Prune(roots, minAge, dryRun)` keeps unreachable-young objects as a
      grace period; dry-run default
- [x] Sweeps racing live writers are grace-gated (`--min-age`); forced
      `--min-age 0` is the documented dangerous variant (cas-core §6)
- [x] The dangerous all-objects prune is admin + dry-run + confirm
- [x] No refcounts, no automatic GC, no GC transactions (§8)
- [x] CLI + viewer expose verify/GC/prune per the conventions (cli §2)
