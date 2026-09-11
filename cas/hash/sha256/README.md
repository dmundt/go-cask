# sha256 — default recommended hash

Package `sha256` provides the default recommended `cas.Hasher` implementation for go-cask.

It follows the repository's policy: the core is hash-agnostic, but new durable content-addressed data should prefer SHA-256 unless a workload has a strong reason to pick another secure algorithm.

## Policy

- `SHA-256` is the recommended default for new CAS data.
- `SHA-512/256` is a supported fast secure alternative.
- `MD5` and `SHA-1` are legacy or compatibility-only choices and should not be used for new content-addressed data.

## Typical use

```go
h := sha256.New()
store := cas.New(raw, codec, h)
```

This package is the simplest and safest default for app-level object identity in the repo's examples and documentation.
