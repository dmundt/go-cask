---
type: Agent Instructions
title: Agent instructions — `benchmarks`
description: The package-local guide for the benchmarks subtree — suite boundaries, measurement rules, the codec/hash matrix, scale probes and result discipline; benchmarks measure performance and never define correctness or gate CI.
version: v5
---

# Agent instructions — `benchmarks`

Package-local guide: freezes benchmark structure and measurement rules for `benchmarks/`.

## Purpose

Benchmarks measure performance; do not define correctness or replace tests. Preserve CAS invariants, benchmark comparable work before optimizing. Read [`../docs/specs/performance.md`](../docs/specs/performance.md) and [`README.md`](./README.md) before changing this subtree.

## Suite boundaries

- Shared helpers in `shared_test.go`.
- Fixed-size microbenchmarks grouped by concern in `store_bench_test.go`, `backend_bench_test.go`, `codec_bench_test.go`, `hash_bench_test.go`, `cache_bench_test.go`, `pack_bench_test.go`, `bloom_bench_test.go`, `verify_bench_test.go`.
- Opt-in state-scaling probes in `scale_bench_test.go`.
- Typed store benchmarks on the memory backend, to avoid disk noise.
- Filesystem behavior measured only in explicit `FSBackend` cases using `b.TempDir()`.
- Scale probes disabled unless `CASK_SCALE_OBJECTS` is a positive integer.
- Never add benchmark execution to CI or a scheduled/nightly benchmark job. One committed, machine-specific reference dump (`data/baseline.txt`), refreshed by hand with `go run ./cmd/gate bench-baseline`: comparison point, never a threshold or a gate (performance §5).

## Measurement rules

- `b.ReportAllocs()` for every timed benchmark; a non-timed economics or layout probe MAY omit it.
- `b.SetBytes()` only when one operation processes one payload of known size. Never invent byte counts for `Exists`, `Delete`, `List`, `Stats`, digest parsing, concurrent mixed operations, or layout probes.
- Complete setup before `b.ResetTimer()`: one-time temp-dir creation, prefill and object setup outside the measured loop when not part of the operation under test. Stop the timer before reports, projections or cleanup outside the measured operation.
- Clean output by default; detailed summary logs only when `CASK_BENCH_SUMMARY=1` is set, else standard Go benchmark output.
- Consume returned readers fully and close them; fail the benchmark on read or close errors.
- Deterministic payloads. Put benchmarks MUST vary content when measuring physical writes — repeated identical content measures deduplication instead.
- Object-size labels stable: `64B`, `1KiB`, `1MiB` for store-level cases unless a benchmark documents a narrower backend-specific matrix.
- Hierarchical sub-benchmark names, so results stay filterable and comparable.

## Codec and hash matrix

- `BenchmarkCodecPackageRoundTrip` is the canonical codec/hash comparison.
- Payload codecs represented: `json`, `gzip`, `zlib`, `flate`, `gob`, `binary`, `cbor`.
- Hashers represented: `sha256`, `sha512`, `sha512_256`.
- Same `testNote`, payload sizes, memory backend and Put+Get operation for every matrix cell. Never add codec- or hasher-specific fast paths.
- `gob` = compatibility comparison, `json` = portable default, `binary` = caller-defined compact format.
- Canonical data in `benchmarks/data/*.json` = source of truth; README = narrative summary, JSON = queryable record.
- Raw matrix in JSON, never a duplicated giant markdown table. README tables carry winners, key deltas, interpretive highlights only.
- Matrix change: JSON file first, then README summary and any affected spec notes, same change.
- Adding or removing a supported codec or hasher: update matrix, [`README.md`](./README.md) and applicable specs, same change.

## Benchmark workflow

- Validate with the smallest relevant scope: exact bench family before broad sweeps. Matrix: `go test ./benchmarks/ -run=^$ -bench='^BenchmarkCodecPackageRoundTrip$' -benchmem -count=5`.
- After a run, check the JSON parses cleanly (`python -m json.tool` or equivalent) before publishing it as canonical.
- Preserve runner metadata in JSON (`go version`, OS/arch, CPU, timestamp, median-of-N, benchmark name), so later queries separate local results from cross-machine claims.
- README summary consistent with the JSON; never hand-edit raw matrix numbers in markdown when the JSON holds them.
- Markdown summaries table-driven and concise; chart blocks only when a later requirement explicitly requires them.

## Scale probes

- Prefill N objects before the timed loop; prefill time is never an operation result.
- Run every operational scale probe against both memory and filesystem backends, and every supported hasher.
- Small scale payload 64 bytes unless the projection contract and its documentation are deliberately revised.
- Exact-count `-benchtime=<n>x` in documented scale commands, to bound work.
- Projections descriptive, never predictive guarantees; always state the measured N and the extrapolation target.
- `BenchmarkScaleList` is O(N) memory per operation; keep its documentation warnings visible.

## Result discipline

- Timing comparable only on the same machine under similar load.
- Repeated runs (`-count=5` or more) for performance conclusions.
- `allocs/op` = primary regression signal; no single noisy `ns/op` result justifies a code change.
- Record CPU, RAM, disk/filesystem, OS and Go version for published results.
- Never claim a regression gate, universal throughput or production capacity from these manual benchmarks.

## Documentation

- [`README.md`](./README.md) = operator guide: inventory, commands, output interpretation, resource warnings.
- Normative benchmark requirements here and in [`../docs/specs/performance.md`](../docs/specs/performance.md); avoid duplicating fragile function counts.
- Update documentation whenever benchmark names, matrices, environment variables, sizes or measurement semantics change.
- Markdown benchmark summaries table-driven, from the JSON results: JSON = source of truth, summaries compact, precise, queryable by size/codec/hasher.
- Visual aid ever needed: a plain markdown table or a small curated excerpt of canonical JSON values, not a chart block.

## Signed pull-request workflow

Rebuild PR branches locally from current `main`; never GitHub's server-side rebase or update-branch operation. Apply changes with `git cherry-pick -S`, verify every head commit with `git verify-commit`, push with `git push --force-with-lease`; auto-merge only after signature verification and the required checks pass. Owner: [`../docs/specs/landing.md`](../docs/specs/landing.md) §5.
