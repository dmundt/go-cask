# backend

The backend layer is the non-generic byte store beneath the typed `cas` API. It stores raw object bytes by `cas.Digest`, exposes the filesystem and in-memory implementations, and stays independent from any app object model or hash algorithm.

## Included implementations

- [fs](./fs/README.md) — durable filesystem backend
- [mem](./mem/README.md) — in-memory backend for tests and ephemeral workloads

## Policy

- The byte layer stores raw bytes by digest only.
- Hash choice stays with the caller via `cas.Hasher`.
- Codec choice stays with the caller via `Codec[T]`.
- A backend does not define object identity semantics or serialization format.
- Backends use the shared [options.go](./options.go) contract: each backend defines its own `With...` setters, all returning the common `backend.Option` type.

## Typical use

- Use `fs` for durable persistent storage.
- Use `mem` for tests, benchmarks, and quick examples.
- Keep the backend root dedicated to object data; do not nest stores or keep scratch temp files under it.

## Notes

This layer speaks only in bytes and digests. It does not know about JSON, gob, or any typed object model.
