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

- counter widths 4, 8 or 16 bits; `CounterBits` selects the packed slot width, so the counter
  array is `m*CounterBits/8` bytes — two 4-bit counters per byte, one 8-bit per byte, one
  little-endian 16-bit per two bytes
- a slot saturates at its width's maximum, never wraps; removals never touch the neighbouring
  counter sharing the byte
- `MaxCounterBytes = bloom.MaxBits/2` (2 GiB) is this filter's own ceiling: a shape whose packed
  counters would be larger is refused with `ErrFilterTooLarge` before anything is allocated, so a
  mis-sized filter is an error rather than an out-of-memory. A 4-bit filter reaches it at the full
  `bloom.MaxBits` bits; an 8- or 16-bit filter reaches it sooner — shard large key spaces instead
  of sizing one filter to the limit
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
