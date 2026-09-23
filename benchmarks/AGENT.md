# Agent instructions — `benchmarks`

This package-local guide freezes benchmark structure and measurement rules for
the `benchmarks/` subtree.

## Purpose

Benchmarks measure performance; they do not define correctness or replace
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
- Run typed store benchmarks on the memory backend to avoid disk noise.
- Measure filesystem behavior only in explicit `FSBackend` cases using
  `b.TempDir()`.
- Keep scale probes disabled unless `CASK_SCALE_OBJECTS` is a positive integer.
- Do not add benchmark execution to CI and do not add a scheduled/nightly benchmark job. The subtree keeps one committed, machine-specific reference dump (`data/baseline.txt`) that a maintainer refreshes by hand with `../scripts/bench-baseline.sh`; it is a comparison point, never a threshold or a gate (performance §5).

## Measurement rules

- Call `b.ReportAllocs()` for every timed benchmark. A non-timed economics or
  layout probe MAY omit it.
- Call `b.SetBytes()` only when one operation processes one payload of known
  size. Do not invent byte counts for `Exists`, `Delete`, `List`, `Stats`,
  digest parsing, concurrent mixed operations, or layout probes.
- Complete setup before `b.ResetTimer()`. Move one-time temp-dir creation,
  prefill, and object setup outside the measured loop when they are not part of
  the operation under test. Stop the timer before reports, projections, or
  cleanup that are not part of the measured operation.
- Keep benchmark output clean by default. Emit detailed summary logs only when
  `CASK_BENCH_SUMMARY=1` is set; otherwise use the standard Go benchmark output.
- Consume returned readers fully and close them; fail the benchmark on read or
  close errors.
- Use deterministic payloads. Put benchmarks MUST vary content when measuring
  physical writes; repeated identical content measures deduplication instead.
- Keep object-size labels stable: `64B`, `1KiB`, and `1MiB` for store-level
  cases unless a benchmark documents a narrower backend-specific matrix.
- Use hierarchical sub-benchmark names so results remain filterable and
  comparable.

## Codec and hash matrix

- `BenchmarkCodecPackageRoundTrip` is the canonical codec/hash comparison.
- Keep supported payload codecs represented: `json`, `gzip`, `zlib`, `flate`, `gob`, `binary`, and `cbor`.
- Keep supported hashers represented: `sha256`, `sha512`, and `sha512_256`.
- Use the same `testNote`, payload sizes, memory backend, and Put+Get operation
  for every matrix cell. Do not add codec- or hasher-specific fast paths.
- Treat gob as a compatibility comparison, JSON as the portable default, and
  binary as the caller-defined compact format.
- The canonical benchmark data lives in `benchmarks/data/*.json` as the source of truth.
  The README is a narrative summary; the JSON is the queryable record.
- Keep the raw matrix in JSON rather than duplicating a giant markdown table in
  the README. Use README tables only for winners, key deltas, and interpretive
  highlights.
- When the matrix changes, update the JSON file first, then update the README summary
  and any affected spec notes in the same change.
- Adding or removing a supported codec or hasher requires updating the matrix,
  [`README.md`](./README.md), and the applicable specs in the same change.

## Benchmark workflow

- Validate benchmark logic with the smallest relevant scope: run the exact bench family
  before broad sweeps. For the matrix, use `go test ./benchmarks/ -run=^$ -bench='^BenchmarkCodecPackageRoundTrip$' -benchmem -count=5`.
- After a benchmark run, check the JSON file parses cleanly (`python -m json.tool` or
  equivalent) before publishing it as the canonical result.
- Preserve runner metadata in the JSON (`go version`, OS/arch, CPU, timestamp,
  median-of-N, and benchmark name) so later queries distinguish local results
  from cross-machine claims.
- Keep the README summary consistent with the JSON. Do not hand-edit the raw matrix
  numbers in markdown when the JSON already contains them.
- Keep markdown summaries table-driven and concise; avoid adding chart blocks unless a
  later requirement explicitly requires them.

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
- When benchmark data is summarized in markdown, keep it table-driven and based on the
  JSON results. Use the JSON as the source of truth and keep markdown summaries compact,
  precise, and easy to query by size/codec/hasher.
- If a visual aid is ever needed, prefer a plain markdown table or a small,
  deliberately curated excerpt of the canonical JSON values rather than a chart block.

## Signed pull-request workflow

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
