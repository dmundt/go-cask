# Concepts

go-cask is a content-addressable store for Go: the digest of an
object's encoded bytes is its identity.

## Why the model matters

- identical content resolves to the same digest
- object identity stays stable even when names or paths change
- the byte layer stays independent from application types
- integrity checks are explicit, not implicit on every read

## The building blocks

| Type | Role |
|---|---|
| `Digest` | the content address — raw bytes, rendered as lowercase hex |
| `Hasher` | the client-supplied algorithm that derives a digest (`Digest`/`Validate`) |
| `Object[T]` | a versioned type name plus the digests it references |
| `Codec[T]` | serializes a Go value to bytes and back (`Encode`/`Decode`) |
| `Backend` | the storage engine for bytes (`Put`/`Get`/`Exists`/`Delete`/`List`/`Stats`) |
| `Store[T]` | typed access on top of a `Backend`, a `Codec[T]`, and a `Hasher` |
| `Validator` | optional `Validate() error` the store enforces on `Put` and `Get` |

See [architecture](../architecture.md) for the canonical data-flow diagram
tying these together, and the topic pages below for each piece in more
depth.

- [Content addressing](content-addressing.md)
- [Hashes](hashes.md)
- [Codecs](codecs.md)
- [Backends](backends.md)
