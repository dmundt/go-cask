# Codecs

A codec defines how a Go value becomes bytes and back again.

## Typical usage

- JSON for readability
- CBOR for compact binary encoding
- custom formats for domain-specific payloads
- gzip / zlib / flate wrappers for compression

## Why codecs are separate

The storage layer stores bytes. The application decides how to represent a domain object in those bytes. That keeps the storage core generic and the application logic independent from the byte format.

## Example flow

```text
Go object -> codec.Encode() -> bytes -> backend
bytes -> codec.Decode() -> Go object
```

The repo ships a few standard codec choices and keeps the interface simple enough for custom implementations.
