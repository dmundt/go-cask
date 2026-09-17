# verify

The `verify` layer keeps corruption checks explicit and separate from object addressing. A backend still stores raw bytes under a `cas.Digest`, while the caller chooses an integrity strategy for validation.

## Why this layer exists

- `cas.Digest` remains the object identity key.
- `cas.Backend` stays storage-only and does not recompute or validate digests.
- `cas.Verify` and `cas.NewVerifier` perform maintenance checks with a caller-supplied `Hasher`.

## Included helpers

- [crc32](./crc32/README.md) — a lightweight maintenance-only validator for cheap integrity checks
- [crc64](./crc64/README.md) — a maintenance-only checksum helper built on CRC-64/ECMA-182
- [adler32](./adler32/README.md) — a lightweight maintenance-only validator for fast local corruption checks

## Policy

Use this layer for consistency and corruption checks, not for choosing the authoritative object-address algorithm. For durable content-addressed storage, prefer a stronger hash such as `SHA-256` and keep the verification helper explicit and optional.
