# sha512 — standard-library full-width hasher

Standard-library `crypto/sha512`-backed `cas.Hasher` for the generic `cas` core. Opt-in alternative to the default `SHA-256`; the core stays hash-agnostic, and the caller injects the hasher when building a store.

Full-width SHA-512: a 64-byte digest, for a workload that wants the extra width. Distinct from [`SHA-512/256`](../sha512_256/README.md) — same 256-bit security level, 32-byte output, different implementation profile. `SHA-256` remains the default recommendation for new durable CAS data; keep MD5 and SHA-1 to migration or compatibility only, never new content-addressed data.

```go
codec := json.New[MyType]()
h := sha512.New()
store := cas.New(raw, codec, h)
```
