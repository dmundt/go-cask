# Codec policy — go-cask

See also: [cas/README.md](../README.md) for the wider package architecture and the project defaults.

The `cas` core is codec-agnostic: it stores raw bytes and lets the caller choose how typed values are encoded. The choice belongs in the app layer, not in `package cas`.

## Recommended defaults

- JSON: recommended default for durable, readable, portable data
- CBOR / MessagePack / Protobuf: good for compact binary interchange when the app needs a binary format
- gob: opt-in compatibility only for Go-only workflows
- gzip-wrapped payloads: compression layer, not a primary serialization format

## Do not use as a default

- gob for long-term object storage
- MD5 or SHA-1 for new content-addressed data

A codec is valid if it matches the storage contract and the app's compatibility needs. A codec is not a good default if it is Go-only, unstable across versions, or unsuitable for durable object identity.

## Policy summary

- `cas` core: format-agnostic
- [json/](./json): recommended default
- [gob/](./gob): Go-only compatibility codec
- custom codec packages: allowed when the app's format needs are explicit
