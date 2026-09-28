# cbor — minimal embedded CBOR codec

Package `cbor` provides a `Codec[T]` for the generic `cas` core over a small, embedded-friendly CBOR
subset consistent with RFC 8949: the scalar, array, map, byte-string, and string values used by
metadata, manifests, and compact structured payloads. A minimal compatibility-friendly option — no
heavy external dependency, not a full-featured general-purpose CBOR implementation, not a full
RFC 8949 transport layer.

## Package overview

- `Codec[T]` is the public contract.
- `NewRaw[T](encode, decode)` builds a compact direct CBOR codec from explicit conversion functions.
- `New[T](next, encode, decode)` builds either a direct codec (`New(nil, encode, decode)`, the same
  as `NewRaw`) or a delegating one (`New(next, nil, nil)`), never both: the conversion already
  produces the stored bytes, so an inner codec passed alongside it is refused by `Encode`/`Decode`
  rather than ignored. To transform another codec's bytes, stack `cas/codec/binary`, whose transform
  pair is the byte-level seam.
- `NewValue()` (`Codec[any]`) and `NewMap()` (`Codec[map[string]any]`) are the convenience
  constructors for the compact value/map model used by metadata and manifest payloads — the repo's
  only sanctioned `any` in an exported API.
- `Decode` bounds how deeply a payload may nest arrays and maps at `MaxDepth` (128 levels); a
  payload nested deeper is `ErrTooDeep` rather than a stack overflow, so the untrusted bytes a store
  hands the codec cannot abort the process.
- A decoded byte string is a **copy**, not a view: `Decode` clones each `[]byte` field, so a retained
  value keeps only its own bytes alive and mutating the input buffer afterwards cannot change a value
  already decoded (go-cask#382).
- No Go reflection: encode and decode conversions must be explicit.

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

Best suited to compact metadata payloads, object manifests, and embedded workflows where a
lightweight binary format is preferred over JSON. Intentionally small and conservative:
deterministic map encoding, no indefinite-length values.
