# gzip — compression codec

Package `gzip` implements a `Codec[T]` that wraps another `cas.Codec[T]` and applies Go's standard `compress/gzip` format to the encoded bytes.

This is a stdlib-style compression wrapper for large or repetitive payloads. It does not change object identity, graph semantics, or the authoritative `cas` store contract: the wrapped codec still owns serialization, and the gzip layer remains a representation-layer optimization.

## Policy

- The core stays codec-agnostic.
- This wrapper is useful when payloads are large or compressible and a cheaper on-disk representation is worth the extra decode step.
- It is not the canonical store format and not a new object model.

## Choosing among the three compression wrappers

`gzip` is the interop choice: its header plus CRC-32 and length trailer let ordinary gzip tooling read the
compressed payload, at the cost of the largest framing of the three. `flate` is the **default**
(defaults.md) — raw DEFLATE with no header or trailer, the smallest output — and `zlib` sits between them
for consumers that expect a zlib stream. All three compress the *inner* codec's output, are read back
through the same package, and share one `ErrDecodedTooLarge` value and one ceiling (cas-core §4.6).

## Typical use

```go
codec := gzip.New(json.New[MyType]())
store := cas.New(backend, codec, sha256.New())
```

Use it when application data is large, repetitive, or expensive to store uncompressed without changing the rest of the CAS design.
