# Object format

A stored object is a self-describing envelope: a Go value is encoded by a
`Codec[T]`, wrapped with its versioned type name, and the whole envelope is
hashed and stored under the resulting digest.

## Envelope layout

```text
[version u8][uvarint type-length][type name][uvarint payload-length][payload]
```

- **version** — the envelope format version (currently `1`).
- **type name** — a versioned name such as `"note@1"` or `"commit@1"`. An
  object's `Type()` method must return a name containing `@`; the store
  rejects an unversioned type name at write time.
- **payload** — exactly the bytes the `Codec[T]` produced. The store never
  interprets them.

The payload length is explicit, so a reader locates the payload without
scanning to the end of the object — useful for streaming and range reads.

## Identity

The digest covers the **entire envelope**, not just the payload. That means:

- identical type + identical encoded bytes → identical digest (dedup)
- a different codec, a different encoding of the same value, or a different
  type version all produce a different digest
- `Store.Get` decodes the envelope, decodes the payload with the store's
  codec, and rejects the object (`ErrUnknownType`) if the decoded value's
  `Type()` does not match the stored type name

## Optional invariants

A type may additionally implement `Validator` (`Validate() error`). When it
does, `Store.Put` calls it before encoding and `Store.Get` calls it after
decoding, so an object that violates its own invariants is never written and
never handed back silently — decoded violations surface as `ErrCorrupt`.

## Shipped codecs

| Codec | Package | Notes |
|---|---|---|
| JSON | `cas/codec/json` | readable, portable, the usual default |
| gob | `cas/codec/gob` | Go-only; not a stable cross-language or long-term archive format |
| binary | `cas/codec/binary` | compact, caller-defined encode/decode functions |
| CBOR | `cas/codec/cbor` | compact, caller-defined encode/decode functions |
| gzip / zlib / flate | `cas/codec/gzip`, `cas/codec/zlib`, `cas/codec/flate` | compression wrappers around an inner codec |

Custom codecs are ordinary `Codec[T]` implementations — see the
[custom codec recipe](../recipes/custom-codec.md).

## Compatibility principle

The store stays lean and explicit rather than accumulating hidden
assumptions: type versioning is a first-class field in the envelope, and
validation is opt-in and identical across every codec, rather than baked into
one wire format.
