# sha512 — standard-library SHA-512 hasher

Package `sha512` provides a client-side `cas.Hasher` for the generic `cas` core using Go's standard library `crypto/sha512` package.

This is an opt-in alternative to the repo's default recommendation, `SHA-256`. It follows the same seam as the other hash packages: the core remains hash-agnostic, and callers choose the algorithm by injecting the hasher they want when they build a store.

## Policy

- The core stays algorithm-agnostic.
- `SHA-256` remains the default recommendation for new durable CAS data.
- `SHA-512` is a useful full-width alternative when a stronger digest width is desired.
- Keep MD5 and SHA-1 out of new data: they are migration or compatibility-only choices.

## Typical use

```go
codec := json.New[MyType]()
h := sha512.New()
store := cas.New(raw, codec, h)
```

Use this package when you want a standard-library full-width SHA-512 digest without changing the CAS core or any object model semantics.
