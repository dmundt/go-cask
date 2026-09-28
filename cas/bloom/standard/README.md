# standard

Standard in-memory Bloom filter — hot-path membership test over `cas.Digest` keys.

## Bloom indexing

A digest is not stored; `k` bit positions are derived from its bytes with a Bloom-specific
index function, default double hashing:

```text
idx_i = (h1(x) + i*h2(x)) mod m
```

`m` is the bit-array length, `i` the probe number.

## Policy

- `Contains` is a plain membership test: no counters, no `Remove`
- a negative is definitive for a well-formed filter and can short-circuit work; a positive is
  "possibly present", never authoritative
- `cas.Digest` stays raw bytes; digest text, digest validation and the algorithm seam are the
  core's (`docs/specs/cas-core.md` §4.1–§4.2) — this filter holds no algorithm
- index hash is pluggable via `standard.Config.Hash`; the default is a stable double-hash mixer
- advisory pre-check in front of a store or index, not a correctness authority

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
