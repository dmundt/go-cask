# flate — optional compression wrapper

Package `flate` provides a `Codec[T]` that wraps another `cas.Codec[T]` and applies the standard-library `compress/flate` format to the serialized bytes.

This fits the same opt-in compression strategy as the other stdlib codec wrappers: it reduces storage cost for large or repetitive payloads without changing the CAS object model or the canonical identity semantics.

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
