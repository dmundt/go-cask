# zlib — compression codec

Package `zlib` implements a `Codec[T]` that wraps another `cas.Codec[T]` and applies the standard-library `compress/zlib` format to the encoded bytes.

This is a stdlib-style compression wrapper for payloads that are large, repetitive, or otherwise expensive to keep uncompressed. It leaves the CAS identity model unchanged: the wrapped codec still owns serialization, and the zlib layer remains a representation-layer optimization.

## Policy

- The core remains codec-agnostic.
- This wrapper is useful when a representation-layer optimization is worth the extra inflate step.
- It is not the canonical object format and not a new object model.

## Choosing among the three compression wrappers

`zlib` is for payloads a zlib consumer must read: a two-byte header and an Adler-32 check around the same
DEFLATE stream `flate` writes. `flate` is the **default** (defaults.md) — no header, no trailer, smallest
output — and `gzip` trades more framing for gzip-tool interoperability. All three compress the *inner*
codec's output, are read back through the same package, and share one `ErrDecodedTooLarge` value and one
ceiling (cas-core §4.6).

## Typical use

```go
codec := zlib.New(json.New[MyType]())
store := cas.New(backend, codec, sha256.New())
```

Use it when a payload is large or compressible and a transparent compression layer improves storage efficiency without changing the rest of the store design.
