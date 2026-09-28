# sha256 — default recommended hash

The default recommended `cas.Hasher`. Core is hash-agnostic; new durable content-addressed
data prefers SHA-256 unless a workload has a strong reason to pick another secure
algorithm. `SHA-512/256` is a supported fast secure alternative. MD5 and SHA-1 are
legacy/compatibility-only — never new content-addressed data.

```go
h := sha256.New()
store := cas.New(backend, codec, h)
```
