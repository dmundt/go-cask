# backend

The non-generic byte store beneath the typed `cas` API. Stores raw object bytes by `cas.Digest`,
exposes the filesystem (`fs`), in-memory (`mem`) and opt-in packfile (`packfs`) implementations,
and stays independent from any app object model or hash algorithm.

## Included implementations

- [fs](./fs/README.md) — durable filesystem backend
- [mem](./mem/README.md) — in-memory backend for tests, benchmarks, and ephemeral workloads
- [packfs](./packfs/README.md) — opt-in packfile backend: the same loose objects mirrored into
  append-only pack files, for batched reads

`cas/pack` ([pack](../pack/README.md)) is a helper for payload splitting and sidecar metadata,
**not** a backend; it does not implement `cas.Backend`.

## Policy

- The byte layer stores raw bytes by digest only; a backend does not define object identity
  semantics or serialization format.
- Hash choice stays with the caller via `cas.Hasher`; codec choice stays with the caller via
  `Codec[T]`.
- Backends use the [options.go](./options.go) contract: each backend declares its own `With...`
  setters and its own concrete `Option` type over its own config struct, so a cross-backend
  option is a compile-time error. The package itself shares only streaming plumbing
  (`ContextReader`, `WriteAll`, `ReadAll`, `ReadPayload`).

## Typical use

- Use `packfs` (`packfs.New(dir, packfs.WithEnabled())`, `cask -backend packfs`) when batched
  reads matter: it keeps the loose tree and mirrors every object into a pack, so it does not
  reduce inode count, does not speed up `List`/`Stats`, and never reclaims pack space on its own.
- Keep the backend root dedicated to object data; do not nest stores or keep scratch temp files
  under it.
