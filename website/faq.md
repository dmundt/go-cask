# FAQ

## Why use go-cask instead of a database?

A database is a mutable, query-oriented system. go-cask is a content-addressable, immutable byte store with a digest-based identity model. It is useful when deduplication, stable object identity, and byte-level integrity matter more than relational queries.

## Why not use Git directly?

Git is excellent for version control and a specific object model. go-cask is smaller and more generic: it is a storage kit for Go applications that want to build their own object models on top of a content-addressable byte layer.

## Why are hashes and verification separate?

Identity and validation are different concerns. A digest identifies the object; verification checks whether the stored bytes still match the expected content.

## Can I plug in my own codec?

Yes. The codec seam is intentionally generic, and custom representation policies fit the design well.

## Which backend should I use?

The filesystem backend is a solid default for local and small-to-medium deployments. The same API can be backed by other storage engines without changing the typed application layer.

## Is go-cask a replacement for files?

No. It is a layer for stable object identity and integrity-aware storage. Filesystems are still useful for unstructured data and user-facing files; go-cask is a better fit when the data model is content-addressed and verifiable.

## Is go-cask a version-control system?

No. It is a reusable storage primitive for Go applications that want content-addressable behavior without taking on the full semantics of a Git-like workflow.
