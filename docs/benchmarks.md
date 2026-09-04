---
title: Benchmarks — go-cask
description: How to run and read the go-cask benchmarks — the regular performance suite (cas/bench_test.go) and the on-demand state-scaling probes (cas/scale_bench_test.go); commands, parameters, purpose, and how to interpret the output.
version: v1
---

# Benchmarks — go-cask

The go-cask benchmarks measure the `cas` core's speed and allocation
behavior. They are **manual, on-demand tools** — nothing in CI runs `-bench`
(CI enforces correctness, race, coverage, and fuzz only). The normative
contract for what is benchmarked and why is `docs/instructions/performance.md`
§5 (suite) and §11 (scenario targets); this file is the operator's guide to
running and reading the benchmarks themselves.

## 1. The two suites

| Suite            | File                      | What it measures                          | Gate            |
| ---------------- | ------------------------- | ----------------------------------------- | --------------- |
| Regular perf     | `cas/bench_test.go`       | Per-operation cost of store ops at fixed, small object counts (64 B – 1 MiB payloads, flat vs. fan-out layouts) | none (manual)   |
| Scale probes     | `cas/scale_bench_test.go` | How per-operation cost behaves as the store already holds **N objects** (state scaling), projected to a 10^10-object store | skips unless `CASK_SCALE_OBJECTS` is set |

Both live next to the code in package `cas` and run with the standard
`go test -bench` machinery. Every benchmark reports allocations
(`b.ReportAllocs`) and throughput bytes (`b.SetBytes`), per performance §5.

## 2. Common flags (apply to both suites)

Run from the repo root. Benchmarks only execute with `-bench`; the `-run=^$`
guard skips unit tests so the run is benchmarks-only.

| Flag                       | Meaning                                                              |
| -------------------------- | -------------------------------------------------------------------- |
| `-bench <regex>`           | Which benchmarks to run (e.g. `.` = all, `Scale` = scale probes)     |
| `-run=^$`                  | Skip tests, run only benchmarks                                      |
| `-benchmem`                | Report allocations (`B/op`, `allocs/op`). These benchmarks also call `b.ReportAllocs()`, so the columns appear regardless; the flag is harmless and explicit |
| `-benchtime <dur>\|<n>x`   | How long (default `1s`) or how many operations (`500x`) per benchmark. `NNx` gives exact operation counts and keeps big scale runs bounded |
| `-count <n>`               | Repeat each benchmark n times (use ≥ 5 for stable numbers)           |
| `-v`                       | Verbose: shows the scale probes' projection lines                    |
| `-timeout <dur>`           | Whole-run timeout (default 10 min). Use `-timeout 0` for long prefill runs |

## 3. Regular perf suite

### 3.1 Benchmarks

| Benchmark                       | Cases                                                          | What it tells you                              |
| ------------------------------- | -------------------------------------------------------------- | ---------------------------------------------- |
| `BenchmarkStorePut`             | 64 B, 1 KiB, 1 MiB                                             | Typed `Store[T].Put`: codec + hashing + write   |
| `BenchmarkStoreGet`             | 64 B, 1 KiB, 1 MiB                                             | Typed `Store[T].Get`: decode + read             |
| `BenchmarkFSRawStorePut/Get`    | `flat` vs. `fan-out` layout × 64 B/1 KiB/1 MiB                  | Real-disk behavior of the filesystem backend    |
| `BenchmarkRoundTrip`            | Put + Get combined                                             | End-to-end store cycle                          |
| `BenchmarkVerify`               | intact object                                                  | Integrity scan cost                             |
| `BenchmarkParseHash`            | `valid` / `invalid` inputs                                     | Hash-string parsing                             |
| `BenchmarkParallelPutGet`       | concurrent writers/readers                                     | Lock-free read path + mutex write coordination  |

Store-level cases run against `MemoryRawStore` (deterministic, no disk
noise); the `BenchmarkFSRawStore*` cases cover disk behavior separately and
write into a temp dir that is cleaned up automatically.

### 3.2 Run them

> **PowerShell note — package first.** This shell (observed in PowerShell
> 7.6) splits an unquoted `-flag=value` token at the `=`, so
> `go test -bench=. ./cas/` makes `go test` treat `.` as the package list
> and fail with "no Go files". Put the package path **before** the flags and
> every token lands where it belongs:
> `go test ./cas/ -bench=. …`. (cmd and bash do not have this quirk, but
> the package-first order works in every shell.)

```powershell
# PowerShell — everything
go test ./cas/ -bench=. -benchmem -run=^$

# one family, 5 repeats for stable numbers
go test ./cas/ -bench='Benchmark(Store|FS)' -benchmem -count=5 -run=^$

# a single benchmark, exact iteration count
go test ./cas/ -bench=^BenchmarkRoundTrip$ -benchmem -benchtime=10000x -run=^$
```

```bash
# bash / macOS / Linux (flags-first is fine here)
go test -bench=. -benchmem -run=^$ ./cas/
```

## 4. Scale probes (`BenchmarkScale*`)

### 4.1 Purpose

These answer the capacity question: **what does the store cost when it
already holds N objects — and what would a 10^10 (ten billion) object store
cost?** Each probe prefills a store to N objects, times the operation *at
that store size*, and logs a projection line extrapolating the measured rate
(and FS file bytes) to the 10^10 target.

The honest caveat: **10^10 unique objects cannot physically fit in any real
store** — the projection output shows why (≈ 596 GiB of file bytes alone at
64 B/object, before directory entries, fan-out dirs, and inode overhead that
push real disk use past the terabyte mark and past filesystem file-count
limits). So you run at increasing N (10k → 1M → 10M, whatever your machine
and disk allow) and read the scaling curve.

### 4.2 Parameter

| Variable                | Meaning                                              | Default     |
| ----------------------- | ---------------------------------------------------- | ----------- |
| `CASK_SCALE_OBJECTS`    | Number of objects to prefill the store with (the scale knob) | unset → benchmarks **skip** |

The env gate is the not-in-CI guarantee: without it, plain `go test ./cas/`
and CI never touch these benchmarks.

### 4.3 Benchmarks

Each runs as `Memory` and `FS` sub-benchmarks (`FSRawStore` writes to an
auto-cleaned temp dir):

| Benchmark               | Measures at store size N                                  |
| ----------------------- | --------------------------------------------------------- |
| `BenchmarkScalePut`     | Appending new unique objects                              |
| `BenchmarkScaleGet`     | Reading existing objects (payload fully read)             |
| `BenchmarkScaleExists`  | Existence checks                                          |
| `BenchmarkScaleDelete`  | Deleting objects (store shrinks during the loop)          |
| `BenchmarkScaleList`    | Full `List` scan — materializes every hash; **O(N) memory per op, keep N modest** |
| `BenchmarkScaleStats`   | `Stats` (FSRawStore only; the Memory sub-benchmark skips) |

The prefill of N objects happens before the timed loop — it can take minutes
at large N and is **not** part of the per-operation numbers.

### 4.4 Run them

```powershell
# PowerShell (env var syntax; package first, see §3.2 note)
$env:CASK_SCALE_OBJECTS = 100000
go test ./cas/ -run=^$ -bench=Scale -benchtime=1000x -v

$env:CASK_SCALE_OBJECTS = 1000000   # FS: expect several GBs of temp files
go test ./cas/ -run=^$ -bench=Scale -benchtime=100x -v -timeout 0
```

```bash
# bash / macOS / Linux
CASK_SCALE_OBJECTS=100000 go test -run=^$ -bench=Scale -benchtime=1000x -v ./cas/
```

Notes:

- `-v` is required to see the `[scale]` projection lines.
- `-benchtime=NNx` is recommended: exact operation counts, and the run stays
  bounded no matter how slow the FS backend gets.
- Without `-benchtime`, Go's default `1s` calibration re-runs each benchmark
  several times — fine for the regular suite, wasteful at large N.
- Add `-timeout 0` once the prefill approaches minutes.

### 4.5 Reading the projection line

```text
scale_bench_test.go:123: [scale] Put @ 1000 objects: 610 obj/s -> 10^10 objects ~ 4551.4 h | 64.00 B/obj file bytes -> 596.0 GiB for 10^10
BenchmarkScalePut/FS-20   200   1638506 ns/op   0.04 MB/s   2732 B/op   21 allocs/op
```

| Piece                     | Meaning                                                            |
| ------------------------- | ------------------------------------------------------------------ |
| `Put @ 1000 objects`      | The store held 1 000 objects during the measurement                |
| `610 obj/s`               | Measured throughput at that store size                             |
| `~ 4551.4 h`              | Wall time to reach 10^10 objects at that rate (≈ 190 days)         |
| `64.00 B/obj → 596 GiB`   | FS file bytes per object, extrapolated to 10^10 (before dir/inode overhead) |
| `1638506 ns/op` …         | Standard per-op timing, throughput, bytes/op, allocs/op            |

Run the same N on the same machine at a few orders of magnitude (10k, 100k,
1M, …) to see how per-op cost grows — flat or super-linear tells you how the
store scales.

### 4.6 Choosing N (budget)

- **Memory backend:** each object costs a map entry plus its payload — plan
  for a few hundred bytes per object; a 10^7-object run needs single-digit
  GBs of RAM.
- **FS backend:** the 64 B payload is stored in a file; the real on-disk cost
  is dominated by the filesystem (block/cluster size + directory entries +
  fan-out dirs). Expect roughly the cluster size (commonly 4 KiB) per object:
  10^6 objects ≈ 4+ GB, 10^7 ≈ 40+ GB. Start small.
- The FS temp dir is removed when the run finishes; a failed/`Ctrl+C` run can
  leave it behind (it is under the system temp dir, named by `b.TempDir`).

## 5. Comparing results

- Benchmarks are only comparable on the **same machine** (and roughly same
  load). Shared/virtualized runners vary too much for wall-clock numbers —
  that is why CI does not gate on them.
- Use `-count=5` and compare medians/mins, not single runs.
- `ns/op` is the headline; `allocs/op` is stable across machines and is the
  number to watch for regressions (performance §5: hot paths keep allocations
  flat — P-03).

## 6. Reference

- `docs/instructions/performance.md` §5 — the benchmark suite contract;
  §11 — scenario-test targets and defaults.
- `docs/instructions/defaults.md` §6 — default performance targets (e.g.
  memory-backend small Put/Get ≥ 100k obj/s, ≤ 5 allocs/op).
- This guide's examples assume PowerShell (Windows) or bash; the `go test`
  flags are identical everywhere.
