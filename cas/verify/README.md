# verify

The `verify` layer keeps corruption checks explicit and separate from object addressing. A backend still stores raw bytes under a `cas.Digest`, while the caller chooses which hasher produces those digests and validates them.

## Why this layer exists

- `cas.Digest` remains the object identity key.
- `cas.Backend` stays storage-only and does not recompute or validate digests.
- `cas.Verify` and `cas.NewVerifier` perform maintenance checks with a caller-supplied `Hasher`.

## The constraint these helpers live under

`cas.Verify` recomputes the digest with the hasher it is given and compares it to the object's **address** (`cas/verifier.go`), after `hasher.Validate(d)` has rejected a digest of another width with `ErrInvalidDigest`. A checksum hasher therefore verifies only objects that were **addressed with that same checksum**: `crc32` cannot validate an object whose key is a SHA-256 digest, and the attempt fails before a byte is read.

These packages are `cas.Hasher` implementations for a store deliberately addressed by a checksum — fixtures, benchmarks, and data imported from systems that key by checksum. `benchmarks/verify_bench_test.go` does exactly that: it addresses each object with the checksum hasher (`h.Digest(...)` → `backend.Put(ctx, h, ...)`) and verifies it with the same one. They are **not** a cheap extra check layered over a strongly-addressed store.

## A cheap check over a strongly-addressed store

That other direction is its own package, [`sidecar`](./sidecar/README.md): it decorates a `cas.Backend` and records the checksum of each object's stored bytes at `<base>/.meta/<hex>.json`, beside objects whose address is the caller's strong hash. Validation compares the recomputed checksum with the **record**, never with the address, so an object with no record is reported as unchecked (`cas.ErrNotFound`/`sidecar.ErrUnrecorded`) rather than corrupt, and a record written by another algorithm is `sidecar.ErrChecksumAlgorithm` rather than corruption (operations §6). `cask verify --checksums` reads the records; `cask gc`/`prune` reconcile them after a sweep.

The three hashers below are unchanged by that: each remains an addressing hasher, and `sidecar` is what can turn one of them into a cheap second signal.

## Included helpers

All three implement `cas.Hasher` (`Digest`, `Validate`), report their width through `Size`, and are interchangeable in code:

- [crc32](./crc32/README.md) — CRC-32/IEEE, 4-byte digest
- [crc64](./crc64/README.md) — CRC-64/ECMA-182, 8-byte digest
- [adler32](./adler32/README.md) — Adler-32 (RFC 1950), 4-byte digest

The sibling maintenance layer is [sidecar](./sidecar/README.md) — not a hasher: a `cas.Backend` decorator that records and validates a per-object checksum.

## Choosing among the three

There is no correctness difference between them at this layer; the choice is which checksum's properties the caller needs. The same choice picks the algorithm of a recorded sidecar checksum.

- **`crc32`** — the ubiquitous one. Choose it when the checksum must be reproducible or cross-checked outside Go, because CRC-32/IEEE values are produced by almost every archive, storage and language toolchain.
- **`crc64`** — twice the width of the other two, so a lower chance of an accidental collision across a large or long-lived object set. Choose it when the checksum is the store's only integrity signal and the object count is high.
- **`adler32`** — defined in RFC 1950 (zlib's checksum) as two modulo-65521 sums, so it needs no polynomial table. Choose it when the computation cost matters more than the checksum's strength and the corruption to catch is accidental.

## Policy

Use this layer for consistency and corruption checks, not for choosing the authoritative object-address algorithm of durable data. For a durable content-addressed store, prefer a stronger hash such as `SHA-256`; use one of these hashers only when the store is deliberately addressed by that checksum, or as the recorded checksum of a [`sidecar`](./sidecar/README.md) record.
