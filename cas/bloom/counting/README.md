# counting

Counting Bloom filter — reference accounting with `Add`/`Remove`/`Contains` over
`cas.Digest` keys.

## Bloom indexing

Same bit-position pattern as the other variants: one digest maps to `k` slots of the counter
array, `idx = hash(digest, i) mod m`, default double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

`Contains` is true only when **every** chosen slot holds a non-zero count; `Remove`
decrements exactly the slots `Add` incremented.

## Policy

- counter widths 4, 8 or 16 bits; a slot count saturates at its width, never wraps
- counters are 32-bit, so a filter at the `bloom.MaxBits` ceiling holds `MaxBits*4` bytes —
  shard large key spaces instead of sizing one filter to the limit
- digest text, digest validation and the algorithm seam are the core's (`docs/specs/cas-core.md`
  §4.1–§4.2) — this filter holds no algorithm
- index hash replaceable via `counting.Config.Hash`
- advisory tracking aid, in-memory and not persisted

## Example

```go
filter, err := counting.New(100_000, 0.01, 8)
if err != nil {
    panic(err)
}

d := cas.NewDigest([]byte("ref"))
filter.Add(d)
if filter.Contains(d) {
    filter.Remove(d)
}
```
