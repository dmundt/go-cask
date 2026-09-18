# Concepts

CASK is a content-addressable storage library for Go. The essential idea is simple: the hash of the bytes is the identity of the object.

## Why the model matters

- a byte sequence maps to one stable digest
- identical data is stored once
- object identity is content-based, not location-based
- integrity checks can be performed independently of the storage backend

## Main building blocks

- `Digest` — the content address
- `Hasher` — the algorithm used to derive a digest
- `Backend` — the storage engine for bytes
- `Codec[T]` — serialization for typed values
- `Store[T]` — typed access on top of storage

## Mental model

```mermaid
flowchart TB
    A[Application object] --> B[Codec[T]]
    B --> C[Bytes]
    C --> D[Hash algorithm]
    D --> E[Digest]
    E --> F[Backend]
    F --> G[Stored content]
```

This is what makes CASK flexible: the hash, codec, and backend are separate seams.
