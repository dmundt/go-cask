---
type: Specification
title: Operations — go-cask
description: Running CASK in production — durability and fsync policy, crash recovery, observability (slog/metrics), integrity cadence, digest/layout migration, and backup guidance.
version: v10
---

# Operations — go-cask

How a CASK-backed deployment stays durable, observable, and migratable. Related: `cas-core.md` (`Stats`/`Verify`/`GC`), `viewer-security.md` (audit logging), `library-design.md` (`ErrDigestMismatch`).

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
- On mismatch: return `ErrDigestMismatch`, quarantine the object (move aside), audit-log, alert.

## 5. Migration

- **The address carries no algorithm and the layout has no algorithm directory.** A `Digest` is raw bytes stored at `<base>/<fan-out dirs>/<full hex digest>`; the core names no algorithm and keeps no registry, so nothing in the store records which algorithm produced a key. A store is therefore effectively single-format, like a Git object database with one object format.
- **Algorithm migration** (e.g. `sha256` → `blake3`, or a legacy SHA-1 store) is a **client-side re-digest and rewrite**, not a configuration switch and not an operation the library performs for you. Run it at the byte layer with a client: `List` every digest → `Get` each object's bytes → digest them with the target `cas.Hasher` → `Put` under the new digest → `Verify` **each** target object through that hasher → and delete the source **only** after its replacement verifies. There is no registry to consult and no per-algorithm filter to lean on (`Backend.List(ctx)` returns every digest; `Backend.Stats` reports only `ObjectCount`/`TotalSize`).
- **Objects stored before the digest change are not migrated at all.** An object with no reference fields (a blob) still decodes; every object whose payload contains a reference — tree, commit, tag — does not. Object type names stay `@1`, but reference payloads changed from `"sha256:hexdigest"` to bare hex **and** the layout lost its algorithm directory. Expect the symptoms in this order. First, `Get`/`Verify` on an old digest returns `ErrNotFound` — the file sits at `<base>/sha256/…` while the current backend reads `<base>/…` — even though `List`/`Stats` still report that digest (the walk matches on the file *name* at any depth, cas-core §4.4): an old store looks populated but nothing in it is fetchable, and `GC`/`Prune` cannot reclaim those files. Then, once an object is at its canonical path, decoding it fails with `ErrCorrupt`, because strict hex parsing rejects the legacy `sha256:` prefix instead of resolving to a different address. There is no migration tool and no `@2` type. Keep the previous build available to decode those objects, re-create the values with the current build, and treat the old store as read-only until then (versioning §4). Never point the current build's backend at a *parent* of an old store — that turns its objects into phantom entries (cas-core §4.4, one base = one store).
- **Layout migration** (change `FanOut`/`FanLevels`): same procedure — copy under the new layout, verify, then remove the old (or keep both during a transition, with reads falling back to the old layout).
- Algorithm and layout transitions are offline or low-write operations; document the maintenance window.

## 6. Backup

- The store is a plain directory tree — back it up with standard tooling (tar/rsync/object-storage sync).
- Consistent snapshot without quiescing: copy while running, then `Verify` the copy — the atomic-write design guarantees the copy never contains partial objects, only possibly the newest ones.
- Dedup keeps backups small; consider packfiles (performance §9) before large-scale backup.

## 7. Checklist

- [x] fsync-before-rename enforced; directory fsync configurable
- [x] orphan `*.tmp` sweep documented/implemented
- [x] slog logging for mutations, login-throttle rejections, slow ops, GC runs
- [x] verify cadence defined; mismatch → quarantine + audit + alert
- [x] migration procedures (algorithm and layout) documented with verify-before-delete; the un-migrated digest break recorded
- [x] backup procedure documented
