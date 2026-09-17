# flate — compression codec

Package `flate` implements a `Codec[T]` that wraps another `cas.Codec[T]` and applies the standard-library `compress/flate` format to the encoded bytes.

This follows the same stdlib-style compression strategy as the other codec wrappers: it reduces storage cost for large or repetitive payloads without changing the CAS object model or canonical identity semantics.

## Policy

- The core stays codec-agnostic.
- `flate` is useful when a compact representation is desired with no change to the underlying store contract.
- It remains an extension layer, not the canonical object format.

## Typical use

```go
codec := flate.New(json.New[MyType]())
store := cas.New(raw, codec, sha256.New())
```

Use it when you want a fast, stdlib-only representation-layer optimization for large or repetitive payloads.
