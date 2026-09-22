# counting

The counting Bloom filter is the reference-accounting variant.

## Purpose

Use it when you need `Add` and `Remove` semantics rather than a pure membership filter. It is suitable for GC candidate tracking, cache invalidation, and multi-tenant reference counting where entries may need to be removed later.

## Bloom indexing

The implementation uses the same bit-position pattern as the other Bloom variants: each digest is mapped to one or more slots in the counter array using a Bloom-specific index function. The default is double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

The counting filter stores counts in those positions, and `Contains` is true only when each slot has a non-zero value. This is an approximate tracking aid, not a replacement for the real reachability graph or CAS store.

## Policy

- counter widths are configurable as 4, 8 or 16 bits
- sizes are bounded by `bloom.MaxBits`; because each bit costs a 32-bit counter, a filter at that ceiling holds `MaxBits*4` bytes, so shard large key spaces instead of sizing one filter to the limit
- `Contains` returns true only when every chosen slot has a non-zero count
- the filter is still advisory; it does not replace the real CAS store or reachability graph
- `counting.Config.Hash` lets callers replace the default index hash without changing the CAS digest semantics

## Typical use

- invalidate stale cache entries
- track approximate reference counts across snapshots or tenants
- mark and unmark candidate digests during GC or retention analysis

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
