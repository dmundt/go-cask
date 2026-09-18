# Content addressing

Content-addressable storage means the bytes determine the key: an object is
identified by what it contains, not by where it lives.

```text
encoded bytes -> hash -> digest -> object identity
```

A store can therefore answer:

- is this object already present? (`Exists`)
- what is the canonical byte sequence for this value?
- can identical data dedupe automatically? (`PutDedup`)

## Why it matters

- identical payloads dedupe naturally — same encoded bytes, same digest
- objects are immutable in practice: changing content changes the digest,
  there is no in-place update
- object identity stays stable across renames and storage moves
- the data model stays simple and easy to audit

## In go-cask

`Store.Put` encodes the value with a `Codec[T]`, wraps it in a small
self-describing envelope (see [object format](../specifications/object-format.md)),
and hashes the whole envelope with the caller's `Hasher`. The digest — not a
database row or a filename — is the object's identity. Different
applications can layer JSON, binary, or custom representations on the same
underlying model without changing how identity works.

This is the same conceptual model as Git's object store: content determines
address, not a mutable pointer. go-cask keeps the same idea but stays generic
about the object types on top.
