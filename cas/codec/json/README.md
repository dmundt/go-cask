# json — default codec

Package `json` provides the default `Codec[T]` for the generic `cas` core.

It serializes typed values with the Go standard library `encoding/json`. This is the recommended default for readable and portable object payloads in new durable data.

## Policy

- The core is codec-agnostic; JSON is the recommended default for most app-level use.
- The caller chooses the hash algorithm separately from the codec.
- JSON is a durable, readable, portable format and is a good default for examples and app-level storage.

## Typical use

```go
codec := json.New[MyType]()
store := cas.New(raw, codec, sha256.New())
```

This package is the simplest and most interoperable default for a typed store.
