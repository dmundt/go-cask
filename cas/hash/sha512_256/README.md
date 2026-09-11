# sha512_256 — fast secure alternative

Package `sha512_256` provides a supported alternative `cas.Hasher` implementation using the Go standard library's SHA-512/256 digest.

It keeps the same design as the SHA-256 package: the core remains hash-agnostic, and the caller injects the hash algorithm. This is a good fast secure option when a workload wants the same 256-bit security level with a different implementation profile.

## Policy

- `SHA-256` remains the default recommendation for new durable CAS data.
- `SHA-512/256` is a supported fast secure alternative.
- `MD5` and `SHA-1` remain legacy or compatibility-only choices and should not be used for new content-addressed data.

## Typical use

```go
h := sha512_256.New()
store := cas.New(raw, codec, h)
```

Use this when you want a modern, fast secure option with 256-bit output and a standard-library implementation.
