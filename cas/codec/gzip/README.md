# gzip — optional compression wrapper

Package `gzip` provides a `Codec[T]` that wraps any other `cas.Codec[T]` and compresses its serialized bytes with Go's standard `compress/gzip` package.

This is an opt-in storage optimization for large or repetitive payloads. It does not change object identity, graph semantics, or the authoritative `cas` store contract: the wrapped codec still owns serialization, and the gzip layer is only a representation layer.

## Policy

- The core stays codec-agnostic.
- This wrapper is useful when payloads are large or compressible and a cheaper on-disk representation is worth the extra decode step.
- It is not the canonical store format and not a new object model.

## Typical use

```go
codec := gzip.New(json.New[MyType]())
store := cas.New(raw, codec, sha256.New())
```

Use it when application data is large, repetitive, or expensive to store uncompressed without changing the rest of the CAS design.
