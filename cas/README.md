# cas

`cas` is the generic, app-agnostic core of the CASK library. It stores raw bytes by digest, exposes a typed object layer above the byte store, and leaves hash choice, codec choice, and backend choice to the caller.

## Package overview

- `Digest` and `Backend` define the byte layer.
- `Hasher` is the caller-supplied algorithm seam.
- `Codec[T]`, `Store[T]`, and `Object[T]` define the typed layer.
- `Walker[T]` and the cache wrappers add traversal and read optimization.
- `VerifyAll`/`Sweep`/`Capabilities` are the generic, backend-agnostic maintenance layer — they work against any `Backend`, not just `fs`.

## Default policy

- Preferred hash: `SHA-256` via [hash/sha256](./hash/sha256/README.md)
- Full-width alternative: `SHA-512` via [hash/sha512](./hash/sha512/README.md)
- Fast secure alternative: `SHA-512/256` via [hash/sha512_256](./hash/sha512_256/README.md)
- Preferred codec: JSON via [codec/json](./codec/json/README.md)
- Optional compression layers: [codec/gzip](./codec/gzip/README.md), [codec/zlib](./codec/zlib/README.md), and [codec/flate](./codec/flate/README.md) for large or repetitive payloads
- Compact custom option: [codec/binary](./codec/binary/README.md) for stable per-type binary payloads
- Maintenance validation layer: [verify](./verify/README.md) with [crc32](./verify/crc32/README.md) for explicit, cheap consistency checks
- Recorded sidecar checksums (opt-in): [verify/sidecar](./verify/sidecar/README.md) — a `cas.Backend` decorator that writes a cheap per-object checksum to `<base>/.meta/<hex>.json` beside a strongly-addressed object and validates the stored bytes against it, so a CRC can act as a second signal without becoming the identity
- Durable backend: [backend/fs](./backend/fs/README.md)
- Test/ephemeral backend: [backend/mem](./backend/mem/README.md)
- Compatibility-only codec: [codec/gob](./codec/gob/README.md)
- Not shipped: MD5 and SHA-1; a legacy source needs a client-supplied `cas.Hasher`, and neither belongs in new content-addressed data

## Layer index

The core stack is intentionally layered: the storage layer stays authoritative, and optional optimization layers sit above it. When an optional layer is not enabled, the underlying store behaves exactly as before.

- [backend](./backend/README.md) — storage primitives: [fs](./backend/fs/README.md), [mem](./backend/mem/README.md), [packfs](./backend/packfs/README.md)
- [bloom](./bloom/README.md) — optional advisory bloom layer: [standard](./bloom/standard/README.md), [counting](./bloom/counting/README.md), [persistent](./bloom/persistent/README.md)
- [cache](./cache/README.md) — optional read-through caching and prefetch wrappers: [lru](./cache/lru/README.md), [mem](./cache/mem/README.md), [prefetch](./cache/prefetch/README.md)
- [codec](./codec/README.md) — object encoders and decoders: [json](./codec/json/README.md), [gzip](./codec/gzip/README.md), [zlib](./codec/zlib/README.md), [flate](./codec/flate/README.md), [binary](./codec/binary/README.md), [cbor](./codec/cbor/README.md), [gob](./codec/gob/README.md)
- [hash](./hash/README.md) — client-owned algorithm choices: [sha256](./hash/sha256/README.md), [sha512](./hash/sha512/README.md), [sha512_256](./hash/sha512_256/README.md)
- [pack](./pack/README.md) — canonical chunk + manifest helper layer for staged payload workflows
- [refs](./refs/README.md) — mutable named pointers ("refs") to a `cas.Digest`, with atomic writes and an append-only reflog
- [repo](./repo/README.md) — typed, cross-type object registry (`Registry`, `Walk`, `Reachable`) promoted from gitlike's example pattern
- [verify](./verify/README.md) — optional integrity/checksum helpers layered above the store: [crc32](./verify/crc32/README.md), [adler32](./verify/adler32/README.md), [crc64](./verify/crc64/README.md), plus [sidecar](./verify/sidecar/README.md) for a recorded per-object checksum
- [backend/packfs](./backend/packfs/README.md) — optional packfile backend for large append-only stores; distinct from the helper layer above

Optional layers such as Bloom sit above the authoritative `cas` core and provide probabilistic front-end checks without changing the underlying store semantics.

## Layering note

The project uses one canonical sentence: `cas/backend/fs` is the filesystem backend, `cas/backend/packfs` is the storage backend with a private pack index format, and `cas/pack` is the optional helper used by apps and examples, not by backend internals.

- `cas/backend/fs` is the authoritative raw byte store.
- `cas/backend/packfs` is a concrete storage policy that adds pack files and a private index on top of that byte store.
- `cas/pack` is a small utility layer for splitting payloads and encoding/decoding manifest metadata.

The helper layer is not a backend, and the backend is not a codec or object model.

## Policy

Keep the core generic and format-agnostic. Applications choose the algorithm, codec, and backend that match their durability, interoperability, and performance needs. The storage core should not hard-code those decisions.
