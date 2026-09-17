# zlib — compression codec

Package `zlib` implements a `Codec[T]` that wraps another `cas.Codec[T]` and applies the standard-library `compress/zlib` format to the encoded bytes.

This is a stdlib-style compression wrapper for payloads that are large, repetitive, or otherwise expensive to keep uncompressed. It leaves the CAS identity model unchanged: the wrapped codec still owns serialization, and the zlib layer remains a representation-layer optimization.

## Policy

- The core remains codec-agnostic.
- This wrapper is useful when a representation-layer optimization is worth the extra inflate step.
- It is not the canonical object format and not a new object model.

## Typical use

```go
codec := zlib.New(json.New[MyType]())
store := cas.New(raw, codec, sha256.New())
```

Use it when a payload is large or compressible and a transparent compression layer improves storage efficiency without changing the rest of the store design.
