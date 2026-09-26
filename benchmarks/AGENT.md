---
type: Agent Instructions
title: Agent instructions — `benchmarks`
description: The package-local guide for the benchmarks subtree — suite boundaries, measurement rules, the codec/hash matrix, scale probes and result discipline; benchmarks measure performance and never define correctness or gate CI.
version: v2
---

# Agent instructions — `benchmarks`

Package-local guide freezes benchmark structure and measurement rules for
`benchmarks/` subtree.

## Purpose

Benchmarks measure performance; do not define correctness or replace
tests. Preserve CAS invariants and benchmark comparable work before optimizing.
Read [`../docs/specs/performance.md`](../docs/specs/performance.md) and
[`README.md`](./README.md) before changing this subtree.

## Suite boundaries

- Keep shared benchmark helpers in `shared_test.go`.
- Keep fixed-size microbenchmarks grouped by concern in package-local files such as
  `store_bench_test.go`, `backend_bench_test.go`, `codec_bench_test.go`,
  `hash_bench_test.go`, `cache_bench_test.go`, `pack_bench_test.go`,
  `bloom_bench_test.go`, and `verify_bench_test.go`.
- Keep opt-in state-scaling probes in `scale_bench_test.go`.
- Run typed store benchmarks on memory backend to avoid disk noise.
- Measure filesystem behavior only in explicit `FSBackend` cases using
  `b.TempDir()`.
- Keep scale probes disabled unless `CASK_SCALE_OBJECTS` is a positive integer.
- Never add benchmark execution to CI, never add a scheduled/nightly benchmark job. Subtree keeps one committed, machine-specific reference dump (`data/baseline.txt`) a maintainer refreshes by hand with `../scripts/bench-baseline.sh`; comparison point, never a threshold or a gate (performance §5).

## Measurement rules

- Call `b.ReportAllocs()` for every timed benchmark. Non-timed economics or
  layout probe MAY omit it.
- Call `b.SetBytes()` only when one operation processes one payload of known
  size. Never invent byte counts for `Exists`, `Delete`, `List`, `Stats`,
  digest parsing, concurrent mixed operations, or layout probes.
- Complete setup before `b.ResetTimer()`. Move one-time temp-dir creation,
  prefill, object setup outside measured loop when not part of operation under
  test. Stop timer before reports, projections, or cleanup not part of measured
  operation.
- Keep benchmark output clean by default. Emit detailed summary logs only when
  `CASK_BENCH_SUMMARY=1` set; otherwise use standard Go benchmark output.
- Consume returned readers fully and close them; fail benchmark on read or
  close errors.
- Use deterministic payloads. Put benchmarks MUST vary content when measuring
  physical writes; repeated identical content measures deduplication instead.
- Keep object-size labels stable: `64B`, `1KiB`, `1MiB` for store-level
  cases unless a benchmark documents a narrower backend-specific matrix.
- Use hierarchical sub-benchmark names so results remain filterable and
  comparable.

## Codec and hash matrix

- `BenchmarkCodecPackageRoundTrip` is the canonical codec/hash comparison.
- Keep supported payload codecs represented: `json`, `gzip`, `zlib`, `flate`, `gob`, `binary`, and `cbor`.
- Keep supported hashers represented: `sha256`, `sha512`, and `sha512_256`.
- Use same `testNote`, payload sizes, memory backend, Put+Get operation
  for every matrix cell. Never add codec- or hasher-specific fast paths.
- Treat gob as compatibility comparison, JSON as portable default, binary as
  caller-defined compact format.
- Canonical benchmark data lives in `benchmarks/data/*.json` as source of truth.
  README = narrative summary; JSON = queryable record.
- Keep raw matrix in JSON rather than duplicating a giant markdown table in
  README. Use README tables only for winners, key deltas, interpretive
  highlights.
- Matrix changes: update JSON file first, then README summary and any affected
  spec notes in same change.
- Adding or removing a supported codec or hasher requires updating matrix,
  [`README.md`](./README.md), and applicable specs in same change.

## Benchmark workflow

- Validate benchmark logic with smallest relevant scope: run exact bench family
  before broad sweeps. For matrix, use `go test ./benchmarks/ -run=^$ -bench='^BenchmarkCodecPackageRoundTrip$' -benchmem -count=5`.
- After a benchmark run, check JSON file parses cleanly (`python -m json.tool` or
  equivalent) before publishing it as canonical result.
- Preserve runner metadata in JSON (`go version`, OS/arch, CPU, timestamp,
  median-of-N, benchmark name) so later queries distinguish local results
  from cross-machine claims.
- Keep README summary consistent with JSON. Never hand-edit raw matrix
  numbers in markdown when JSON already contains them.
- Keep markdown summaries table-driven and concise; avoid chart blocks unless a
  later requirement explicitly requires them.

## Scale probes

- Prefill N objects before timed loop; prefill time never an operation
  result.
- Run every operational scale probe against both memory and filesystem
  backends and every supported hasher.
- Keep small scale payload at 64 bytes unless projection contract and
  documentation deliberately revised.
- Use exact-count `-benchtime=<n>x` in documented scale commands to bound work.
- Keep projections descriptive, not predictive guarantees. Always state
  measured N and extrapolation target.
- `BenchmarkScaleList` is O(N) memory per operation; keep documentation warnings
  visible.

## Result discipline

- Compare timing only on the same machine under similar load.
- Use repeated runs (`-count=5` or more) for performance conclusions.
- Treat `allocs/op` as primary regression signal; no single noisy
  `ns/op` result justifies a code change.
- Record CPU, RAM, disk/filesystem, OS, and Go version for published results.
- Never claim a regression gate, universal throughput, or production capacity
  from these manual benchmarks.

## Documentation

- Keep [`README.md`](./README.md) as operator guide: inventory, commands,
  output interpretation, resource warnings.
- Keep normative benchmark requirements in this file and
  [`../docs/specs/performance.md`](../docs/specs/performance.md); avoid
  duplicating fragile function counts.
- Update documentation whenever benchmark names, matrices, environment
  variables, sizes, or measurement semantics change.
- Benchmark data summarized in markdown: keep it table-driven and based on
  JSON results. Use JSON as source of truth, keep markdown summaries compact,
  precise, easy to query by size/codec/hasher.
- Visual aid ever needed: prefer a plain markdown table or a small,
  deliberately curated excerpt of canonical JSON values rather than a chart block.

## Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
