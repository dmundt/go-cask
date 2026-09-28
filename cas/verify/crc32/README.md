# crc32

CRC-32/IEEE `cas.Hasher` helper, for a caller that deliberately addresses objects by CRC-32: identity stays a `cas.Digest` and the backend stays a raw `Digest -> bytes` store.

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

## Typical use: a cheap check over a strongly-addressed store

Choose `crc32` where the checksum must be reproducible or cross-checked outside Go: almost every archive, storage and language toolchain produces CRC-32/IEEE. For the recorded-checksum path, the object keeps its `SHA-256` address and the CRC-32 of the stored bytes sits beside it in a [`sidecar`](../sidecar/README.md) record (`<base>/.meta/<hex>.json`, `operations.md` §6):

```go
backend, _ := fs.New("store/objects")
rec, _ := sidecar.New(backend, sidecar.WithChecksum(crc32.Name, crc32.New()))
store := cas.New(rec, json.New[*Blob](), sha256.New())

blob := &Blob{Data: []byte("payload")}
d, _ := store.Put(ctx, blob) // address is SHA-256; the record holds CRC-32
err := rec.Verifier(crc32.Name, crc32.New()).Verify(ctx, d) // cheap check
```

An object with no record is unchecked, never corrupt. `crc64` for a wider digest, `adler32` for the cheapest computation ([selection rule](../README.md#choosing-among-the-three)) — the same rule picks a record's algorithm.
