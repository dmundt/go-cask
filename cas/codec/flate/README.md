# flate — compression codec

Package `flate` implements a `Codec[T]` wrapping another `cas.Codec[T]`, applying the standard-library
`compress/flate` format to the encoded bytes. Representation-layer extension, never the canonical
object format.

## Choosing among the three compression wrappers

`flate` is the **default** of `flate`/`gzip`/`zlib` (defaults.md): raw DEFLATE, no format header and
no integrity trailer, so its output is the smallest of the three. Pick `zlib` when the compressed
bytes must be readable by a zlib consumer (it adds a two-byte header and an Adler-32 check) and
`gzip` when they must be readable by gzip tooling (it adds the largest header plus a CRC-32 and a
length trailer). All three compress the *inner* codec's output, are read back through the same
package, and share one `ErrDecodedTooLarge` value and one `MaxDecodedBytes` ceiling (cas-core §4.6).

```go
codec := flate.New(json.New[MyType]())
store := cas.New(backend, codec, sha256.New())
```

Use it for a fast, stdlib-only representation-layer optimization of large or repetitive payloads.
