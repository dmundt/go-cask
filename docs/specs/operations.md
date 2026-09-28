---
type: Specification
title: Operations — go-cask
description: Running CASK in production — durability and fsync policy, crash recovery, observability (slog/metrics), integrity cadence, digest/layout migration, and backup guidance.
version: v21
---

# Operations — go-cask

Durable, observable, migratable CASK deployment. Related: `cas-core.md` (`Stats`/`Verify`/`GC`),
`viewer-security.md` (audit logging), `library-design.md` (`ErrDigestMismatch`).

## 1. Durability

- Write: `O_CREATE|O_EXCL` temp file (numeric suffix if another process holds `<path>.tmp`) → `f.Sync()` → `os.Rename`.
- Unique name → no shared temp inode, even cross-process.
- `f.Sync()` before rename → bytes on disk before visibility.
- Directory fsync after rename: optional, configurable (cost vs. durability); survives a crash.
- Atomic rename is the contract — no partial writes exposed.
- `packfs` durable object = the loose one: `packfs.Put` uses the fs temp-file→`Sync`→rename path, then appends to the active pack.
- Append = extra, non-fsynced copy: losing pack or index loses the packed view, not the object (cas-core §4.14).
- **Trust assumption: the store directory must not be writable by an actor the process does not trust** — a group- or world-writable store root, a shared volume, a store restored from an attacker-influenced backup, tarball or image, or a process running with more privilege than the directory's owner. The library does not re-verify digests on read (cas-core §4.3), so a store whose bytes a hostile actor can write cannot be trusted to hold honest bytes. The backends narrow the assumption instead of implying it: a symbolic link planted **inside** a base — a fan-out directory, an object's own name, `<base>/packs`, `<base>/loose` or `<base>/packs/current.pack` — is refused with `fs.ErrUnsafeTarget` before anything is created or appended (cas-core §4.4, §4.14). The **base itself** may be a symlink: it is resolved once where the store is opened (`internal/store.ResolveBase`), and the resolved directory is printed by `clean`/`gc`/`prune` and logged by `web`, so following it is deliberate and visible (cli.md §2).

## 2. Crash recovery

- Orphan `*.tmp`: ignored by `List`/`Stats`; a documented operation (e.g. `clean`) removes `*.tmp` older than a threshold.
- After a crash: `Verify` the store or a representative sample; restore from backup on mismatch.
- Packfile index is no recovery dependency: a stale record (pack missing, shorter than the record, outside `packs/`) is dropped on use; the object is re-read from the loose tree, which holds every object.
- **Deleted** `packs/index.json` → packed view only: the index repopulates as objects are written; nothing scans packs to rebuild it.
- **Malformed** `packs/index.json` → refused at construction (`packfs.New` fails decoding rather than guessing); operator deletes or repairs it and reopens (cas-core §4.14).

## 3. Observability

- **Structured logging (`log/slog`), implemented.** Viewer: login, throttle and CSRF rejections, verification results (`internal/web/auth.go`, `internal/web/verify.go`); `examples/api`: put/delete/verify/GC mutations with the affected hash; `cask web`: lifecycle errors.
- **Slow-operation line (latency threshold), designed, not implemented:** no code path measures duration against a threshold (extensions §3).
- **Metrics counters, implemented.** `cas.Stats`: object count and total bytes; the cache layer: `CacheStats` (hits/misses/loads/evicts, `cas/cache/mem`).
- **Store-level counters (objects stored/read/deleted, bytes in/out, login-throttle count), read-only viewer stats page, designed, not implemented:** no stats route (`internal/web/web.go`; extensions §3).
- **Metrics dependency: rule.** Do NOT add one unless required (coding-guidelines §3); else expose a small interface the deployment implements.
- **Audit logging: rule.** Per `viewer-security.md` — never log tokens or secrets.

## 4. Integrity cadence

- `Verify` per read is expensive → write-back verify (re-read after `Put`) for critical data; full scan on an operator-chosen cadence.
- Nothing is scheduled or sampled: `cask verify <hash>|--all` and the viewer's Verify control are on demand.
- Scheduled full `Verify` (e.g. nightly), random-sample `Verify` during `List`: designed, not implemented (extensions §3); no nightly CI job.
- On mismatch: `ErrDigestMismatch` (or the digest in `Report.Bad`) plus an audit-log line.
- Quarantine (object moved aside), alerting: designed, not implemented (extensions §3).

## 5. Migration

- **Address and layout carry no algorithm.** `Digest` = raw bytes at `<base>/<fan-out dirs>/<full hex digest>`; no algorithm name, no registry — one store, one format.
- **Algorithm migration** (e.g. `sha256` → `blake3`, a legacy SHA-1 store) = **client-side re-digest and rewrite** — no configuration switch, no library operation. Byte layer:
- Pipeline: `List` every digest → `Get` each object's bytes → re-digest with the target `cas.Hasher` → `Put` under the new digest → `Verify` **each** target object through that hasher → delete the source **only** after its replacement verifies.
- No per-algorithm filter: `Backend.List(ctx)` returns every digest; `Backend.Stats` reports only `ObjectCount`/`TotalSize`.
- **Objects stored before the digest change are not migrated at all.** A blob (no reference fields) still decodes; any object whose payload holds a reference — tree, commit, tag — does not.
- Type names stay `@1`; reference payloads went from `"sha256:hexdigest"` to bare hex and the layout lost its algorithm directory. Symptoms:
  - `Get`/`Verify` on an old digest → `ErrNotFound`: file at `<base>/sha256/…`, the backend reads `<base>/…`, yet `List`/`Stats` report that digest (the walk matches the file *name* at any depth, cas-core §4.4) — nothing fetchable, `GC`/`Prune` reclaim nothing.
  - At the canonical path, decoding fails with `ErrCorrupt`: strict hex parsing rejects the legacy `sha256:` prefix instead of resolving to another address.
- No migration tool, no `@2` type: the previous build decodes those objects, the current build re-creates the values, the old store stays read-only until then (versioning §4).
- Never point the current backend at a *parent* of an old store — phantom entries (cas-core §4.4, one base = one store).
- **Layout migration** (`FanOut`/`FanLevels` change): copy under the new layout, verify, remove the old (or keep both in transition, reads falling back to the old layout).
- Algorithm and layout transitions: offline or low-write; document the maintenance window.

## 5.1. Migration playbook (algorithm and layout moves)

- One base directory per store; every object path content-addressed; the old store stays a valid byte tree until the new one validates — offline rewrite with snapshot + rollback.

### 5.1.1 Algorithm migration example (`sha256` → `sha512_256`)

1. Freeze writes and create a snapshot.

```bash
cp -a ./store ./store-backup-$(date -u +%Y%m%dT%H%M%SZ)
find ./store -name '*.tmp' -delete
```

2. Create the target store and a one-off rewrite helper (project-local; `/tmp` fine for one migration): rehashes each object under the new algorithm, writes under the new canonical layout, verifies before deleting the source value.

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

3. Verify before promotion: gate = the CI gate, `./scripts/verify.sh` plus a targeted test for the changed package. For a migration, check the target's objects one-by-one with the new hasher and verify the references graph.

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

- Same pattern for a path-layout rewrite; only the destination mapping changes.

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

- New layout canonical and verified → replace the store path in place; else recover from `./store-backup-*` before writes resume.
- Old data tree intact until the new one passes `Verify`; then flip the active root.
- No silent in-place migration in the library: operational rewrite plus verified cutover.

## 6. Object descriptor + sidecar checksum

- `cas/verify/sidecar`: **opt-in** sidecar record **above** the `Backend` contract (§6.4) — only when a caller wraps its backend; holds no objects, so deleting the record directory loses the cheap check, never an object.
- Purpose: a cheap second check on a store addressed by a strong hash — bytes re-read with a CRC while SHA-256 stays the identity.

### 6.1 Layout

```text
<base>/<fan-out>/<hex>        object bytes (the backend's own layout, unchanged)
<base>/.meta/<hex>.json       sidecar record for that digest
<base>/.meta/<hex>.<n>.tmp    atomic-write scratch, reclaimed by the backend's Clean
```

- `<base>`: backend's `BasePath()` — for `fs` the path passed to `fs.New`; for `packfs` the loose tree at `<base>/loose`, where its `List`, `Stats` and `Clean` operate.
- `.meta`: the one sanctioned resident under a store's base (cas-core §4.4) — outside both rules that make a base single-owner.
- `.json` suffix: keeps a record out of `List`/`Stats`, which read a digest from the last path element only.
- `.tmp` suffix: puts a crashed write inside the backend's own scratch reclamation.
- A record is never an object; an object without a record is never damage.

### 6.2 Record, version 1

```json
{
  "version": 1,
  "digest": "abcd…",
  "type": "blob@1",
  "codec": "json",
  "checksum_algo": "crc32",
  "checksum": "1a2b3c4d",
  "size": 4096,
  "created_at": "2026-10-01T12:00:00Z"
}
```

| Field | Rule |
|---|---|
| `digest`, `checksum` | bare lowercase hex (`cas.Digest` via `encoding.TextMarshaler`, cas-core §4.6) |
| `checksum` | the **stored bytes** — exactly what `Backend.Get` returns, one streaming pass; not a payload checksum (v1 claims no logical-payload layer) |
| `checksum_algo` | the writer's algorithm (e.g. `crc32.Name`), compared on read: crc32 and adler32 are both four bytes wide, so width alone cannot tell a wrong-algorithm read from corruption |
| `type`, `codec` | best-effort from a bounded prefix of the stored bytes; empty when those bytes are no go-cask envelope (a `snapshot` archive) or the envelope does not fit the captured prefix; a write never fails on an underivable optional field; the object is never buffered |
| `type`/`codec` overflow | write path bounded by the read cap: over `WithMaxRecordBytes`, the record is written without them (never required); still too large → `sidecar.ErrRecordTooLarge`, publishing nothing; a record the reader would refuse as `cas.ErrCorrupt` is therefore never written (go-cask#362) |
| `size` | the stored byte count; disagreement with the object's actual size is reported, never repaired |
| `created_at` | first-record time (UTC); a repeat `Put` of the same digest under the same algorithm leaves a valid record untouched — deterministic, creation time not refreshed |
| `references` | none in v1: only the typed layer knows `References()`, a byte-layer producer cannot derive it; the object type stays the authoritative traversal source |

### 6.3 Read path, failures and reconciliation

- Reader checks a record, never the address: `cas.Verify` with the addressing hasher stays the identity check; the two are independent.

| Condition | Result |
|---|---|
| record absent | `cas.ErrNotFound` (wrapped by `sidecar.ErrUnrecorded`) — unchecked object, never corruption |
| `checksum_algo` differs from the reader's configured name | `sidecar.ErrChecksumAlgorithm`; a reader change must not read as damage, the same reasoning as `cas.ErrCodecMismatch` |
| recomputed checksum or stored size differs | `cas.ErrCorrupt`, wrapped |
| record's `digest` disagrees with the object, or the record is unparseable, the wrong version, or larger than the read cap | `cas.ErrCorrupt` on read, never a silent skip |
| the same unusable record during a full pass (`VerifyAll`) | `VerifyReport.Unreadable`; the pass keeps checking the rest — one damaged record must not report the whole store unchecked (go-cask#362) |
| a `.json` name in `.meta` that is not a digest | not a record: `Keys`/`Reconcile` skip it, `Reconcile` reports it in `ReconcileReport.Foreign`, neither aborts (go-cask#362) |
| the inner backend returns before the reader reaches EOF | a loud error and **no** record: a checksum over partial bytes is worse than none |

- Ordering and crash rule: **object first, record second** — a crash leaves an object with no record (unchecked, never corrupt), as does a record the cap refuses (§6.2); `Reconcile` closes the gap.
- Record write: temp file → `f.Sync()` → rename inside `.meta`; directory sync opt-in (`WithDirSync`), matching the backend's configurable directory fsync (§1).
- `Reconcile` removes the record of every gone object, reports stored objects with no record; never deletes an object, never invents a record.
- A `.json` name it cannot read as a digest: skipped, reported foreign, never removed.
- `cask gc` and a non-dry `cask prune` run it after their sweep; the backend's `Clean` reclaims `.meta` scratch like any other temp file.

### 6.4 Why not in the TLV?

- The object digest already checksums the stored bytes; a payload checksum inside the TLV would join the identity — circular, computed over bytes containing it.
- The envelope stays stable; checksum metadata lives only **above** the `Backend` contract, outside the hash input (identity would otherwise stop matching the stored bytes).

### 6.5 Not implemented

- Deferred: quarantine of a mismatching object, alerting, a logical-payload (as opposed to stored-bytes) checksum, a `references` field in the record (extensions §3.1; consistency §2).
- This path reports; it never moves bytes aside, never repairs an object.

## 7. Backup

- Store = plain directory tree: standard tooling (tar/rsync/object-storage sync).
- Snapshot without quiescing: copy while running, then `Verify` the copy — atomic writes mean no partial objects, only possibly the newest ones.
- Dedup keeps backups small; `packfs` does **not**: it keeps the loose tree and mirrors every object into a pack — a backup copies the data twice, and a sweep never shrinks it.
- Choose `packfs` for read-open amortization, not backup or disk size (performance §9, cas-core §4.14).

## 8. Checklist

- [x] fsync-before-rename; directory fsync configurable
- [x] orphan `*.tmp` sweep
- [x] slog for the viewer's mutations/audit lines and login-throttle rejections; `examples/api` mutations and GC
- [ ] slow-operation (latency-threshold) logging — not implemented (extensions §3)
- [ ] store-level metric counters, read-only viewer stats page — not implemented; only `cas.Stats`, `CacheStats` (extensions §3)
- [x] verify on demand; mismatch → `ErrDigestMismatch`/`Report.Bad`, CLI `CORRUPT`, viewer audit
- [ ] quarantine on mismatch, alerting — not implemented (extensions §3)
- [x] migration procedures (algorithm, layout) with verify-before-delete; un-migrated digest break recorded
- [x] sidecar checksum, opt-in `cas/verify/sidecar` (`<base>/.meta/<hex>.json`, version 1): producer, reader, validation, `cask verify --checksums`, `cask gc`/`prune` reconciliation (§6); quarantine, alerting, logical-payload checksum, `references` deferred (extensions §3.1)
- [x] backup documented; packfile backend's doubled footprint and missing reclamation (performance §9)
