# Codecs

A codec defines how a Go value becomes bytes and back:

```go
type Codec[T any] interface {
    Encode(v T) ([]byte, error)
    Decode(data []byte) (T, error)
}
```

The contract is a round trip: `Decode(Encode(v)) == v` for every storable
value.

## Shipped codecs

| Package | Format | Notes |
|---|---|---|
| `cas/codec/json` | JSON | readable, portable, the usual default |
| `cas/codec/gob` | Go `encoding/gob` | Go-only; not a stable cross-language format |
| `cas/codec/binary` | compact binary | caller-supplied encode/decode functions |
| `cas/codec/cbor` | compact CBOR subset | caller-supplied encode/decode functions |
| `cas/codec/gzip`, `cas/codec/zlib`, `cas/codec/flate` | compression wrappers | wrap an inner codec; no encryption wrapper ships |

## Why codecs are separate

The storage layer stores bytes; the codec decides how a domain object becomes
those bytes. That keeps the core generic and leaves representation choices —
JSON for readability, a compact format for size — entirely with the caller.

## Composing codecs

A compression wrapper takes an inner codec and re-encodes its output:

```go
inner := jsoncodec.New[*Note]()
compressed := gzip.New(inner)
```

`compressed.Encode` runs the inner codec first, then compresses the result;
`Decode` reverses the order. Because the digest covers the final envelope,
changing the codec stack changes the digest of otherwise-identical values —
see the [custom codec recipe](../recipes/custom-codec.md) for a worked
example.
