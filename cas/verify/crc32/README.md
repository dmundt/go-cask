# crc32

`crc32` is a maintenance-only integrity helper for go-cask. It is intentionally not the default object-address algorithm.

## Purpose

The storage model stays boring and stable: object identity remains a `cas.Digest`, and the backend is still a raw `Digest -> bytes` store. `crc32` gives callers a cheap, explicit way to verify that bytes still match a known checksum without redefining the address layer.

## Typical use

```go
backend := mem.New()
// object identity still uses the caller's stronger hash
// e.g. sha256.New() when storing a blob

verifier := cas.NewVerifier(backend, crc32.New())
_ = verifier.Verify(ctx, d)
```

## Policy

Use CRC32 for fast consistency checks and local corruption detection. Keep stronger hashes such as SHA-256 for the durable content-address identity path.
