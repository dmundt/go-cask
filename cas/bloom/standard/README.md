# standard

The standard Bloom filter is the hot-path option for rapid `Exists` checks.

## Purpose

Use it when you want to avoid unnecessary backend or index lookups in latency-sensitive code. It is small, fast, and memory-efficient, and it is designed around `cas.Digest` bytes as the lookup key.

## Bloom indexing

A Bloom filter does not store the digest itself as the index. It computes bit positions from the digest and a Bloom-specific indexing function. The common pattern is double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

where `m` is the bit-array length and `i` is the probe number. The base hash function is internal to the Bloom filter; it is not the CAS object hash, which remains the caller-owned `cas.Hasher`/`Digest` contract.

## Policy

- `cas.Digest` remains raw bytes; the algorithm remains the caller's responsibility.
- This filter is advisory only: a positive result is "possibly present", never authoritative.
- A negative result is definitive for a well-formed filter and can short-circuit work.
- The index hash is pluggable via `standard.Config.Hash`; the default implementation uses a stable double-hash mixer.

## Typical use

- pre-check a large object index before a real lookup
- reduce repeated duplicate checks in hot code paths
- avoid expensive fetches for obviously absent digests

## Example

```go
filter, err := standard.New(100_000, 0.01)
if err != nil {
    panic(err)
}

d := cas.NewDigest([]byte("hello"))
filter.Add(d)
if filter.Contains(d) {
    fmt.Println("possibly present")
}
```
