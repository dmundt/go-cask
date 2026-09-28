# codec

Package `codec` = encoding boundary between typed values and stored bytes: a codec turns a value of
type `T` into bytes and back. Core `cas` is format-agnostic; callers choose the codec matching the
workload.

## Included implementations

- [json](./json/README.md) — default for readable, portable data
- [gzip](./gzip/README.md) — gzip compression for large or repetitive payloads
- [zlib](./zlib/README.md) — zlib compression, stdlib `compress/zlib` format
- [flate](./flate/README.md) — flate compression, stdlib `compress/flate` format, **default**
- [binary](./binary/README.md) — compact custom payload from caller-supplied encode/decode functions
- [gob](./gob/README.md) — Go-only compatibility codec
- [cbor](./cbor/README.md) — compact embedded CBOR for metadata and manifests

## Policy

- Codec choice MUST NOT change object identity or store semantics — representation only. Prefer JSON
  for durable, portable object data.
- `gzip`, `zlib` and `flate` are opt-in compression layers for large or compressible payloads: each
  wraps an inner codec, compresses its output, is read back through its own package, and all three
  share one `ErrDecodedTooLarge` value and one `MaxDecodedBytes` ceiling of 1 GiB (cas-core §4.6).
  Treat compression-encryption wrappers as transport or storage layers, never the canonical format.
- `binary` only when compactness plus a versioned custom layout repay the app-level definition work;
  the object-specific schema stays in the app or gitlike layer.
- `gob` is compatibility-only (Go-only workflows, migration). CBOR suits a compact, self-describing
  payload without a broad external dependency set. Custom codecs stay explicit for a domain-specific
  format.
- No codec is the right default if it is Go-only, unstable across versions, or unsuitable for
  long-lived object identity.

## Cascading codec layers

Wrappers, not replacements: stack one around a base codec for a new transitive codec, core `cas`
semantics unchanged. Every wrapper takes a `next` codec; inner codec first, outer transform second:

```go
codec := flate.New(gzip.New(json.New[MyType]()))
```

The inner codec owns value serialization; each outer layer adds one representation step. Store and
object identity model stay untouched; no constructor variant may break that composition.
