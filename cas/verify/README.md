# verify

Corruption checks explicit and separate from object addressing: a backend still stores raw bytes
under a `cas.Digest`, while the caller chooses which hasher produces those digests and validates
them.

## The constraint these helpers live under

- `cas.Digest` is the object identity key; `cas.Backend` stays storage-only.
- `cas.Verify`/`cas.NewVerifier` run maintenance checks with a caller-supplied `Hasher`: recompute
  the digest, compare it to the object's **address** (`cas/verifier.go`), after
  `hasher.Validate(d)` rejects another width with `ErrInvalidDigest`.
- A checksum hasher therefore verifies only objects **addressed with that same checksum**: `crc32`
  cannot validate an object whose key is a SHA-256 digest, and the attempt fails before a byte is
  read.
- These packages are `cas.Hasher` implementations for a store deliberately addressed by a checksum
  — fixtures, benchmarks, data imported from systems that key by checksum.
  `benchmarks/verify_bench_test.go` addresses each object with the checksum hasher
  (`h.Digest(...)` → `backend.Put(ctx, h, ...)`) and verifies with the same one.
- They are **not** a cheap extra check over a strongly-addressed store.

## A cheap check over a strongly-addressed store

- That other direction is [`sidecar`](./sidecar/README.md): a `cas.Backend` decorator recording each
  object's stored-bytes checksum at `<base>/.meta/<hex>.json`, beside objects addressed by the
  caller's strong hash.
- Validation compares the recomputed checksum with the **record**, never the address.
- So an object with no record is unchecked (`cas.ErrNotFound`/`sidecar.ErrUnrecorded`), not corrupt;
  a record written by another algorithm is `sidecar.ErrChecksumAlgorithm`, not corruption
  (operations §6).
- `cask verify --checksums` reads the records; `cask gc`/`prune` reconcile them after a sweep.

## Included helpers

All three implement `cas.Hasher` (`Digest`, `Validate`), report width through `Size`, and are
interchangeable in code:

- [crc32](./crc32/README.md) — CRC-32/IEEE, 4-byte digest
- [crc64](./crc64/README.md) — CRC-64/ECMA-182, 8-byte digest
- [adler32](./adler32/README.md) — Adler-32 (RFC 1950), 4-byte digest

Sibling maintenance layer: [sidecar](./sidecar/README.md) — not a hasher, a `cas.Backend` decorator
recording and validating a per-object checksum.

## Choosing among the three

No correctness difference at this layer; the choice is which checksum's properties the caller
needs. The same choice picks the algorithm of a recorded sidecar checksum.

- **`crc32`** — the ubiquitous one. Choose it when the checksum must be reproducible or
  cross-checked outside Go, because almost every archive, storage and language toolchain produces
  CRC-32/IEEE values.
- **`crc64`** — twice the width of the other two, so a lower chance of an accidental collision
  across a large or long-lived object set. Choose it when the checksum is the store's only integrity
  signal and the object count is high.
- **`adler32`** — defined in RFC 1950 (zlib's checksum) as two modulo-65521 sums, so it needs no
  polynomial table. Choose it when the computation cost matters more than the checksum's strength
  and the corruption to catch is accidental.

## Policy

- Use this layer for consistency and corruption checks, not for choosing the authoritative
  object-address algorithm of durable data.
- Prefer a stronger hash such as `SHA-256` there; use one of these hashers only when the store is
  deliberately addressed by that checksum, or as the recorded checksum of a
  [`sidecar`](./sidecar/README.md) record.
