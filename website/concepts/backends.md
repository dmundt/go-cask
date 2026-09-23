# Backends

A `Backend` is the storage engine for raw bytes. It owns persistence and
retrieval only — never object semantics, codecs, or hashing.

```go
type Backend interface {
    Put(ctx context.Context, d Digest, r io.Reader) error
    Get(ctx context.Context, d Digest) (io.ReadCloser, error)
    Exists(ctx context.Context, d Digest) (bool, error)
    Delete(ctx context.Context, d Digest) error
    List(ctx context.Context) ([]Digest, error)
    Stats(ctx context.Context) (*Stats, error)
}
```

## Shipped backends

| Package | Storage | Notes |
|---|---|---|
| `cas/backend/fs` | filesystem | durable, atomic writes, Git-like fan-out directories — the default |
| `cas/backend/mem` | in-memory | fast, deterministic, not persistent — tests and benchmarks |
| `cas/backend/packfs` | filesystem + pack files | opt-in (`packfs.New(dir, packfs.WithEnabled())`): the same loose objects, mirrored into append-only pack files with a JSON index; batched reads open each pack once |

`cask -backend fs|packfs` selects the backend for every store operation
(`fs` is the default); `cask web` needs the `fs` backend, because the viewer
reads per-object physical metadata through the filesystem backend.

The packfile backend is a read-throughput option, not a space optimization:
it keeps every object both loose and packed, `List`/`Stats` still walk the
loose tree, and a sweep removes the object from the index without reclaiming
the pack bytes — packs are append-only and are never compacted.

Any other storage engine (object storage, a database, a network service)
works if it satisfies the interface above.

## Guarantees every backend must provide

- **Content-addressability** — the digest is the object identity; a backend
  never returns bytes different from those stored under a key.
- **Immutability** — repeated writes of identical content are safe; `Delete`
  of a missing object is a no-op.
- **Streaming** — implementations stream to/from `io.Reader`/`io.ReadCloser`
  rather than buffering whole objects.
- **Concurrent safety** — safe for concurrent use without external
  synchronization.
- **Stable errors** — a missing object returns `ErrNotFound`.

The backend does not recompute the digest on write or read: that check is the
separate, explicit `cas.Verify` layer described in
[hashes](hashes.md#identity-and-verification-are-different-concerns).
