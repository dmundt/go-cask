# persistent

The persistent Bloom filter survives process restarts without rebuilding its bitset.

## Purpose

Use it for large CAS stores, CI artifact caches, or long-lived directory indexes where the filter should remain warm across process lifetimes. It is designed to be cheap to reopen and to avoid rebuild cost on the next startup.

## File format

```text
offset  size  contents
0       8     magic "CASKBLM1" (the last byte is the format version)
8       1     index-hash kind: 0 = this package's default, 1 = caller-supplied
9       7     reserved, zero; a reader ignores it
16      32    index key — the material the default index hash is derived from
48      ...   the bitset
```

The key is what makes "survives a restart" true. A Bloom index built from a
process-local seed (`bloom.DefaultIndexHash`, which uses `maphash.MakeSeed()`)
reports **false** for every digest another process recorded — and
`bloom.Guard` treats a negative as authoritative absence, so that is a wrong
answer, not a lost hint (go-cask#254). This package therefore derives its
default index hash from a 32-byte key generated once per file and reused on every
reopen. A caller-supplied `Config.Hash` must be deterministic for the same
reason; the header records which kind wrote the file, so reopening under the
other kind rebuilds the hint set instead of trusting bits indexed another way.

A file with no usable header — one written before the header existed, a
truncated one, or one written under the other kind — is rebuilt empty with a
fresh key. The hint set is a cache: an unusable file costs a rebuild, never a
wrong answer. `Filter.Reset` clears the bits and keeps the header and key, so a
reset filter is immediately reusable for the same shape.

## Bloom indexing

Bit positions come from the configured `bloom.IndexHash`: for each probe `i` the
filter hashes the digest and the probe index together (`hash(digest || i) mod m`,
with `m` the bit-array length), so one digest's `k` positions stay distinct. The
default for this package is the keyed, deterministic hash described above. The
CAS object digest remains independent from the in-memory or on-disk Bloom
structure: the object hash still belongs to the injected `cas.Hasher` and any
typed store that uses it.

## Policy

- stores the filter on disk so it survives restarts
- persists the index key beside the bitset, so the default index hash is stable
  across processes; a custom `Config.Hash` must be deterministic by the same
  rule
- rebuilds a file whose header it cannot use, rather than answering from bits it
  cannot index
- uses mmap when the platform supports it; otherwise it falls back to an in-memory buffer that is written back on `Sync`/`Close`
- reports which backing strategy is in use through `Filter.IsMapped()`; Windows always reports `false` because this package has no Windows memory mapping yet
- still remains advisory; correctness comes from the real backend and object graph
- `persistent.Config.Hash` enables a custom Bloom index hash without altering the CAS object identity model
- sizing is bounded by `bloom.MaxBits`, so an implausible `ExpectedItems` is an error instead of an enormous allocation

## Lifetime

`Close` flushes and releases the backing store. It is idempotent, and a `*Filter`
must not be used afterwards: `Add` and `Reset` become no-ops and `Contains`
reports `false`. Calling `Close` more than once (including on a `nil` `*Filter`)
returns `nil`.

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
defer filter.Close()
```
