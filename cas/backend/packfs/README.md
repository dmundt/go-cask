# packfs — optional packfile backend

Package `packfs` provides an opt-in backend for large stores that need to coalesce many small objects into pack files.

This is a performance extension, not the default storage model. The default backend remains [fs](../fs/README.md), and the pack layer is intended only when a real workload crosses the large-store threshold described in the repo docs.

The project’s canonical distinction is: `cas/backend/fs` is the filesystem backend, `cas/backend/packfs` is the storage backend with a private pack index format, and `cas/pack` is the optional helper used by apps and examples, not by backend internals.

## Policy

- `packfs` is an extension over the core CAS byte contract; it is not a replacement for the digest model.
- The backend remains opt-in and app-selected.
- The default path stays loose-object `fs` storage for normal workloads.
- A pack layout still uses `cas.Digest` as the object identity; the pack file only changes the on-disk layout.
- This is a storage backend, not the chunk/manifest helper package in [../pack](../../pack/README.md). The helper package is for payload splitting and metadata; the backend is for durable append-only object storage.

## Typical use

```go
backend, err := packfs.New("./store", packfs.WithEnabled(), packfs.WithPackMaxBytes(64<<20))
```

Use this only when a workload is large enough to justify coalescing small objects into append-only pack files. For normal durable use, prefer `fs`.
