# FAQ

## Why not just use a database?

A database is a mutable, query-oriented system. CASK is a content-addressable, immutable byte store with a digest-based identity model. The design is useful when deduplication, stable object identity, and byte-level integrity matter more than relational queries.

## Why not use git directly?

Git is excellent for version control and a specific object model. CASK is smaller and more generic: it is a storage kit for Go applications that want to build their own object models on top of a content-addressable byte layer.

## Why are hashes and verification separate?

Identity and validation are different concerns. A digest identifies the object; verification checks whether the stored bytes still match expected content.

## Can I plug in my own codec?

Yes. The codec seam is intentionally generic, and custom representation policies fit the design well.

## Should I use the filesystem backend in production?

It is a solid baseline for local and small-to-medium deployments. For other environments, the same API can be backed by other storage engines without changing the typed application layer.
