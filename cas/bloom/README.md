# bloom

This package provides optional bloom-filter helpers for `cas.Digest` keys. The actual filter implementations live in subpackages to keep the hot path, counting logic, and persistent storage separate.

## Included variants

- [standard](./standard/README.md) — standard bloom filter for hot-path `Exists` checks
- [counting](./counting/README.md) — counting bloom filter with `Add`/`Remove`/`Contains`
- [persistent](./persistent/README.md) — persistent bloom filter that survives restarts
- [guard.go](./guard.go) — advisory `bloom.Guard` that sits in front of a backend

## Scope

The bloom layer is intentionally not part of the core identity model. `cas.Digest` remains the raw digest bytes selected by the client and validated by the injected `cas.Hasher`. This package only wraps a client-defined store or index with a probabilistic pre-check.

bloom does not store a digest as the bit index; it derives bit positions from digest bytes and a Bloom-specific index function. A common pattern is double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

where `m` is the bit-array length and `k` is the number of probes. The object hash used by the CAS store remains independent; the Bloom filter only chooses bit locations inside its own bitmap.

## Policy

- `Digest` stays raw bytes — no algorithm field is added to the core model.
- `Hasher` keeps algorithm responsibility outside the storage core.
- bloom is an optimization layer, not a correctness authority.
- Positive results are advisory; the real store still validates existence and content.
- Negative results are definitive for a well-formed filter and can short-circuit expensive lookups.
- The filter's internal index hash is pluggable; callers may replace the default `sha256`-based mixer with their own stable function.
- Filter dimensions are bounded by `bloom.MaxBits`; `bloom.Parameters` reports an error instead of sizing an allocation the process cannot serve.
- `bloom.Guard` is a `cas.Backend`, so it keeps the backend contract for bad keys: an absent digest is `cas.ErrInvalidDigest`, not a bare "not present".

## Example

```go
import (
    "github.com/dmundt/go-cask/cas/bloom"
    stdfilter "github.com/dmundt/go-cask/cas/bloom/standard"
)

filter, err := stdfilter.New(100_000, 0.01)
if err != nil {
    panic(err)
}
guard, err := bloom.NewGuard(raw, filter)
if err != nil {
    panic(err)
}

exists, err := guard.Exists(ctx, digest)
if err != nil {
    panic(err)
}
```

This layer is optional. When it is not enabled, the underlying store behaves exactly as before.
