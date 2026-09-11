# hash

The hash layer is the client-side algorithm seam for the generic `cas` core. The core stores raw digests and does not know which algorithm produced them; the caller injects a `cas.Hasher` implementation when building a store.

## Included implementations

- [sha256](./sha256/README.md) — recommended default for new durable CAS data
- [sha512_256](./sha512_256/README.md) — fast secure alternative with a 256-bit output size

## Policy

- Prefer `SHA-256` for new durable content-addressed data.
- Use `SHA-512/256` when you want a fast secure alternative with the same 256-bit security level.
- Treat MD5 and SHA-1 as legacy or compatibility-only choices, not as new CAS defaults.

## Notes

The core stays hash-agnostic by design. A digest is just bytes; the algorithm is part of the caller's contract and documentation, and implemented explicitly in the selected hasher package.
