# Object format

The object format in CASK is intentionally simple: byte-oriented content, typed wrapper models, and digest-based identity.

## Core pattern

- bytes are stored under a digest
- a typed object defines how those bytes are interpreted
- a codec converts a Go object to bytes and back
- the storage layer remains generic and does not know the application type

## Good defaults

- JSON for readable, portable metadata
- binary or CBOR for compact custom payloads
- compression wrappers when space matters more than readability

## Compatibility principle

The project keeps the core lean and explicit. Stable object semantics are more useful than a broad set of hidden assumptions.
