---
type: Specification
title: Operations — go-cask
description: Running CASK in production — durability and fsync policy, crash recovery, observability (slog/metrics), integrity cadence, hash/layout migration, and backup guidance.
version: v7
---

# Operations — go-cask

How a CASK-backed deployment stays durable, observable, and migratable. Related: `cas-core.md` (`Stats`/`Verify`/`GC`), `viewer-security.md` (audit logging), `library-design.md` (`ErrHashMismatch`).

## 1. Durability

- Writes: uniquely named temp file (created `O_CREATE|O_EXCL`; a numeric suffix is appended if another process holds `<path>.tmp`) → `f.Sync()` → `os.Rename`. The unique name means concurrent writers (even across processes) never share a temp inode; `f.Sync()` before rename guarantees data is on disk before it becomes visible.
- Optional full durability: fsync the containing directory after rename so the rename survives a crash; make it configurable (cost vs. durability trade-off).
- The store never exposes partial writes — atomic rename is the contract.

## 2. Crash recovery

- Orphan `*.tmp` files (crash mid-write) are ignored by `List`/`Stats`; provide a documented maintenance operation (e.g. `clean`) removing `*.tmp` files older than a threshold.
- After a crash: run `Verify` over the store (or a representative sample) to detect corruption; restore from backup on mismatch.

## 3. Observability

- Structured logging (`log/slog`): mutations (store/delete/verify/gc) with affected hash and result; login-throttle rejections with caller IP; slow operations (latency above a threshold); GC runs (deleted count, duration).
- Metrics (counters): objects stored/read/deleted, bytes in/out, cache hits/misses (`CacheStats`), login-throttle count. Expose read-only via the viewer stats page and structured logs. Do NOT add a metrics dependency unless required (coding-guidelines §3); if it becomes necessary, expose a small interface the deployment implements.
- Audit logging follows `viewer-security.md`: never log tokens or secrets.

## 4. Integrity cadence

- `Verify` on every read is expensive; recommended: verify on write-back (re-read after `Put`) for critical data; scheduled full `Verify` (e.g. nightly); random-sample `Verify` during `List`.
- On mismatch: return `ErrHashMismatch`, quarantine the object (move aside), audit-log, alert.

## 5. Migration

- **One algorithm per build, and it is in every address.** The core implements exactly one hash algorithm (`sha256`, cas-core §4.2). A reference carries that name, so an object written by a build with another algorithm is *recognized* rather than misread (`ErrUnknownAlgorithm`) — but this build cannot read or verify it.
- **Algorithm migration** (e.g. `sha1` → `sha256`, or a future `sha256` → `blake3`): list objects with the source build, re-hash each under the target, write, `Verify` **each** target object, and only then delete the source. Never delete the source before the target verifies. This is a format transition with a maintenance window, not a configuration switch.
- **Layout migration** (change `FanOut`/`FanLevels`): same procedure — copy under the new layout, verify, then remove the old (or keep both during a transition, with reads falling back to the old layout).
- Both are offline or low-write operations; document the maintenance window.

## 6. Backup

- The store is a plain directory tree — back it up with standard tooling (tar/rsync/object-storage sync).
- Consistent snapshot without quiescing: copy while running, then `Verify` the copy — the atomic-write design guarantees the copy never contains partial objects, only possibly the newest ones.
- Dedup keeps backups small; consider packfiles (performance §9) before large-scale backup.

## 7. Checklist

- [x] fsync-before-rename enforced; directory fsync configurable
- [x] orphan `*.tmp` sweep documented/implemented
- [x] slog logging for mutations, login-throttle rejections, slow ops, GC runs
- [x] verify cadence defined; mismatch → quarantine + audit + alert
- [x] migration procedures (algorithm and layout) documented with verify-before-delete
- [x] backup procedure documented
