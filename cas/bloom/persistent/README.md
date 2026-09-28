# persistent

On-disk Bloom filter — mmap-backed, survives process restarts without rebuilding its bitset.

## File format

```text
offset  size  contents
0       8     magic "CASKBLM1" (the last byte is the format version)
8       1     index-hash kind: 0 = this package's default, 1 = caller-supplied
9       7     reserved, zero; a reader ignores it
16      32    index key — the material the default index hash is derived from
48      ...   the bitset
```

The key is what makes "survives a restart" true. `bloom.DefaultIndexHash` uses
`maphash.MakeSeed()`, so it is process-local: a filter indexed that way reports **false** for
every digest another process recorded, and `bloom.Guard` treats a negative as authoritative
absence — a wrong answer, not a lost hint (go-cask#254). This package therefore derives its
default index hash from a 32-byte key generated once per file and reused on every reopen. A
caller-supplied `Config.Hash` must be deterministic for the same reason, and the header records
which kind wrote the file, so reopening under the other kind rebuilds the hint set instead of
trusting bits indexed another way. A file with no usable header — written before the header
existed, truncated, or written under the other kind — is rebuilt empty with a fresh key. The
hint set is a cache: an unusable file costs a rebuild, never a wrong answer. `Filter.Reset`
clears the bits and keeps the header and key.

External dependency: `golang.org/x/sys`, for portable mmap flushing —
`docs/specs/coding-guidelines.md` §3.

## Bloom indexing

Bit positions come from the configured `bloom.IndexHash`: for each probe `i` the filter hashes
the digest and the probe index together (`hash(digest || i) mod m`, `m` the bit-array length),
so one digest's `k` positions stay distinct.

## Policy

- stores the filter on disk so it survives restarts
- persists the index key beside the bitset, so the default index hash is stable across
  processes; a custom `Config.Hash` must be deterministic by the same rule
- rebuilds a file whose header it cannot use, rather than answering from bits it cannot index
- uses mmap where the platform supports it; otherwise an in-memory buffer written back on
  `Sync`/`Close`
- `Filter.IsMapped()` reports the backing strategy; Windows always reports `false` — no Windows
  memory mapping yet
- `Filter.Sync` flushes the backing store
- sizing bounded by `bloom.MaxBits`, so an implausible `ExpectedItems` is an error, not an
  enormous allocation
- `persistent.Config.Hash` replaces the Bloom index hash without altering CAS object identity
- advisory; correctness comes from the real backend and object graph

## Lifetime

`Close` flushes and releases the backing store. Idempotent, and a `*Filter` MUST NOT be used
afterwards: `Add` and `Reset` become no-ops, `Contains` reports `false`. Calling `Close` more
than once — including on a `nil` `*Filter` — returns `nil`.

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
defer filter.Close()
```
