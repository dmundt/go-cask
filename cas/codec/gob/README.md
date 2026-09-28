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

## Decode depth

`Decode` recursion follows the **destination type**, not the payload: gob recurses as deep as the `T`
it is handed, so bytes cannot invent nesting `T` does not have, and the one payload-only recursion
path (skipping a field the destination does not know) is capped by the standard library at 10 000
levels. A **recursive** `T` — `type Node struct{ Next *Node }` — is the exception: a crafted,
type-compatible chain of non-nil pointers drives the decoder one stack frame per level until the
goroutine stack is exhausted, a fatal error no `recover` can catch. Never decode untrusted bytes
directly into a recursive `T`; bound the payload first (a size ceiling, or a wire format with its own
depth bound, such as `cas/codec/cbor`'s `MaxDepth`).
