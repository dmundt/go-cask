# gob — Go-only compatibility codec

Package `gob` provides a `Codec[T]` for the generic `cas` core over Go's `encoding/gob`. Valid as an
opt-in compatibility option, not recommended as long-term storage for durable CAS objects:
Go-specific, not designed for stable cross-language or archival use.

```go
codec := gob.NewRaw[MyType]()
store := cas.New(raw, codec, sha256.New())
```

Both producer and consumer Go programs, and the data is not intended to be a long-lived external
format.
