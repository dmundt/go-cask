# gob — Go-only compatibility codec

Package `gob` provides a `Codec[T]` for the generic `cas` core using Go's `encoding/gob` package.

This codec is valid as an opt-in compatibility option, but it is not the recommended long-term storage format for durable CAS objects because it is Go-specific and not designed for stable cross-language or archival use.

## Policy

- The core stays codec-agnostic.
- `gob` is included as a compatibility codec for Go-only workflows.
- The project recommends JSON for general durable storage and custom formats for specific interoperability needs.

## Typical use

```go
codec := gob.New[MyType]()
store := cas.New(raw, codec, sha256.New())
```

Use `gob` when both producer and consumer are Go programs and the data is not intended to be a long-lived external format.
