# crc32

`crc32` is a CRC-32/IEEE checksum helper for go-cask. It implements `cas.Hasher`, so it is an **addressing** hasher as well as a validating one — see [the constraint](../README.md#the-constraint-these-helpers-live-under).

## Purpose

The storage model stays boring and stable: object identity remains a `cas.Digest`, and the backend is still a raw `Digest -> bytes` store. `crc32` gives a caller that deliberately addresses objects by CRC-32 a hasher to store and verify them with.

## Typical use: a cheap check over a strongly-addressed store

This is what the recorded-checksum path is for: the object keeps its `SHA-256` address, and the CRC-32 of the stored bytes sits beside it.

```go
backend, _ := fs.New("store/objects")
rec, _ := sidecar.New(backend, sidecar.WithChecksum(crc32.Name, crc32.New()))
store := cas.New(rec, json.New[*Blob](), sha256.New())

blob := &Blob{Data: []byte("payload")}
d, _ := store.Put(ctx, blob) // address is SHA-256; the record holds CRC-32
err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d) // cheap check
```

The record lives at `<base>/.meta/<hex>.json`, the address is untouched, and an object with no record is reported as unchecked rather than corrupt (`operations.md` §6, [`sidecar`](../sidecar/README.md)).

## Typical use: a checksum-addressed store

```go
backend := backmem.New()
h := crc32.New()

d, err := h.Digest(bytes.NewReader(payload)) // the object's address IS its crc32
_ = backend.Put(ctx, d, bytes.NewReader(payload))

verifier := cas.NewVerifier(backend, crc32.New())
_ = verifier.Verify(ctx, d) // reads the object back and recomputes the same checksum
```

The address and the verifying hasher must be the same algorithm: `cas.Verify` compares the recomputed digest to `d`, so a CRC-32 digest never matches an object addressed by SHA-256 — `hasher.Validate` rejects the 32-byte address as `ErrInvalidDigest` before anything is read. `benchmarks/verify_bench_test.go` runs exactly the sequence above.

## Policy

Use CRC-32 for a deliberately checksum-addressed store, for fixtures, and for interchange with systems that key data by CRC-32. Keep a stronger hash such as `SHA-256` for durable content-address identity; a cheap check over such a store is a per-object record, which [`sidecar`](../sidecar/README.md) implements (`operations.md` §6). Choose `crc64` for a wider digest and `adler32` for the cheapest computation ([selection rule](../README.md#choosing-among-the-three)) — the same rule picks a record's algorithm.
