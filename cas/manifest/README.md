# manifest — codec-agnostic sidecar metadata

Package `manifest` provides a lightweight sidecar metadata layer for CAS workflows. It reuses the existing core codec contract (`cas.Codec[T]`) rather than defining its own duplicate interface, so the manifest layer stays aligned with the repo's generic design.

## Policy

- The core stays algorithm-agnostic and identity-authoritative.
- This package is for sidecar metadata only; it is not a new object model or a replacement for object hashing.
- It is useful when an application needs operational metadata without introducing a larger database or custom storage layer.
- The default convenience codec is the standard JSON implementation from `cas/codec/json`, but callers can inject any compatible `cas.Codec[T]` for TOML, YAML, CBOR, or other layouts.

## Typical use

```go
m := manifest.Data{"created_by": "demo", "retention": "30d"}
if err := manifest.Save("./store.manifest.json", m); err != nil {
	panic(err)
}
```

For a custom format, pass a compatible codec implementation to `manifest.New[T]`, `manifest.SaveWith`, or `manifest.LoadWith`.
