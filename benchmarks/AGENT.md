# Agent instructions — `benchmarks`

This package-local guide freezes benchmark structure and measurement rules for
the `benchmarks/` subtree.

## Purpose

Benchmarks measure performance; they do not define correctness or replace
tests. Preserve CAS invariants and benchmark comparable work before optimizing.
Read [`../docs/specs/performance.md`](../docs/specs/performance.md) and
[`README.md`](./README.md) before changing this subtree.

## Suite boundaries

- Keep fixed-size microbenchmarks in `bench_test.go`.
- Keep opt-in state-scaling probes in `scale_bench_test.go`.
- Run typed store benchmarks on the memory backend to avoid disk noise.
- Measure filesystem behavior only in explicit `FSBackend` cases using
  `b.TempDir()`.
- Keep scale probes disabled unless `CASK_SCALE_OBJECTS` is a positive integer.
- Do not add benchmark execution to CI or introduce a committed timing baseline.

## Measurement rules

- Call `b.ReportAllocs()` for every timed benchmark. A non-timed economics or
  layout probe MAY omit it.
- Call `b.SetBytes()` only when one operation processes one payload of known
  size. Do not invent byte counts for `Exists`, `Delete`, `List`, `Stats`,
  digest parsing, concurrent mixed operations, or layout probes.
- Complete setup before `b.ResetTimer()`. Stop the timer before reports,
  projections, or cleanup that are not part of the measured operation.
- Consume returned readers fully and close them; fail the benchmark on read or
  close errors.
- Use deterministic payloads. Put benchmarks MUST vary content when measuring
  physical writes; repeated identical content measures deduplication instead.
- Keep object-size labels stable: `64B`, `1KiB`, and `1MiB` for store-level
  cases unless a benchmark documents a narrower backend-specific matrix.
- Use hierarchical sub-benchmark names so results remain filterable and
  comparable.

## Codec and hash matrix

- `BenchmarkStoreCodecHashRoundTrip` is the canonical codec/hash comparison.
- Keep supported payload codecs represented: `json`, `gob`, and `binary`.
- Keep supported hashers represented: `sha256` and `sha512_256`.
- Use the same `testNote`, payload sizes, memory backend, and Put+Get operation
  for every matrix cell. Do not add codec- or hasher-specific fast paths.
- Treat gob as a compatibility comparison, JSON as the portable default, and
  binary as the caller-defined compact format.
- Adding or removing a supported codec or hasher requires updating the matrix,
  [`README.md`](./README.md), and the applicable specs in the same change.

## Scale probes

- Prefill N objects before the timed loop; prefill time is never an operation
  result.
- Run every operational scale probe against both memory and filesystem
  backends and every supported hasher.
- Keep the small scale payload at 64 bytes unless the projection contract and
  documentation are deliberately revised.
- Use exact-count `-benchtime=<n>x` in documented scale commands to bound work.
- Keep projections descriptive, not predictive guarantees. Always state the
  measured N and extrapolation target.
- `BenchmarkScaleList` is O(N) memory per operation; keep documentation warnings
  visible.

## Result discipline

- Compare timing only on the same machine under similar load.
- Use repeated runs (`-count=5` or more) for performance conclusions.
- Treat `allocs/op` as the primary regression signal; no single noisy
  `ns/op` result justifies a code change.
- Record CPU, RAM, disk/filesystem, OS, and Go version for published results.
- Never claim a regression gate, universal throughput, or production capacity
  from these manual benchmarks.

## Documentation

- Keep [`README.md`](./README.md) as the operator guide: inventory, commands,
  output interpretation, and resource warnings.
- Keep normative benchmark requirements in this file and
  [`../docs/specs/performance.md`](../docs/specs/performance.md); avoid
  duplicating fragile function counts.
- Update documentation whenever benchmark names, matrices, environment
  variables, sizes, or measurement semantics change.

