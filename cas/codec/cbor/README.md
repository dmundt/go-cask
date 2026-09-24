# cbor — minimal embedded CBOR codec

Package `cbor` provides a `Codec[T]` for the generic `cas` core using a small, embedded-friendly CBOR subset consistent with RFC 8949. It supports the scalar, array, map, byte-string, and string values most often used in metadata, manifests, and compact structured payloads without adding a heavy external dependency.

## Package overview

- `Codec[T]` is the public contract.
- `NewRaw[T](encode, decode)` builds a compact direct CBOR codec from explicit conversion functions.
- `New[T](next, encode, decode)` builds either a direct codec (`New(nil, encode, decode)`, the same as `NewRaw`) or a delegating one (`New(next, nil, nil)`), never both: the conversion already produces the stored bytes, so an inner codec passed alongside it is refused by `Encode`/`Decode` rather than ignored. To transform another codec's bytes, stack `cas/codec/binary`, whose transform pair is the byte-level seam.
- `NewMap()` and `NewValue()` are the convenience constructors for the compact map/value model used by metadata and manifest payloads.
- The implementation focuses on embedded metadata and manifests rather than a full RFC 8949 transport layer.

## Policy

- Keep the core codec-agnostic.
- Prefer JSON for portable, human-readable durable objects.
- Use CBOR when a compact binary encoding is useful and the app accepts a narrow, embedded-focused format.
- Do not use Go reflection in this package; encode and decode conversions must be explicit.
- Treat this implementation as a minimal compatibility-friendly option, not as a full-featured general-purpose CBOR implementation.

## Typical use

```go
encode := func(v MyType) ([]byte, error) { return cbor.NewMap().Encode(map[string]any{"title": v.Title, "body": v.Body}) }
decode := func(data []byte) (MyType, error) {
    m, err := cbor.NewMap().Decode(data)
    if err != nil { return MyType{}, err }
    return MyType{Title: m["title"].(string), Body: m["body"].(string)}, nil
}
codec := cbor.NewRaw[MyType](encode, decode)
store := cas.New(raw, codec, sha256.New())
```

This package is best suited to compact metadata payloads, object manifests, and embedded workflows where a lightweight binary format is preferred over JSON but a full RFC 8949 implementation is not required.

## Notes

The implementation is intentionally small and conservative: deterministic map encoding, no indefinite-length values, and support for the common scalar and structured data patterns needed by small CAS metadata workflows.
