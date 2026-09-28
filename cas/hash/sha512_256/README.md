# sha512_256 — fast secure alternative

Standard-library `crypto/sha512`-backed `cas.Hasher` for the generic `cas` core. Supported
alternative to the default `SHA-256`; the core stays hash-agnostic, and the caller injects the
hasher when building a store.

SHA-512/256: 32-byte output at the same 256-bit security level as `SHA-256`, with a different
implementation profile — the fast secure option when that profile matters. Distinct from
full-width [`SHA-512`](../sha512/README.md) (64-byte digest). `SHA-256` remains the default
recommendation for new durable CAS data; keep MD5 and SHA-1 to migration or compatibility only,
never new content-addressed data.

```go
h := sha512_256.New()
store := cas.New(backend, codec, h)
```
