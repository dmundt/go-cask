# cas

`cas` is the generic, app-agnostic core of the CASK library. It stores raw bytes by digest, exposes a typed object layer above the byte store, and leaves hash choice, codec choice, and backend choice to the caller.

## Package overview

- `Digest` and `Backend` define the byte layer.
- `Hasher` is the caller-supplied algorithm seam.
- `Codec[T]`, `Store[T]`, and `Object[T]` define the typed layer.
- `Walker[T]` and the cache wrappers add traversal and read optimization.
- Local agent notes: [AGENT.md](./AGENT.md)

## Default policy

- Preferred hash: `SHA-256` via [hash/sha256](./hash/sha256/README.md)
- Fast secure alternative: `SHA-512/256` via [hash/sha512_256](./hash/sha512_256/README.md)
- Preferred codec: JSON via [codec/json](./codec/json/README.md)
- Durable backend: [backend/fs](./backend/fs/README.md)
- Test/ephemeral backend: [backend/mem](./backend/mem/README.md)
- Compatibility-only codec: [codec/gob](./codec/gob/README.md)
- Legacy-only choices: MD5 and SHA-1; not for new content-addressed data

## Layer index

- [backend](./backend/README.md) — [fs](./backend/fs/README.md), [mem](./backend/mem/README.md)
- [cache](./cache/README.md) — [lru](./cache/lru/README.md), [mem](./cache/mem/README.md), [prefetch](./cache/prefetch/README.md)
- [codec](./codec/README.md) — [json](./codec/json/README.md), [gob](./codec/gob/README.md)
- [hash](./hash/README.md) — [sha256](./hash/sha256/README.md), [sha512_256](./hash/sha512_256/README.md)

## Policy

Keep the core generic and format-agnostic. Applications choose the algorithm, codec, and backend that match their durability, interoperability, and performance needs. The storage core should not hard-code those decisions.
