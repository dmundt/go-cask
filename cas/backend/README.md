# Backend layer — go-cask

See also: [cas/README.md](../README.md) and [cas/README.md](../README.md) for the wider package layout.

The backend layer is the non-generic byte store beneath the typed `cas` API. It owns raw object bytes keyed by `cas.Digest`, provides the filesystem and in-memory implementations, and is intentionally independent from any application object model or hash algorithm.

## What lives here

- [fs/](./fs) — durable filesystem backend
- [mem/](./mem) — in-memory backend for tests and ephemeral workloads

Other common backend patterns that follow the same byte-level contract include:
- local object storage such as S3-compatible object stores or MinIO
- embedded key/value stores such as Pebble, Badger, or Bolt
- a SQLite-backed backend for simple single-host persistence
- a journaled or append-only backend for streaming writes and crash recovery
- a networked backend over an RPC or gRPC layer

These are valid extensions of the byte-layer contract, but they all must preserve the same semantics: store bytes by digest, handle atomic writes correctly, and support the `Backend` API without introducing a typed object model into the storage layer.

## Policy

- The core stays agnostic: a backend stores bytes by digest only.
- The application chooses the hash algorithm through `cas.Hasher`.
- The application chooses the codec through `Codec[T]`.
- The backend is deliberately not the place where object identity semantics or serialization format are decided.

## Recommended use

- Use `fs` for durable persistent storage.
- Use `mem` for tests, benchmarks, quick examples, and transient in-process workloads.
- Do not nest stores under the same base or keep scratch temp files under a `cas` backend root; the backend owns the object namespace beneath its base directory.

## Notes

- `fs` is the stable default for durable storage.
- `mem` is not persistent and is not a replacement for a durable backend.
- This layer speaks in bytes and digests; it does not know about JSON, gob, or any typed object model.
