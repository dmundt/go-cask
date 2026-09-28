# gzip — compression codec

Package `gzip` implements a `Codec[T]` wrapping another `cas.Codec[T]`, applying Go's standard
`compress/gzip` format to the encoded bytes. Representation-layer extension, never the canonical
`cas` store format or a new object model.

Interop choice: its header plus a CRC-32 and a length trailer let ordinary gzip tooling read the
compressed payload, at the cost of the largest framing of the three. `flate` is the **default**
(defaults.md) — raw DEFLATE, no header or trailer, the smallest output — and `zlib` sits between
them for consumers that expect a zlib stream. Shared wrapper rules, `ErrDecodedTooLarge` and
`MaxDecodedBytes`: `cas/codec/README.md` and cas-core §4.6.

```go
codec := gzip.New(json.New[MyType]())
store := cas.New(backend, codec, sha256.New())
```
