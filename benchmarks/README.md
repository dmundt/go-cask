---
type: Guide
title: Benchmarks — go-cask
description: How to run and read the go-cask benchmarks — the regular performance suite (benchmarks/bench_test.go) and the on-demand state-scaling probes (benchmarks/scale_bench_test.go); commands, parameters, purpose, and how to interpret the output.
version: v8
---

# Benchmarks — go-cask

The go-cask benchmarks measure the `cas` core's speed and allocations. They are **manual, on-demand tools** — CI never runs `-bench` (CI enforces correctness/race/coverage/fuzz). The normative contract for what/why is `performance.md` §5 (suite) and §11 (scenario targets); this is the operator's run-and-read guide.

## 1. The two suites

| Suite | File | Measures | Gate |
|---|---|---|---|
| Regular perf | `benchmarks/bench_test.go` | Per-op cost at fixed, small object counts (64 B – 1 MiB, flat vs. fan-out) | none (manual) |
| Scale probes | `benchmarks/scale_bench_test.go` | Per-op cost as the store already holds **N objects** (state scaling), projected to a 10^10-object store | skips unless `CASK_SCALE_OBJECTS` set |

Both live in `benchmarks/` and use standard `go test -bench`. Every benchmark except `BenchmarkScaleStoreEconomics` reports allocations (`b.ReportAllocs`) — 14 of the 15 `Benchmark*` functions (15 call sites: `BenchmarkParseDigest` calls it in each of its two subtests). `BenchmarkScaleStoreEconomics` is a layout/count probe that times nothing. Throughput (`b.SetBytes`) is set only where one payload of known size defines the per-op work — `BenchmarkStorePut`, `BenchmarkStoreGet`, `BenchmarkFSBackendPut`, `BenchmarkFSBackendGet`, `BenchmarkRoundTrip`, `BenchmarkVerify` and the `ScalePut`/`ScaleGet` probes (8 of the 14). `BenchmarkParseDigest` (both subtests), `BenchmarkParallelPutGet`, `BenchmarkScaleExists`, `BenchmarkScaleDelete`, `BenchmarkScaleList` and `BenchmarkScaleStats` report allocations only, because "bytes per op" is meaningless for them.

## 2. Common flags

Run from the repo root. Benchmarks run only with `-bench`; `-run=^$` skips unit tests.

| Flag | Meaning |
|---|---|
| `-bench <regex>` | Which benchmarks (`.` = all, `Scale` = scale probes) |
| `-run=^$` | Tests off; benchmarks only |
| `-benchmem` | Report `B/op`/`allocs/op` (also emitted via `ReportAllocs`); harmless/explicit |
| `-benchtime <dur>\|<n>x` | Time (default `1s`) or exact op count (`500x`); `NNx` bounds big scale runs |
| `-count <n>` | Repeats (≥ 5 for stable numbers) |
| `-v` | Shows the scale probes' projection lines |
| `-timeout <dur>` | Whole-run timeout (default 10 min); `-timeout 0` for long prefills |

## 3. Regular perf suite

### 3.1 Benchmarks

| Benchmark | Cases | Tells you |
|---|---|---|
| `BenchmarkStorePut` | 64 B, 1 KiB, 1 MiB | Typed `Store[T].Put`: codec + hashing + write |
| `BenchmarkStoreGet` | 64 B, 1 KiB, 1 MiB | Typed `Store[T].Get`: decode + read |
| `fs`-backend Put/Get | `flat` vs `fan-out` × 64 B/1 KiB/1 MiB | Real-disk `fs` behavior |
| `BenchmarkRoundTrip` | Put + Get | End-to-end cycle |
| `BenchmarkVerify` | intact object | Integrity scan cost |
| `BenchmarkParseDigest` | `valid`/`invalid` | Digest parsing (`sha256.Parse`: printable form + bare hex) |
| `BenchmarkParallelPutGet` | concurrent | Lock-free reads + mutex writes |

Store cases run against the in-memory `memory` backend (deterministic); the `fs` cases write to an auto-cleaned temp dir.

### 3.2 Run them

```powershell
go test -bench='.' -benchmem -run=^$ ./benchmarks/   # canonical: quote flag values
```

> **PowerShell note — quote `-flag=value` tokens.** PowerShell 7.6 mis-parses an *unquoted* `-bench=.` (treats `.` as the package list → "no Go files"). Quoting (`-bench='.'`) fixes it. Putting the package first (`go test ./benchmarks/ -bench=. …`) also works everywhere, as does dropping `-run=^$`.

```powershell
go test -bench='Benchmark(Store|FS)' -benchmem -count=5 -run=^$ ./benchmarks/      # one family, 5 repeats
go test -bench='^BenchmarkRoundTrip$' -benchmem -benchtime=10000x -run=^$ ./benchmarks/
```

```bash
# bash / macOS / Linux (flags-first fine)
go test -bench=. -benchmem -run=^$ ./benchmarks/
```

## 4. Scale probes (`BenchmarkScale*`)

### 4.1 Purpose

Answer: **what does a store cost when it already holds N objects — and a 10^10 (ten-billion) store?** Each probe prefills to N, times the op *at that size*, and logs a projection line extrapolating the measured rate (and FS file bytes) to 10^10.

Honest caveat: **10^10 unique objects cannot fit any real store** — ≈ 596 GiB of file bytes alone at 64 B/object, before directory/inode overhead pushes real use past the terabyte and FS file-count limits. Run at increasing N (10k → 1M → 10M, whatever your disk allows) and read the curve.

### 4.2 Parameter

| Variable | Meaning | Default |
|---|---|---|
| `CASK_SCALE_OBJECTS` | Prefill size (the scale knob) | unset → benchmarks **skip** |

The env gate is the not-in-CI guarantee.

### 4.3 Benchmarks

Each runs as `Memory` and `FS` sub-benchmarks (`fs.New` writes to an auto-cleaned temp dir):

| Benchmark | Measures at store size N |
|---|---|
| `BenchmarkScalePut` | Appending new unique objects |
| `BenchmarkScaleGet` | Reading existing objects (payload fully read) |
| `BenchmarkScaleExists` | Existence checks |
| `BenchmarkScaleDelete` | Deleting objects (store shrinks) |
| `BenchmarkScaleList` | Full `List` scan — materializes every hash; **O(N) memory/op, keep N modest** |
| `BenchmarkScaleStats` | `Stats` summary (counts, bytes) on both backends |
| `BenchmarkScaleStoreEconomics` | FS on-disk layout cost at N: object-file count, dirs, leaf-dir spread (min/avg/max), object bytes — for `(2,1)` vs `(4,1)` (needs `-v`) |

The N-object prefill happens before the timed loop (can take minutes at large N) and is **not** part of the per-op numbers.

### 4.4 Run them

```powershell
$env:CASK_SCALE_OBJECTS = 100000
go test ./benchmarks/ -run=^$ -bench=Scale -benchtime=1000x -v

$env:CASK_SCALE_OBJECTS = 1000000   # FS: expect several GBs of temp files
go test ./benchmarks/ -run=^$ -bench=Scale -benchtime=100x -v -timeout 0
```

```bash
CASK_SCALE_OBJECTS=100000 go test -run=^$ -bench=Scale -benchtime=1000x -v ./benchmarks/
```

Notes: `-v` is required for the `[scale]` projection lines; `-benchtime=NNx` is recommended (exact counts, bounded runs); without it Go's `1s` calibration re-runs each bench (wasteful at large N); add `-timeout 0` when the prefill nears minutes.

### 4.5 Hash-choice comparison

The scale probes now compare the default `sha256` path against the `sha512_256` path in the same benchmark family, so you can compare throughput without changing the benchmark harness:

```text
BenchmarkScalePut/Memory/sha256-8
BenchmarkScalePut/Memory/sha512_256-8
BenchmarkScaleGet/FS/sha256-8
BenchmarkScaleGet/FS/sha512_256-8
```

Use them to answer the practical question: is the extra digest width worth the cost for your deployment? In practice, `SHA-256` is the default recommendation for durability and interoperability; `SHA-512/256` is a supported fast secure alternative if a workload favors a slightly different tradeoff. Do not use MD5 or SHA-1 for new content-addressed data.

### 4.6 Reading the projection line

```text
scale_bench_test.go:123: [scale] Put @ 1000 objects: 610 obj/s -> 10^10 objects ~ 4551.4 h | 64.00 B/obj file bytes -> 596.0 GiB for 10^10
BenchmarkScalePut/FS-20   200   1638506 ns/op   0.04 MB/s   2732 B/op   21 allocs/op
```

| Piece | Meaning |
|---|---|
| `Put @ 1000 objects` | Store held 1000 objects during measurement |
| `610 obj/s` | Measured throughput at that size |
| `~ 4551.4 h` | Wall time to 10^10 at that rate (≈ 190 days) |
| `64.00 B/obj → 596 GiB` | FS file bytes/object extrapolated to 10^10 (before dir/inode overhead) |
| `1638506 ns/op` … | Standard per-op timing, throughput, B/op, allocs/op |

Run the same N at a few magnitudes (10k/100k/1M/…) on one machine; flat vs. super-linear per-op growth shows how the store scales.

### 4.6 Choosing N (budget)

- **Memory:** ~a few hundred bytes/object (map entry + payload); 10^7 objects needs single-digit GBs of RAM.
- **FS:** on-disk cost is FS-dominated (block/cluster size + dir entries + fan-out) — expect ~the cluster size (commonly 4 KiB) per object: 10^6 ≈ 4+ GB, 10^7 ≈ 40+ GB. Start small.
- The FS temp dir is removed on a clean run; a failed/`Ctrl+C` run can leave it (under the system temp dir, named by `b.TempDir`).

## 5. Comparing results

- Benchmarks are comparable only on the **same machine** (and roughly same load) — why CI doesn't gate wall-clock.
- Use `-count=5`; compare medians/mins, not single runs.
- `ns/op` is the headline; `allocs/op` is machine-stable and the number to watch for regressions (performance §5, P-03).

## 6. Reference

- `performance.md` §5 (suite contract), §11 (scenario targets).
- `defaults.md` §6 (default targets, e.g. memory small Put/Get ≥ 100k obj/s, ≤ 5 allocs/op).
- Commands above assume PowerShell (Windows) or bash; the `go test` flags are identical everywhere.
