# flate — compression codec

Package `flate` implements a `Codec[T]` that wraps another `cas.Codec[T]` and applies the standard-library `compress/flate` format to the encoded bytes.

This follows the same stdlib-style compression strategy as the other codec wrappers: it reduces storage cost for large or repetitive payloads without changing the CAS object model or canonical identity semantics.

## Policy

- The core stays codec-agnostic.
- `flate` is useful when a compact representation is desired with no change to the underlying store contract.
- It remains an extension layer, not the canonical object format.

## Choosing among the three compression wrappers

`flate` is the **default** of `flate`/`gzip`/`zlib` (defaults.md): raw DEFLATE, no format header and no
integrity trailer, so its output is the smallest of the three. Pick `zlib` when the compressed bytes must
be readable by a zlib consumer (it adds a two-byte header and an Adler-32 check) and `gzip` when they must
be readable by gzip tooling (it adds the largest header plus a CRC-32 and a length trailer). All three
compress the *inner* codec's output, are read back through the same package, and share one
`ErrDecodedTooLarge` value and one ceiling (cas-core §4.6).

## Typical use

```go
codec := flate.New(json.New[MyType]())
store := cas.New(backend, codec, sha256.New())
```

Use it when you want a fast, stdlib-only representation-layer optimization for large or repetitive payloads.
