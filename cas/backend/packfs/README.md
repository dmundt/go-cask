# packfs — optional packfile backend

Package `packfs` provides an opt-in backend for large stores that need to coalesce many small objects into pack files.

This is a performance extension, not the default storage model. The default backend remains [fs](../fs/README.md), and the pack layer is opt-in: it is worth selecting when a batch of reads is the dominant cost, because `GetMany` opens each pack once for the whole group.

The project’s canonical distinction is: `cas/backend/fs` is the filesystem backend, `cas/backend/packfs` is the storage backend with a private pack index format, and `cas/pack` is the optional helper used by apps and examples, not by backend internals.

## Policy

- `packfs` is an extension over the core CAS byte contract; it is not a replacement for the digest model.
- The backend remains opt-in and app-selected.
- The default path stays loose-object `fs` storage for normal workloads.
- A pack layout still uses `cas.Digest` as the object identity; the pack file only changes the on-disk layout.
- Every object is written **twice**: the loose copy through the atomic fs path (the durable one) and an append-only pack record. There is no size threshold and no object that stays only loose.
- Nothing is filtered and nothing is compacted: `List`/`Stats` still walk the loose tree, inode count is unchanged, and `Delete` drops the pack index record without reclaiming the pack bytes — a packed store grows with every `Put` and never shrinks on its own.
- The base directory belongs to exactly one store: it holds the loose tree (`<base>/loose`), the pack directory (`<base>/packs`) and the pack index. `packfs.New` validates it with `fs.ValidateBase` before creating anything — an empty path, `.`, a filesystem or volume root, or a parent-traversal path is rejected; a nested directory is accepted.
- This is a storage backend, not the chunk/manifest helper package in [../pack](../../pack/README.md). The helper package is for payload splitting and metadata; the backend is for durable append-only object storage.

## Typical use

```go
backend, err := packfs.New("./store", packfs.WithEnabled(), packfs.WithPackMaxBytes(64<<20))
```

Use this when read-open amortization pays: the loose objects are the source of truth and the packs exist so `GetMany` can serve a batch from one open per pack. For normal durable use, or to save disk space or inodes, prefer `fs`.
