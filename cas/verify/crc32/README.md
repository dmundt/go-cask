# crc32

`crc32` is a CRC-32/IEEE checksum helper for go-cask. It implements `cas.Hasher`, so it is an **addressing** hasher as well as a validating one — see [the constraint](../README.md#the-constraint-these-helpers-live-under).

## Purpose

The storage model stays boring and stable: object identity remains a `cas.Digest`, and the backend is still a raw `Digest -> bytes` store. `crc32` gives a caller that deliberately addresses objects by CRC-32 a hasher to store and verify them with.

## Typical use

```go
backend := mem.New()
h := crc32.New()

d, err := h.Digest(bytes.NewReader(payload)) // the object's address IS its crc32
_ = backend.Put(ctx, d, bytes.NewReader(payload))

verifier := cas.NewVerifier(backend, crc32.New())
_ = verifier.Verify(ctx, d) // reads the object back and recomputes the same checksum
```

The address and the verifying hasher must be the same algorithm: `cas.Verify` compares the recomputed digest to `d`, so a CRC-32 digest never matches an object addressed by SHA-256 — `hasher.Validate` rejects the 32-byte address as `ErrInvalidDigest` before anything is read. `benchmarks/verify_bench_test.go` runs exactly the sequence above.

## Policy

Use CRC-32 for a deliberately checksum-addressed store, for fixtures, and for interchange with systems that key data by CRC-32. Keep a stronger hash such as `SHA-256` for durable content-address identity; a cheap check over such a store needs a per-object sidecar checksum, which the library does not implement (`extensions.md` §3.1). Choose `crc64` for a wider digest and `adler32` for the cheapest computation ([selection rule](../README.md#choosing-among-the-three)).
