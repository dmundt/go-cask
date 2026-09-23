# Object format

A stored object is a self-describing envelope: a Go value is encoded by a
`Codec[T]`, wrapped with its versioned type name and the codec's identity tag,
and the whole envelope is hashed and stored under the resulting digest.

## Envelope layout

```text
version            u8
codec length       uvarint
codec tag          bytes
type length        uvarint
type name          bytes
payload length     uvarint
payload            bytes
```

- **version** — the envelope format version (currently `2`).
- **codec tag** — the identity of the codec that wrote the payload (see
  [Codec identity](#codec-identity)); empty means "unspecified".
- **type name** — a versioned name such as `"note@1"` or `"commit@1"`. An
  object's `Type()` method must return a name containing `@`; the store
  rejects an unversioned type name at write time.
- **payload** — exactly the bytes the `Codec[T]` produced. The store never
  interprets them.

The payload length is explicit, so a reader locates the payload without
scanning to the end of the object — useful for streaming and range reads.
Version `1` of the format had no codec tag: those objects still read, as
"codec unspecified".

## Codec identity

A codec may implement `cas.CodecNamer` (`CodecName() string`) to declare the
format it produces. The tag is **declared, never derived** — no Go type name,
reflection or payload inspection is involved, so renaming a type or moving a
package is not read as a format change.

- `json`, `gob`, `cbor`, `binary` — a codec's own format.
- `gzip+json`, `zlib+json`, `flate+json` — a stack, composed from the inner
  codec's tag (`<wrapper>+<inner>`); a stack over a codec that declares no tag
  reports no tag either.
- `""` — a codec without `CodecNamer`, or a version 1 object: the identity is
  unspecified and no comparison is made.

`Store.Get` compares the stored tag with the reading codec's own tag before it
decodes, and reports `ErrCodecMismatch` when both are present and differ — a
format change, distinct from `ErrCorrupt` (damaged bytes) and `ErrUnknownType`
(unknown type or major version). That is what makes changing a codec safe
without bumping every type's major version.

## Identity

The digest covers the **entire envelope**, not just the payload. That means:

- identical type + identical encoded bytes → identical digest (dedup)
- a different codec, a different encoding of the same value, or a different
  type version all produce a different digest
- `Store.Get` compares the codec identity tag (above), decodes the envelope,
  decodes the payload with the store's codec, and rejects the object
  (`ErrUnknownType`) if the decoded value's `Type()` does not match the stored
  type name

## Optional invariants

A type may additionally implement `Validator` (`Validate() error`). When it
does, `Store.Put` calls it before encoding and `Store.Get` calls it after
decoding, so an object that violates its own invariants is never written and
never handed back silently — decoded violations surface as `ErrCorrupt`.

## Shipped codecs

| Codec | Package | Tag | Notes |
|---|---|---|---|
| JSON | `cas/codec/json` | `json` | readable, portable, the usual default |
| gob | `cas/codec/gob` | `gob`, `gob+<inner>` when stacked | Go-only; not a stable cross-language or long-term archive format |
| binary | `cas/codec/binary` | `binary`, `binary+<inner>` when stacked | compact, caller-defined encode/decode functions |
| CBOR | `cas/codec/cbor` | `cbor` | compact, caller-defined encode/decode functions |
| gzip / zlib / flate | `cas/codec/gzip`, `cas/codec/zlib`, `cas/codec/flate` | `gzip+<inner>`, `zlib+<inner>`, `flate+<inner>` | compression wrappers around an inner codec |

Custom codecs are ordinary `Codec[T]` implementations — see the
[custom codec recipe](../recipes/custom-codec.md).

## Compatibility principle

The store stays lean and explicit rather than accumulating hidden
assumptions: type versioning is a first-class field in the envelope, and
validation is opt-in and identical across every codec, rather than baked into
one wire format.
