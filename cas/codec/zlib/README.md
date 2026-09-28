# zlib — compression codec

Package `zlib` implements a `Codec[T]` wrapping another `cas.Codec[T]`, applying the
standard-library `compress/zlib` format to the encoded bytes. Representation-layer extension, never
the canonical object format or a new object model.

For payloads a zlib consumer must read: a two-byte header and an Adler-32 check around the same
DEFLATE stream `flate` writes. `flate` is the **default** (defaults.md) — no header, no trailer,
smallest output — and `gzip` trades more framing for gzip-tool interoperability. Shared wrapper
rules, `ErrDecodedTooLarge` and `MaxDecodedBytes`: `cas/codec/README.md` and cas-core §4.6.

```go
codec := zlib.New(json.New[MyType]())
store := cas.New(backend, codec, sha256.New())
```
