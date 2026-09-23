# Backends

A `Backend` is the storage engine for raw bytes. It owns persistence and
retrieval only — never object semantics, codecs, or hashing.

```go
package backends

import (
    "context"
    "io"

    "github.com/dmundt/go-cask/cas"
)

// Backend is the six-method storage contract every backend implements.
type Backend interface {
    Put(ctx context.Context, d cas.Digest, r io.Reader) error
    Get(ctx context.Context, d cas.Digest) (io.ReadCloser, error)
    Exists(ctx context.Context, d cas.Digest) (bool, error)
    Delete(ctx context.Context, d cas.Digest) error
    List(ctx context.Context) ([]cas.Digest, error)
    Stats(ctx context.Context) (*cas.Stats, error)
}

// The contract above and the shipped cas.Backend accept exactly each other's
// implementations, so this listing cannot drift from the real interface.
var (
    _ cas.Backend = Backend(nil)
    _ Backend     = cas.Backend(nil)
)
```

## Shipped backends

| Package | Storage | Notes |
|---|---|---|
| `cas/backend/fs` | filesystem | durable, atomic writes, Git-like fan-out directories — the default |
| `cas/backend/mem` | in-memory | fast, deterministic, not persistent — tests and benchmarks |
| `cas/backend/packfs` | filesystem + pack files | opt-in (`packfs.New(dir, packfs.WithEnabled())`): the same loose objects, mirrored into append-only pack files with a JSON index; batched reads open each pack once |
| `cas/backend/snapshot` | portable archives | `snapshot.Export`/`snapshot.Import` stream every raw object between any two `Backend` implementations; an archive/migration helper, not a `Backend` |

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

## Optional capability interfaces

The six methods above are the whole mandatory contract. Maintenance operations
are layered on top and ask the backend what it can do instead of requiring
more methods, so a third-party backend opts into each capability by
implementing one small interface. `cas.CapabilitiesOf(backend)` reports the
result:

```go
package backends

import (
    "context"
    "time"

    "github.com/dmundt/go-cask/cas"
)

// Capabilities reports what a backend supports. Verify and Sweep are always
// true (cas.VerifyAll and cas.Sweep are generic and need only Get/List/Delete);
// Clean and Stat report whether the backend implements the optional interface.
type Capabilities struct {
    Verify bool
    Sweep  bool
    Clean  bool
    Stat   bool
}

// Cleaner is implemented by backends with their own orphaned scratch state.
type Cleaner interface {
    Clean(ctx context.Context, olderThan time.Duration) (int, error)
}

// Statter is implemented by backends that can report physical per-object
// metadata; Sweep uses ModTime for age-based retention.
type Statter interface {
    Size(ctx context.Context, d cas.Digest) (int64, error)
    ModTime(ctx context.Context, d cas.Digest) (time.Time, error)
}

var _ = cas.CapabilitiesOf

// The declarations above and the shipped ones are interchangeable, so this
// listing cannot drift from cas.
var (
    _ = cas.Capabilities(Capabilities{})
    _ cas.Cleaner = Cleaner(nil)
    _ Cleaner     = cas.Cleaner(nil)
    _ cas.Statter = Statter(nil)
    _ Statter     = cas.Statter(nil)
)
```

| Maintenance command | What it needs | Where it comes from |
|---|---|---|
| `cask verify` | `Get` + `List` | `cas.VerifyAll` — generic, every backend |
| `cask gc`, `cask prune` | `List` + `Delete` | `cas.Sweep` — generic, every backend |
| age-gated `gc`/`prune` (`-min-age`) | `Statter` | `Capabilities.Stat`; without it only an unconditional sweep is possible |
| `cask clean` | `Cleaner` | `Capabilities.Clean`; without it the command reports `ErrUnsupported` (for example `mem`, which leaves no scratch state) |

Two more optional interfaces are not part of `Capabilities`, because they
sharpen a read or a traversal rather than enabling a command:

- **`cas.BatchGetter`** — `GetMany(ctx, digests, fn)` serves a group of digests
  in the backend's own way, so `cas.GetMany` opens one pack or sends one
  request instead of one `Get` per object. `packfs` implements it; every
  backend works without it through the sequential `Get` fallback.
- **`cas.RefLister`** — `References(ctx, d)` returns the direct references of a
  stored object, so `cas.Reachable` can expand a root set over an
  otherwise-opaque byte store. A typed layer supplies it by wrapping its own
  lookup in `cas.RefListerFunc` or by implementing the one-method interface; a
  backend never needs to.

`cas/backend/fs` implements `Cleaner` and `Statter`, so it reports every
capability; `cas/backend/mem` implements neither and reports the two generic
ones. Call `cas.CapabilitiesOf` before offering a maintenance operation to a
user, rather than assuming a method exists.
