# persistent

The persistent Bloom filter survives process restarts without rebuilding its bitset.

## Purpose

Use it for large CAS stores, CI artifact caches, or long-lived directory indexes where the filter should remain warm across process lifetimes. It is designed to be cheap to reopen and to avoid rebuild cost on the next startup.

## Bloom indexing

The on-disk layout still selects bit positions using the same rule as the other Bloom variants. The default is double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

with `m` the bit-array length. The filter persists only the bitset; the CAS object digest remains independent from the in-memory or on-disk Bloom structure. The object hash still belongs to the injected `cas.Hasher` and any typed store that uses it.

## Policy

- stores the filter on disk so it survives restarts
- uses mmap when the platform supports it; otherwise it falls back to file-backed memory
- still remains advisory; correctness comes from the real backend and object graph
- `persistent.Config.Hash` enables a custom Bloom index hash without altering the CAS object identity model

## Typical use

- large object caches reused across CI jobs
- long-lived artifact indexing in build systems
- startup-time filter warmup for hot object lookups

## Example

```go
filter, err := persistent.New("./bloom.bin", 1_000_000, 0.01)
if err != nil {
    panic(err)
}

d := cas.NewDigest([]byte("artifact"))
filter.Add(d)
if filter.Contains(d) {
    fmt.Println("possibly present")
}
filter.Close()
```
