# bloom

Optional bloom-filter helpers for `cas.Digest` keys. Filter implementations live in subpackages, so
the hot path, counting logic, and persistent storage stay separate.

## Included variants

- [standard](./standard/README.md) — standard bloom filter for hot-path `Exists` checks
- [counting](./counting/README.md) — counting bloom filter with `Add`/`Remove`/`Contains`
- [persistent](./persistent/README.md) — persistent bloom filter that survives restarts
- [guard.go](./guard.go) — advisory `bloom.Guard` that sits in front of a backend

## Scope

Intentionally not part of the core identity model: `cas.Digest` stays the raw digest bytes the
client selects and the injected `cas.Hasher` validates — no algorithm field is added to the core
model. This package only wraps a client-defined store or index with a probabilistic pre-check.

bloom does not store a digest as the bit index; it derives bit positions from digest bytes and a
Bloom-specific index function. A common pattern is double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

`m` is the bit-array length, `k` the number of probes; the CAS object hash stays independent.

## Policy

- `Hasher` keeps algorithm responsibility outside the storage core.
- bloom is an optimization layer, not a correctness authority: positive results are advisory, and
  the real store still validates existence and content; negative results are definitive for a
  well-formed filter and can short-circuit expensive lookups.
- The index hash is pluggable through `IndexHash`. The default `DefaultIndexHash` is a process-local
  `hash/maphash`, so bits that must outlive the process MUST NOT use it — a filter reloaded from
  disk would report every recorded digest as absent (go-cask#254).
- Filter dimensions are bounded by `bloom.MaxBits`; `bloom.Parameters` reports an error instead of
  sizing an allocation the process cannot serve.
- `bloom.Guard` is a `cas.Backend`, so it keeps the backend contract for bad keys: an absent digest
  is `cas.ErrInvalidDigest`, not a bare "not present".

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

Optional layer: disabled, the underlying store behaves exactly as before.
