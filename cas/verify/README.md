# verify

The `verify` layer keeps corruption checks explicit and separate from object addressing. A backend still stores raw bytes under a `cas.Digest`, while the caller chooses which hasher produces those digests and validates them.

## Why this layer exists

- `cas.Digest` remains the object identity key.
- `cas.Backend` stays storage-only and does not recompute or validate digests.
- `cas.Verify` and `cas.NewVerifier` perform maintenance checks with a caller-supplied `Hasher`.

## The constraint these helpers live under

`cas.Verify` recomputes the digest with the hasher it is given and compares it to the object's **address** (`cas/verifier.go`), after `hasher.Validate(d)` has rejected a digest of another width with `ErrInvalidDigest`. A checksum hasher therefore verifies only objects that were **addressed with that same checksum**: `crc32` cannot validate an object whose key is a SHA-256 digest, and the attempt fails before a byte is read.

These packages are `cas.Hasher` implementations for a store deliberately addressed by a checksum — fixtures, benchmarks, and data imported from systems that key by checksum. `benchmarks/verify_bench_test.go` does exactly that: it addresses each object with the checksum hasher (`h.Digest(...)` → `backend.Put(ctx, h, ...)`) and verifies it with the same one. They are **not** a cheap extra check layered over a strongly-addressed store. A per-object checksum stored *beside* an object addressed by SHA-256 is a different feature, and go-cask does not implement it: `extensions.md` §3.1 records the object-descriptor/sidecar-checksum path as specified but not built.

## Included helpers

All three implement `cas.Hasher` (`Digest`, `Validate`), report their width through `Size`, and are interchangeable in code:

- [crc32](./crc32/README.md) — CRC-32/IEEE, 4-byte digest
- [crc64](./crc64/README.md) — CRC-64/ECMA-182, 8-byte digest
- [adler32](./adler32/README.md) — Adler-32 (RFC 1950), 4-byte digest

## Choosing among the three

There is no correctness difference between them at this layer; the choice is which checksum's properties the caller needs.

- **`crc32`** — the ubiquitous one. Choose it when the checksum must be reproducible or cross-checked outside Go, because CRC-32/IEEE values are produced by almost every archive, storage and language toolchain.
- **`crc64`** — twice the width of the other two, so a lower chance of an accidental collision across a large or long-lived object set. Choose it when the checksum is the store's only integrity signal and the object count is high.
- **`adler32`** — defined in RFC 1950 (zlib's checksum) as two modulo-65521 sums, so it needs no polynomial table. Choose it when the computation cost matters more than the checksum's strength and the corruption to catch is accidental.

## Policy

Use this layer for consistency and corruption checks, not for choosing the authoritative object-address algorithm of durable data. For a durable content-addressed store, prefer a stronger hash such as `SHA-256`; use one of these hashers only when the store is deliberately addressed by that checksum.
