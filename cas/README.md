# cas — core package

The `cas` package is the generic, app-agnostic content-addressable store core. It stores raw bytes by digest, exposes the typed object layer on top, and intentionally leaves algorithm choice, codec choice, and backend choice to the caller.

## Architecture

- `Digest` + `Backend` — byte layer and object identity
- `Hasher` — caller-supplied hash algorithm
- `Codec[T]` + `Store[T]` + `Object[T]` — typed layer over the byte store
- `Walker[T]` + cache wrappers — traversal and performance extensions

## Recommended defaults

- Hash: `SHA-256` (`cas/hash/sha256`)
- Fast secure alternative: `SHA-512/256` (`cas/hash/sha512_256`)
- Codec: JSON (`cas/codec/json`) for readable, portable data
- Backend: `fs` (`cas/backend/fs`) for durable storage; `mem` (`cas/backend/mem`) for tests and ephemeral workloads
- Legacy/compatibility choices: `gob` (`cas/codec/gob`) is Go-only and opt-in; MD5 and SHA-1 are migration-only, not new CAS defaults

## Sub-layer docs

- [backend/README.md](./backend/README.md)
- [cache/README.md](./cache/README.md)
- [codec/README.md](./codec/README.md)
- [hash/README.md](./hash/README.md)

## Policy

The core stays generic and format-agnostic. Applications should pick a hash algorithm, a codec, and a backend that match their durability, interoperability, and performance needs; the core should not encode those choices into the library surface.
