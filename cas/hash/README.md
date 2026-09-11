# Hash layer — go-cask

See also: [cas/README.md](../README.md) for the wider package architecture and recommended defaults.

The hash layer is the client-side algorithm seam for the generic `cas` core. The core stores raw digests and does not know which algorithm produced them; the caller injects a `cas.Hasher` implementation when building a store.

## Included implementations

- [sha256/](./sha256) — default recommended hash for new durable CAS data
- [sha512_256/](./sha512_256) — supported fast secure alternative with a 256-bit output size

## Policy

- Use `SHA-256` for new durable content-addressed data.
- Use `SHA-512/256` when you want a fast secure alternative with the same 256-bit security level.
- Do not use `MD5` or `SHA-1` for new content-addressed data; they are migration-only or compatibility-only choices.

## Notes

- The core is hash-agnostic by design.
- A digest is just bytes; its algorithm is part of the caller's contract and documentation.
- Human-readable representations should be explicit, e.g. `sha256:<hex>` or `sha512_256:<hex>`.
