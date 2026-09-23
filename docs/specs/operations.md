---
type: Specification
title: Operations — go-cask
description: Running CASK in production — durability and fsync policy, crash recovery, observability (slog/metrics), integrity cadence, digest/layout migration, and backup guidance.
version: v14
---

# Operations — go-cask

How a CASK-backed deployment stays durable, observable, and migratable. Related: `cas-core.md` (`Stats`/`Verify`/`GC`), `viewer-security.md` (audit logging), `library-design.md` (`ErrDigestMismatch`).

## 1. Durability

- Writes: uniquely named temp file (created `O_CREATE|O_EXCL`; a numeric suffix is appended if another process holds `<path>.tmp`) → `f.Sync()` → `os.Rename`. The unique name means concurrent writers (even across processes) never share a temp inode; `f.Sync()` before rename guarantees data is on disk before it becomes visible.
- Optional full durability: fsync the containing directory after rename so the rename survives a crash; make it configurable (cost vs. durability trade-off).
- The store never exposes partial writes — atomic rename is the contract.
- **The packfile backend's durable object is the loose one.** `packfs.Put` writes the object through the fs backend's temp-file→`Sync`→rename path and *then* appends it to the active pack, so the fsync contract above still holds and the pack append is an additional, non-fsynced copy. Losing the pack (or its index) loses the packed view, not the object (cas-core §4.14).

## 2. Crash recovery

- Orphan `*.tmp` files (crash mid-write) are ignored by `List`/`Stats`; provide a documented maintenance operation (e.g. `clean`) removing `*.tmp` files older than a threshold.
- After a crash: run `Verify` over the store (or a representative sample) to detect corruption; restore from backup on mismatch.
- **The packfile index is not a recovery dependency.** A stale record (pack missing, shorter than the record, outside `packs/`) is dropped on use and the object is re-read from the loose tree, which holds every object. A **deleted** `packs/index.json` therefore costs the packed view only — the index repopulates as objects are written and nothing scans packs to rebuild it; a **malformed** one is refused at construction (`packfs.New` fails decoding it rather than guessing), so the operator deletes or repairs the file and reopens (cas-core §4.14).

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

## 5.1. Migration playbook (algorithm and layout moves)

The repo keeps a single base directory per store, and every object path is content-addressed; that matters during a digest algorithm or fan-out migration because the old store is still a valid byte tree until the new one is validated. Treat the move as an offline rewrite with a snapshot and a rollback path.

### 5.1.1 Algorithm migration example (`sha256` → `sha512_256`)

1. Freeze writes and create a snapshot.

```bash
cp -a ./store ./store-backup-$(date -u +%Y%m%dT%H%M%SZ)
find ./store -name '*.tmp' -delete
```

2. Create the target store and a one-off rewrite helper. The exact helper is project-local and can live in `/tmp` for a single migration; the important part is that it rehashes each object under the new algorithm, writes under the new canonical layout, and verifies before deleting the source value.

```bash
mkdir -p ./store-next
cat >/tmp/cask-rehash.go <<'EOF'
package main

import (
  "bytes"
  "crypto/sha512"
  "encoding/hex"
  "fmt"
  "io/fs"
  "os"
  "path/filepath"
)

func main() {
  if len(os.Args) != 3 {
    panic("usage: cask-rehash <src-store> <dst-store>")
  }
  src := os.Args[1]
  dst := os.Args[2]
  _ = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
    if err != nil || d.IsDir() { return err }
    data, err := os.ReadFile(path)
    if err != nil { return err }
    sum := sha512.Sum512(data)
    key := hex.EncodeToString(sum[:])
    target := filepath.Join(dst, key)
    if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { return err }
    return os.WriteFile(target, bytes.TrimSpace(data), 0o644)
  })
  fmt.Println("rewrite complete")
}
EOF

go run /tmp/cask-rehash.go ./store ./store-next
```

3. Verify the new store before promotion. The repo's verification gate is the same one used in CI: `./scripts/verify.sh` and a targeted test for the changed package. For a migration, also check the target store's objects one-by-one with the new hasher and verify the references graph.

```bash
./scripts/verify.sh
find ./store-next -type f | sort | head
```

4. Promote only after the target is clean.

```bash
mv ./store ./store-old
mv ./store-next ./store
```

5. Roll back if verification fails.

```bash
rm -rf ./store
mv ./store-old ./store
```

### 5.1.2 Layout migration example (`FanOut`/`FanLevels` change)

The same pattern applies to a path-layout rewrite; only the destination directory mapping changes. The safest path is:

```bash
cp -a ./store ./store-backup-$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p ./store-layout-next
find ./store -type f ! -name '*.tmp' -print | sort > /tmp/cask-layout-files.txt

while IFS= read -r src; do
  rel="${src#./store/}"
  dst="./store-layout-next/$rel"
  mkdir -p "$(dirname "$dst")"
  cp "$src" "$dst"
done < /tmp/cask-layout-files.txt

./scripts/verify.sh
```

If the new layout is canonical and verified, replace the old store path in place; otherwise recover from `./store-backup-*` before any writes resume. The core rule is unchanged: keep the old data tree intact until the new one passes `Verify`, then flip the active root. There is no silent in-place migration in the library; the migration is an operational rewrite plus a verified cutover.

## 6. Object descriptor + sidecar checksum

The core does not put a payload checksum inside the TLV envelope. The object digest is already the checksum of the stored bytes. A payload checksum is therefore an optional sidecar descriptor, stored above the `Backend` contract rather than inside the content-addressed payload itself.

### 6.1 Exact shape

The canonical raw object remains the backend bytes keyed by `Digest`, with the exact layout unchanged:

```text
<base>/<digest path>          // bytes with canonical content-address identity
<base>/.meta/<digest>.json    // sidecar descriptor, optional and not part of the hash input
```

A minimal descriptor record is:

```json
{
  "version": 1,
  "digest": "sha256:abcd...",
  "type": "blob@1",
  "codec": "json",
  "payload_checksum": "sha256:abcd...",
  "payload_size": 4096,
  "created_at": "2026-09-16T22:45:00Z",
  "references": ["sha256:dead...", "sha256:beef..."]
}
```

Rules:

- `digest` is the object key. It is the authoritative content-address identity.
- `payload_checksum` is a metadata checksum of the logical payload or of a chunked payload. For a whole-object store it is usually equal to `digest`; the descriptor stores it only so the metadata can be validated without re-decoding the whole content stream.
- The sidecar does not replace the object digest, does not change the `Digest` of the stored bytes, and does not alter the TLV bytes on disk.
- `Store[T]` or an app-level descriptor package writes it; `Backend` stores only bytes.

### 6.2 Why not in the TLV?

Putting the checksum into the object bytes would make it part of the object identity. That creates a circular dependency: the checksum must be computed over bytes that contain the checksum itself. The result is a different hash for a different payload and a store where object identity no longer matches the bytes you stored.

The repo therefore keeps the TLV envelope stable and keeps checksum metadata in a sidecar record outside the hash input.

### 6.3 Verification path

When a descriptor is present:

1. `Backend.Get` returns the bytes for `digest`.
2. the descriptor is read or lazily opened from `/.meta/<digest>.json`.
3. the payload checksum is recomputed from the logical payload or chunk stream and checked against `payload_checksum`.
4. the object is decoded and `Validate()` is enforced.
5. on any mismatch, the object is treated as `ErrCorrupt` and quarantined.

This pattern is useful for app metadata, chunked payload manifests, and large recovery metadata — not for the base object bytes, which are already digest-addressed.

## 7. Backup

- The store is a plain directory tree — back it up with standard tooling (tar/rsync/object-storage sync).
- Consistent snapshot without quiescing: copy while running, then `Verify` the copy — the atomic-write design guarantees the copy never contains partial objects, only possibly the newest ones.
- Dedup keeps backups small. The packfile backend does **not**: it keeps the loose tree and mirrors every object into a pack, so backing up a `packfs` store copies the data twice and a sweep never shrinks it. Choose `packfs` for read-open amortization, not for backup or disk size (performance §9, cas-core §4.14).

## 8. Checklist

- [x] fsync-before-rename enforced; directory fsync configurable
- [x] orphan `*.tmp` sweep documented/implemented
- [x] slog logging for mutations, login-throttle rejections, slow ops, GC runs
- [x] verify cadence defined; mismatch → quarantine + audit + alert
- [x] migration procedures (algorithm and layout) documented with verify-before-delete; the un-migrated digest break recorded
- [x] object descriptor + sidecar checksum defined as optional metadata above the backend, never in the hash input
- [x] backup procedure documented; the packfile backend's doubled on-disk footprint and missing space reclamation stated (performance §9)
- [x] backup procedure documented
