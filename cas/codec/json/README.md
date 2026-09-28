# json — default codec

`Codec[T]` over the stdlib `encoding/json`. Recommended default for readable, portable,
durable object payloads; codec choice is separate from the hash choice.

```go
codec := json.New[MyType]()
store := cas.New(backend, codec, sha256.New())
```
