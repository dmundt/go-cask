# FAQ

## Why use go-cask instead of a database?

A database is a mutable, query-oriented system. go-cask is a
content-addressable, immutable byte store with a digest-based identity model.
It is useful when deduplication, stable object identity, and byte-level
integrity matter more than relational queries or transactions.

## Why not use Git directly?

Git is excellent for version control with a specific, fixed object model.
go-cask is smaller and more generic: a storage kit for Go applications that
want to build their own object models on a content-addressable byte layer. The
`gitlike` package shows how to build a Git-like graph on top of it, but
go-cask itself has no branches, merges, or remotes.

## Does go-cask verify integrity automatically?

No. `Backend.Put`/`Get` store and return bytes for a digest; they never
recompute or check it. Verification is a separate, explicit step:
`cas.Verify` (or a `cas.Verifier`) re-reads an object and recomputes its
digest with the caller's `Hasher`, returning `ErrDigestMismatch` on
corruption. Call it on whatever cadence your application needs (on read, on a
schedule, or via `cask verify` in the CLI).

## Are objects mutable?

No. An object's digest is derived from its encoded bytes, so changing the
content changes the address. There is no in-place update: writing new content
produces a new digest, and the old object remains addressable until it is
explicitly deleted or garbage-collected.

## Can I plug in my own codec or hash algorithm?

Yes, both are small interfaces. `Codec[T]` needs `Encode`/`Decode`; `Hasher`
needs `Digest`/`Validate`. See the
[custom codec recipe](recipes/custom-codec.md) and the
[hashes concept page](concepts/hashes.md).

## Which backend should I use?

The filesystem backend (`cas/backend/fs`) is a solid default for local and
small-to-medium deployments: durable, atomic writes, Git-like fan-out
directories. The in-memory backend (`cas/backend/mem`) is for tests and
ephemeral use. The packfile backend (`cas/backend/packfs`, selected with
`cask -backend packfs`) keeps the loose objects and mirrors them into
append-only pack files, so a batch of reads opens each pack once — reach for it
when read-open cost dominates, not to save disk space or inodes, which it does
not. Any other storage engine works if it implements the six-method
`Backend` interface (`Put`/`Get`/`Exists`/`Delete`/`List`/`Stats`).

To move stored objects between two backends, `cas/backend/snapshot` provides a
portable `Export`/`Import` archive that works over any `Backend`; it is a
migration and diagnostics helper, not a storage engine of its own.

## Is go-cask a replacement for files?

No. It is a layer for stable object identity and integrity-aware storage.
Filesystems remain useful for unstructured or user-facing data; go-cask is a
better fit once the data model is content-addressed and you want dedup and
explicit verification.

## Is go-cask a version-control system?

No. It is a reusable storage primitive. Applications that want Git-like
semantics (commits, trees, tags) can build them on `Store[T]` — see
`gitlike` — but go-cask itself has no branch, merge, or history-rewriting
model.
