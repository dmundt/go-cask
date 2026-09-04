---
title: Operations — go-cask
description: Running CASK in production — durability and fsync policy, crash recovery, observability (slog/metrics), integrity cadence, hash/layout migration, and backup guidance.
version: v5
---

# Operations — go-cask

> How a CASK-backed deployment stays durable, observable, and migratable.
> Related: `cas-core.md` (`StoreStats`/`Verify`/`GC`), `viewer-security.md`
> (audit logging), `library-design.md` (`ErrHashMismatch`).

---

## 1. Durability

- Writes: uniquely named temp file (created with `O_CREATE|O_EXCL`; a
  numeric suffix is appended if another process already holds `<path>.tmp`) →
  `f.Sync()` → `os.Rename` (the reference contract). The unique name means
  concurrent writers — even across processes — never share a temp inode,
  and the `f.Sync()` **before** rename guarantees the data is on disk before
  it becomes visible.
- Optional full durability: fsync the containing directory after the rename
  so the rename itself survives a crash; make this configurable (cost vs.
  durability trade-off).
- The store never exposes partial writes — atomic rename is the contract.

---

## 2. Crash Recovery

- Orphan `*.tmp` files (left by a crash mid-write) are ignored by
  `List`/`Stats`; provide a documented maintenance operation (e.g. `clean`)
  that removes `*.tmp` files older than a threshold.
- After a crash: run `Verify` over the store (or a representative sample) to
  detect corruption; restore from backup if mismatches are found.

---

## 3. Observability

- Structured logging (`log/slog`):
  - mutations (store / delete / verify / gc) with affected hash and result
  - login-throttle rejections with caller IP
  - slow operations (latency above a threshold)
  - GC runs (deleted count, duration)
- Metrics (counters): objects stored/read/deleted, bytes in/out, cache
  hits/misses (`CacheStats`), login-throttle count. Expose read-only via the
  viewer stats page and structured logs; do NOT add a metrics dependency
  unless
  required (coding-guidelines §3) — if it becomes necessary, expose a small
  interface the deployment implements.
- Audit logging follows `viewer-security.md`: never log tokens
  or secrets.

---

## 4. Integrity Cadence

- `Verify` on every read is expensive; recommended cadence:
  - verify on write-back (re-read after `Put`) for critical data,
  - scheduled full `Verify` (e.g. nightly),
  - random-sample `Verify` during `List`.
- Hash mismatch handling: return `ErrHashMismatch`, quarantine the object
  (move it aside), audit-log, and alert.

---

## 5. Migration

- **Migration is optional and never required for reads.** The hash type is
  part of every reference (cas-core §2/§4.2): each object remains
  addressable under its own algorithm, so changing the store's write
  algorithm never makes the system useless — old objects keep working and
  several algorithms coexist in one store at any time.
- **Hash-algorithm migration** (e.g. `sha1` → `sha256`): list objects,
  re-hash each with the target algorithm, write, `Verify` **each** target
  object, and only then delete the source. Never delete the source before
  the target verifies. Both algorithms coexist during the transition; reads
  keep working the whole time.
- **Layout migration** (change `FanOut`/`FanLevels`): the same procedure —
  copy under the new layout, verify, then remove the old layout (or keep
  both during a transition, with reads falling back to the old layout).
- Both are offline or low-write operations; document the maintenance window.

---

## 6. Backup

- The store is a plain directory tree: back it up with standard tooling
  (tar/rsync/object-storage sync).
- Consistent snapshot without quiescing: copy while running, then run
  `Verify` over the copy — the atomic-write design guarantees the copy never
  contains partial objects, only possibly the newest ones.
- Dedup keeps backups small; consider packfiles (performance §9) before
  large-scale backup.

---

## 7. Checklist

- [ ] fsync-before-rename enforced; directory fsync configurable
- [x] orphan `*.tmp` sweep documented/implemented
- [ ] slog logging for mutations, login-throttle rejections, slow ops, and
      GC runs
- [ ] verify cadence defined; mismatch → quarantine + audit + alert
- [x] migration procedures (algorithm and layout) documented with
      verify-before-delete
- [x] backup procedure documented
