---
title: Benchmarks — go-cask
description: How to run and read the go-cask benchmarks — the regular performance suite (cas/bench_test.go) and the on-demand state-scaling probes (cas/scale_bench_test.go); commands, parameters, purpose, and how to interpret the output.
version: v2
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

The canonical "run all benchmarks" command — identical in PowerShell,
cmd, and bash:

```powershell
go test -bench='.' -benchmem -run=^$ ./cas/
```

> **PowerShell note — quote `-flag=value` tokens.** This shell (observed in
> PowerShell 7.6) mis-parses an *unquoted* `-bench=.` token: `go test`
> then treats `.` as the package list and fails with "no Go files".
> Quoting the value (`-bench='.'`) fixes it and is the canonical form
> above. Putting the package path first (`go test ./cas/ -bench=. …`)
> also works in every shell, as does dropping `-run=^$` if you accept the
> unit tests running first.

```powershell
# one family, 5 repeats for stable numbers
go test -bench='Benchmark(Store|FS)' -benchmem -count=5 -run=^$ ./cas/

# a single benchmark, exact iteration count
go test -bench='^BenchmarkRoundTrip$' -benchmem -benchtime=10000x -run=^$ ./cas/
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

## 6. Sample measurements & extrapolation (Windows/NTFS anchor)

Anchor run: 2026-09-04, Windows 11 (NTFS), 13th Gen Intel i7-13800H, Go
1.27, default fan-out (2,1). Measured with the scale probes
(`CASK_SCALE_OBJECTS=<N> go test ./cas/ -run=^$ -bench=Scale -benchtime=NNx -v`).
Per-op numbers are for a store that **already holds N objects**; List/Stats
are full scans. These are a *sample*, not a contract — re-measure on your
machine (§5) before quoting numbers.

### 6.1 Measured anchors

| Store size N | Backend | Put | Get | Exists | List (per call) | Stats |
| ------------ | ------- | --- | --- | ------ | --------------- | ----- |
| 10^4 | FS | 2.61 ms | 70 µs | 26 µs | 66 ms | 60 ms |
| 10^5 | FS | 2.90 ms | 119 µs | — | — | — |
| 10^5 | Memory | 0.67 µs | 0.26 µs | 0.26 µs | 0.69 s | (no Stats) |
| 10^6 | Memory | 0.67 µs | 0.38 µs | 0.29 µs | 8.1 s | (no Stats) |

Memory per-op cost is **flat** from 10^5 → 10^6 (O(1) map ops). FS per-op
cost **grows slowly** with N (larger fan-out dirs: Put +11 % per decade,
Get ~×1.7 per decade); List/Stats are **linear** in N.

### 6.2 Model fits (used for the extrapolation)

- Memory Put/Get/Exists ≈ **flat** at the 10^6 anchors (0.67 / 0.38 /
  0.29 µs).
- FS Put ≈ `1.7×10^6 · N^0.046` ns; FS Get ≈ `10.6 · N^0.21` µs (N in
  objects).
- List (both backends) and FS Stats ≈ **N × ~7 µs/object** per full scan.

### 6.3 Estimated per-op cost at store size N (10^5 … 10^10)

| N | Mem Put/Get/Exists | FS Put | FS Get | FS Exists* |
| - | ------------------ | ------ | ------ | ---------- |
| 10^5 | ~0.3–0.7 µs | 2.9 ms | 0.12 ms | ~0.03 ms |
| 10^6 | ~0.3–0.7 µs | 3.2 ms | 0.19 ms | ~0.03 ms |
| 10^7 | ~0.3–0.7 µs | 3.6 ms | 0.31 ms | ~0.04 ms |
| 10^8 | ~0.3–0.7 µs | 4.0 ms | 0.51 ms | ~0.04 ms |
| 10^9 | ~0.3–0.7 µs | 4.4 ms | 0.82 ms | ~0.05 ms |
| 10^10 | ~0.3–0.7 µs | 4.9 ms | 1.3 ms | ~0.06 ms |

\* Exists has no anchor past 10^4 (26 µs); shown as a mild-growth estimate.

### 6.4 Full scans, build-up and footprint

| N | Mem List (per call) | FS List / Stats (per call) | FS single-writer fill (cumulative) | Mem store RAM† | FS disk‡ |
| - | ------------------- | -------------------------- | ---------------------------------- | -------------- | -------- |
| 10^5 | 0.7 s | ~0.7 s | ~5 min | 20 MB | ~0.2 GB |
| 10^6 | 8 s | ~7 s | ~50 min | 200 MB | ~2 GB |
| 10^7 | ~75 s | ~70 s | ~10 h | 2 GB | ~20 GB |
| 10^8 | ~12 min | ~12 min | ~4 days | 20 GB | ~0.2 TB |
| 10^9 | ~2 h | ~2 h | ~50 days | 200 GB | ~2 TB |
| 10^10 | ~21 h | ~18 h | **~1.5 years** | **2 TB** | ~20 TB |

† Assumes ~0.2 KB retained per object (map entry + 64 B payload).
‡ Assumes ~2 KiB on disk per object (64 B payload + filesystem overhead);
the real figure is FS-dependent, roughly 1–4 KiB/object (§4.6).

### 6.5 Takeaways

- The store is **O(1)-per-op on both backends** up to physical limits —
  Put/Get/Exists/Delete stay flat in time; only the FS file cost (ms vs µs)
  and its slow directory growth separate the backends.
- **List and Stats are O(N)** scans; at 10^9 they cost hours per call, and
  Memory's List additionally churns ~7 KB of allocations per object per
  call (≈ 740 GB allocated per call at 10^8 — unusable before the time
  matters).
- **10^10 is not a wall-clock question but a physics question**: an FS fill
  would take ~1.5 years single-threaded and land at ~20 TB with ~4×10^7
  files per fan-out directory — past NTFS/ext4 file-count ceilings
  (~4×10^9 max, practical limits far lower); the Memory store would need
  ~2 TB of RAM.

Caveats: single-threaded, single-machine Windows/NTFS numbers; the fits use
2–3 anchor points and are rough at the extremes (nothing past 10^6 on FS
was measured — the fan-out layout may degrade harder in pathological
per-directory counts).

## 7. Reference

- `docs/instructions/performance.md` §5 — the benchmark suite contract;
  §11 — scenario-test targets and defaults.
- `docs/instructions/defaults.md` §6 — default performance targets (e.g.
  memory-backend small Put/Get ≥ 100k obj/s, ≤ 5 allocs/op).
- This guide's examples assume PowerShell (Windows) or bash; the `go test`
  flags are identical everywhere.
