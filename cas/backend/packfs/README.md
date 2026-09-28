# packfs — optional packfile backend

Package `packfs` — opt-in, app-selected backend for large stores that need to coalesce many small
objects into pack files. A performance extension, not the default storage model: the default
backend remains `cas/backend/fs` ([fs](../fs/README.md)), and the pack layer is worth selecting
when a batch of reads is the dominant cost, because `GetMany` opens each pack once for the whole
group.

`cas/backend/packfs` is the storage backend with a private pack index format; `cas/pack` is the
app-level helper, never a backend internal. It extends the core CAS byte contract and never
replaces the digest model — a pack layout still uses `cas.Digest` as the object identity, and the
pack file only changes the on-disk layout.

## Policy

- Every object is written **twice**: the loose copy through the atomic fs path (the durable one)
  and an append-only pack record. There is no size threshold and no object that stays only loose.
- Nothing is filtered and nothing is compacted: `List`/`Stats` still walk the loose tree, inode
  count is unchanged, and `Delete` drops the pack index record without reclaiming the pack bytes
  — a packed store grows with every `Put` and never shrinks on its own.
- The pack index is rewritten in full after **every** packed `Put`: re-marshalled from the
  in-memory map and republished atomically, so a store with N packed objects pays O(N) allocation
  and I/O per put (O(N²) ingest overall). `WithPackMaxEntries` bounds the pack file, not the index.
  Measured with `BenchmarkPackIndexRewrite` (4 KiB per put, go1.27.1 windows/amd64): ≈ 78 allocs/op
  and 15 KB/op at 10 index entries, ≈ 2 070 allocs/op and 1.0 MB/op at 1 000, ≈ 20 100 allocs/op and
  8 MB/op at 10 000. The on-disk format is unchanged, and the publication is a short retry against
  the transient Windows rename failure `fs.Put` already tolerates.
- The base directory belongs to exactly one store: it holds the loose tree (`<base>/loose`), the
  pack directory (`<base>/packs`) and the pack index. `packfs.New` validates it with
  `fs.ValidateBase` before creating anything — an empty path, `.`, a filesystem or volume root,
  or a parent-traversal path is rejected; a nested directory is accepted.
- `Clean` sweeps both trees under that base: the loose tree through its own backend, the pack
  directory through `fs.CleanTemp`. One implementation and one `*.tmp`/`*.tmp.<n>` convention, so
  the two cannot disagree about what a leftover is, and a name outside the convention
  (`notes.tmp.old`) survives in either tree.
- This is a storage backend, not the chunk/manifest helper package in
  [../pack](../../pack/README.md). The helper layer splits payloads and writes manifest metadata;
  the backend stores durable append-only objects, and no backend internal imports the helper.

```go
backend, err := packfs.New("./store", packfs.WithEnabled(), packfs.WithPackMaxBytes(64<<20))
```

For normal durable use, or to save disk space or inodes, prefer `fs`.
