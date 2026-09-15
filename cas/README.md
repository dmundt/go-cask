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
- Full-width alternative: `SHA-512` via [hash/sha512](./hash/sha512/README.md)
- Fast secure alternative: `SHA-512/256` via [hash/sha512_256](./hash/sha512_256/README.md)
- Preferred codec: JSON via [codec/json](./codec/json/README.md)
- Optional compression layers: [codec/gzip](./codec/gzip/README.md), [codec/zlib](./codec/zlib/README.md), and [codec/flate](./codec/flate/README.md) for large or repetitive payloads
- Compact custom option: [codec/binary](./codec/binary/README.md) for stable per-type binary payloads
- Durable backend: [backend/fs](./backend/fs/README.md)
- Test/ephemeral backend: [backend/mem](./backend/mem/README.md)
- Compatibility-only codec: [codec/gob](./codec/gob/README.md)
- Legacy-only choices: MD5 and SHA-1; not for new content-addressed data

## Layer index

The core stack is intentionally layered: the storage layer stays authoritative, and optional optimization layers sit above it. When an optional layer is not enabled, the underlying store behaves exactly as before.

- [backend](./backend/README.md) — storage primitives: [fs](./backend/fs/README.md), [mem](./backend/mem/README.md), [pack](./backend/pack/README.md)
- [bloom](./bloom/README.md) — optional advisory bloom layer: [standard](./bloom/standard/README.md), [counting](./bloom/counting/README.md), [persistent](./bloom/persistent/README.md)
- [cache](./cache/README.md) — optional read-through caching and prefetch wrappers: [lru](./cache/lru/README.md), [mem](./cache/mem/README.md), [prefetch](./cache/prefetch/README.md)
- [codec](./codec/README.md) — object encoders and decoders: [json](./codec/json/README.md), [gzip](./codec/gzip/README.md), [zlib](./codec/zlib/README.md), [flate](./codec/flate/README.md), [binary](./codec/binary/README.md), [gob](./codec/gob/README.md)
- [hash](./hash/README.md) — client-owned algorithm choices: [sha256](./hash/sha256/README.md), [sha512](./hash/sha512/README.md), [sha512_256](./hash/sha512_256/README.md)
- [chunk](./chunk/README.md) — fixed-size payload splitting/reassembly helper for large-object workflows
- [manifest](./manifest/README.md) — JSON sidecar metadata for app-level workflow hints

Optional layers such as Bloom sit above the authoritative `cas` core and provide probabilistic front-end checks without changing the underlying store semantics.

## Policy

Keep the core generic and format-agnostic. Applications choose the algorithm, codec, and backend that match their durability, interoperability, and performance needs. The storage core should not hard-code those decisions.
