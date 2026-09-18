# Content addressing

Content-addressable storage means the bytes determine the key. A value is identified by what it contains, not by where it lives.

```text
bytes -> hash -> digest -> object identity
```

A store can therefore answer questions like:

- is this object already present?
- what is the canonical byte sequence for this value?
- can I deduplicate data automatically?

## Why it matters

- identical payloads deduplicate naturally
- data is immutable by default
- object identity stays stable across copies and renames
- the data model remains simple and easy to audit

## A simple diagram

```mermaid
flowchart LR
    A["Bytes"] --> B["Hash function"]
    B --> C["Digest"]
    C --> D["Stable identity"]
    D --> E["Stored object"]
```

## In go-cask

The core stores bytes behind a digest. The object identity is not a database row or filename; it is the content-derived key. The application can then layer typed JSON, binary, or custom representations on top without altering the storage model itself.

This is the same conceptual model behind Git object storage and similar CAS systems: the object is identified by its content, not by a mutable pointer.
