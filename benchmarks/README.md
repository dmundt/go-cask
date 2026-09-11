---
type: Guide
title: Benchmarks — go-cask
description: How to run and read the go-cask benchmarks — the regular performance suite (benchmarks/bench_test.go) and the on-demand state-scaling probes (benchmarks/scale_bench_test.go); commands, parameters, purpose, and how to interpret the output.
version: v10
---

# Benchmarks — go-cask

The go-cask benchmarks measure the `cas` core's speed and allocations. They are **manual, on-demand tools** — CI never runs `-bench` (CI enforces correctness/race/coverage/fuzz). The normative contract is `performance.md` §5 and §11; [`AGENT.md`](./AGENT.md) freezes package-local benchmark rules; this file is the operator's run-and-read guide.

## 1. The two suites

| Suite | File | Measures | Gate |
|---|---|---|---|
| Regular perf | `benchmarks/bench_test.go` | Per-op cost at fixed, small object counts (64 B – 1 MiB, flat vs. fan-out) | none (manual) |
| Scale probes | `benchmarks/scale_bench_test.go` | Per-op cost as the store already holds **N objects** (state scaling), projected to a 10^10-object store | skips unless `CASK_SCALE_OBJECTS` set |

Both live in `benchmarks/` and use standard `go test -bench`. Every timed benchmark reports allocations. `BenchmarkScaleStoreEconomics` is a layout/count probe that times nothing. Throughput is reported only where one payload of known size defines each operation; benchmarks never invent byte counts for metadata, parsing, mixed concurrent, or layout work.

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
| `BenchmarkStorePut` | 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 1 MiB | Typed `Store[T].Put`: codec + hashing + write |
| `BenchmarkStoreGet` | 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 1 MiB | Typed `Store[T].Get`: decode + read |
| `BenchmarkMemBackendPut/Get` | 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 256 KiB, 1 MiB | Raw memory-backend byte path |
| `BenchmarkFSBackendPut/Get` | `flat` vs `fan-out` × 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 256 KiB, 1 MiB | Real-disk `fs` behavior |
| `BenchmarkStoreCodecHashRoundTrip` | `json`/`gob`/`binary` × `sha256`/`sha512_256` × 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 1 MiB | Comparable end-to-end codec/hash combinations |
| `BenchmarkCodecMarshalUnmarshal` | `json`/`gob`/`binary` × 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 1 MiB | Codec-only marshal/unmarshal cost |
| `BenchmarkHasherDigest` | `sha256`/`sha512_256` × 64 B, 256 B, 1 KiB, 8 KiB, 64 KiB, 1 MiB | Hash-only throughput and allocation profile |
| `BenchmarkRoundTrip` | Put + Get | End-to-end cycle |
| `BenchmarkVerify` | intact object | Integrity scan cost |
| `BenchmarkParseDigest` | `valid`/`invalid` | Digest parsing (`sha256.Parse`: printable form + bare hex) |
| `BenchmarkParallelPutGet` | concurrent | Lock-free reads + mutex writes |

Store cases run against the in-memory `memory` backend (deterministic); the `fs` cases write to an auto-cleaned temp dir.

### 3.2 Codec/hash matrix

`BenchmarkStoreCodecHashRoundTrip` keeps the object type and the in-memory store
backend constant while sweeping the payload size, codec, and hasher. The matrix
covers:

- sizes: `64B`, `256B`, `1KiB`, `8KiB`, `64KiB`, `1MiB`
- codecs: `json`, `gob`, `binary`
- hashers: `sha256`, `sha512_256`

The sub-benchmark naming format is:

```text
BenchmarkStoreCodecHashRoundTrip/<codec>/<hasher>/<size>
```

Examples:

```text
BenchmarkStoreCodecHashRoundTrip/json/sha256/1KiB
BenchmarkStoreCodecHashRoundTrip/json/sha512_256/1KiB
BenchmarkStoreCodecHashRoundTrip/gob/sha256/1KiB
BenchmarkStoreCodecHashRoundTrip/gob/sha512_256/1KiB
BenchmarkStoreCodecHashRoundTrip/binary/sha256/1KiB
BenchmarkStoreCodecHashRoundTrip/binary/sha512_256/1KiB
```

This measures full typed-store round trips: codec marshal/unmarshal, envelope,
hashing, and memory-backend Put/Get. It does not isolate codec or hash cost in
isolation. Gob remains a Go-only compatibility comparison; JSON is the portable
default; binary uses the benchmark's stable caller-defined layout.

Run only this matrix:

```powershell
go test ./benchmarks/ -run=^$ -bench='^BenchmarkStoreCodecHashRoundTrip$' -benchmem -count=5
```

### 3.3 Observed results (median of 5 runs)

#### Winner by payload size

| Payload size | Winner | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| 64B | `binary` + `sha256` | 1481 | 43.22 | 1976 | 25 |
| 256B | `binary` + `sha256` | 2092 | 122.35 | 2920 | 28 |
| 1KiB | `json` + `sha256` | 6442 | 158.97 | 8554 | 27 |
| 8KiB | `json` + `sha256` | 47109 | 173.89 | 65898 | 39 |
| 64KiB | `binary` + `sha256` | 364266 | 179.91 | 620961 | 58 |
| 1MiB | `binary` + `sha256` | 2685813 | 390.41 | 9224386 | 74 |

#### Full matrix by payload size

##### 64B

| Codec | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| `json` | `sha256` | 1656 | 38.64 | 1870 | 21 |
| `json` | `sha512_256` | 1690 | 37.86 | 1966 | 21 |
| `gob` | `sha256` | 11408 | 5.61 | 10200 | 192 |
| `gob` | `sha512_256` | 11550 | 5.54 | 10296 | 192 |
| **`binary`** | **`sha256`** | **1481** | **43.22** | **1976** | **25** |
| `binary` | `sha512_256` | 1662 | 38.50 | 2072 | 25 |

##### 256B

| Codec | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| `json` | `sha256` | 2398 | 106.75 | 2448 | 21 |
| `json` | `sha512_256` | 2606 | 98.24 | 2544 | 21 |
| `gob` | `sha256` | 11734 | 21.82 | 11288 | 193 |
| `gob` | `sha512_256` | 12272 | 20.86 | 11384 | 193 |
| **`binary`** | **`sha256`** | **2092** | **122.35** | **2920** | **28** |
| `binary` | `sha512_256` | 2364 | 108.28 | 3016 | 28 |

##### 1KiB

| Codec | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| **`json`** | **`sha256`** | **6442** | **158.97** | **8554** | **27** |
| `json` | `sha512_256` | 7178 | 142.66 | 8651 | 27 |
| `gob` | `sha256` | 15747 | 65.03 | 19720 | 199 |
| `gob` | `sha512_256` | 16797 | 60.96 | 19816 | 199 |
| `binary` | `sha256` | 7388 | 138.60 | 10200 | 34 |
| `binary` | `sha512_256` | 7313 | 140.02 | 10296 | 34 |

##### 8KiB

| Codec | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| **`json`** | **`sha256`** | **47109** | **173.89** | **65898** | **39** |
| `json` | `sha512_256` | 50244 | 163.05 | 66003 | 39 |
| `gob` | `sha256` | 65324 | 125.41 | 97992 | 211 |
| `gob` | `sha512_256` | 67651 | 121.09 | 98088 | 211 |
| `binary` | `sha256` | 49999 | 163.84 | 78745 | 46 |
| `binary` | `sha512_256` | 52444 | 156.21 | 78840 | 46 |

##### 64KiB

| Codec | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| `json` | `sha256` | 388523 | 168.68 | 526798 | 53 |
| `json` | `sha512_256` | 398853 | 164.31 | 526271 | 53 |
| `gob` | `sha256` | 438682 | 149.39 | 710863 | 223 |
| `gob` | `sha512_256` | 475039 | 137.96 | 710959 | 223 |
| **`binary`** | **`sha256`** | **364266** | **179.91** | **620961** | **58** |
| `binary` | `sha512_256` | 393598 | 166.51 | 621055 | 58 |

##### 1MiB

| Codec | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| `json` | `sha256` | 3823624 | 274.24 | 9022783 | 80 |
| `json` | `sha512_256` | 4471169 | 234.52 | 8967757 | 79 |
| `gob` | `sha256` | 2831201 | 370.36 | 10534892 | 239 |
| `gob` | `sha512_256` | 3521372 | 297.77 | 10534980 | 239 |
| **`binary`** | **`sha256`** | **2685813** | **390.41** | **9224386** | **74** |
| `binary` | `sha512_256` | 3216106 | 326.04 | 9224481 | 74 |

#### Full matrix ordered by codec

##### `json`

| Size | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| **64B** | **`sha256`** | **1656** | **38.64** | **1870** | **21** |
| `64B` | `sha512_256` | 1690 | 37.86 | 1966 | 21 |
| **256B** | **`sha256`** | **2398** | **106.75** | **2448** | **21** |
| `256B` | `sha512_256` | 2606 | 98.24 | 2544 | 21 |
| **1KiB** | **`sha256`** | **6442** | **158.97** | **8554** | **27** |
| `1KiB` | `sha512_256` | 7178 | 142.66 | 8651 | 27 |
| **8KiB** | **`sha256`** | **47109** | **173.89** | **65898** | **39** |
| `8KiB` | `sha512_256` | 50244 | 163.05 | 66003 | 39 |
| **64KiB** | **`sha256`** | **388523** | **168.68** | **526798** | **53** |
| `64KiB` | `sha512_256` | 398853 | 164.31 | 526271 | 53 |
| **1MiB** | **`sha256`** | **3823624** | **274.24** | **9022783** | **80** |
| `1MiB` | `sha512_256` | 4471169 | 234.52 | 8967757 | 79 |

##### `gob`

| Size | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| **64B** | **`sha256`** | **11408** | **5.61** | **10200** | **192** |
| `64B` | `sha512_256` | 11550 | 5.54 | 10296 | 192 |
| **256B** | **`sha256`** | **11734** | **21.82** | **11288** | **193** |
| `256B` | `sha512_256` | 12272 | 20.86 | 11384 | 193 |
| **1KiB** | **`sha256`** | **15747** | **65.03** | **19720** | **199** |
| `1KiB` | `sha512_256` | 16797 | 60.96 | 19816 | 199 |
| **8KiB** | **`sha256`** | **65324** | **125.41** | **97992** | **211** |
| `8KiB` | `sha512_256` | 67651 | 121.09 | 98088 | 211 |
| **64KiB** | **`sha256`** | **438682** | **149.39** | **710863** | **223** |
| `64KiB` | `sha512_256` | 475039 | 137.96 | 710959 | 223 |
| **1MiB** | **`sha256`** | **2831201** | **370.36** | **10534892** | **239** |
| `1MiB` | `sha512_256` | 3521372 | 297.77 | 10534980 | 239 |

##### `binary`

| Size | Hash | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| **64B** | **`sha256`** | **1481** | **43.22** | **1976** | **25** |
| `64B` | `sha512_256` | 1662 | 38.50 | 2072 | 25 |
| **256B** | **`sha256`** | **2092** | **122.35** | **2920** | **28** |
| `256B` | `sha512_256` | 2364 | 108.28 | 3016 | 28 |
| `1KiB` | `sha256` | 7388 | 138.60 | 10200 | 34 |
| **1KiB** | **`sha512_256`** | **7313** | **140.02** | **10296** | **34** |
| **8KiB** | **`sha256`** | **49999** | **163.84** | **78745** | **46** |
| `8KiB` | `sha512_256` | 52444 | 156.21 | 78840 | 46 |
| **64KiB** | **`sha256`** | **364266** | **179.91** | **620961** | **58** |
| `64KiB` | `sha512_256` | 393598 | 166.51 | 621055 | 58 |
| **1MiB** | **`sha256`** | **2685813** | **390.41** | **9224386** | **74** |
| `1MiB` | `sha512_256` | 3216106 | 326.04 | 9224481 | 74 |

#### Full matrix ordered by hash

##### `sha256`

| Codec | Size | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| `json` | 64B | 1656 | 38.64 | 1870 | 21 |
| `gob` | 64B | 11408 | 5.61 | 10200 | 192 |
| **`binary`** | **64B** | **1481** | **43.22** | **1976** | **25** |
| `json` | 256B | 2398 | 106.75 | 2448 | 21 |
| `gob` | 256B | 11734 | 21.82 | 11288 | 193 |
| **`binary`** | **256B** | **2092** | **122.35** | **2920** | **28** |
| **`json`** | **1KiB** | **6442** | **158.97** | **8554** | **27** |
| `gob` | 1KiB | 15747 | 65.03 | 19720 | 199 |
| `binary` | 1KiB | 7388 | 138.60 | 10200 | 34 |
| **`json`** | **8KiB** | **47109** | **173.89** | **65898** | **39** |
| `gob` | 8KiB | 65324 | 125.41 | 97992 | 211 |
| `binary` | 8KiB | 49999 | 163.84 | 78745 | 46 |
| `json` | 64KiB | 388523 | 168.68 | 526798 | 53 |
| `gob` | 64KiB | 438682 | 149.39 | 710863 | 223 |
| **`binary`** | **64KiB** | **364266** | **179.91** | **620961** | **58** |
| `json` | 1MiB | 3823624 | 274.24 | 9022783 | 80 |
| `gob` | 1MiB | 2831201 | 370.36 | 10534892 | 239 |
| **`binary`** | **1MiB** | **2685813** | **390.41** | **9224386** | **74** |

##### `sha512_256`

| Codec | Size | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| `json` | 64B | 1690 | 37.86 | 1966 | 21 |
| `gob` | 64B | 11550 | 5.54 | 10296 | 192 |
| **`binary`** | **64B** | **1662** | **38.50** | **2072** | **25** |
| `json` | 256B | 2606 | 98.24 | 2544 | 21 |
| `gob` | 256B | 12272 | 20.86 | 11384 | 193 |
| **`binary`** | **256B** | **2364** | **108.28** | **3016** | **28** |
| **`json`** | **1KiB** | **7178** | **142.66** | **8651** | **27** |
| `gob` | 1KiB | 16797 | 60.96 | 19816 | 199 |
| `binary` | 1KiB | 7313 | 140.02 | 10296 | 34 |
| **`json`** | **8KiB** | **50244** | **163.05** | **66003** | **39** |
| `gob` | 8KiB | 67651 | 121.09 | 98088 | 211 |
| `binary` | 8KiB | 52444 | 156.21 | 78840 | 46 |
| `json` | 64KiB | 398853 | 164.31 | 526271 | 53 |
| `gob` | 64KiB | 475039 | 137.96 | 710959 | 223 |
| **`binary`** | **64KiB** | **393598** | **166.51** | **621055** | **58** |
| `json` | 1MiB | 4471169 | 234.52 | 8967757 | 79 |
| `gob` | 1MiB | 3521372 | 297.77 | 10534980 | 239 |
| **`binary`** | **1MiB** | **3216106** | **326.04** | **9224481** | **74** |

Interpretation:

- `sha256` generally wins or ties `sha512_256` on the same codec/path.
- `gob` remains the slowest and most alloc-heavy path.
- `json` is the most balanced choice in the middle of the payload range.
- `binary` is the best compact-format option for tiny and very large object sizes.
- The benchmark still measures end-to-end store round trips, not codec-only cost.

#### Focused isolation benchmarks (median of 5 runs)

`BenchmarkCodecMarshalUnmarshal` isolates pure serialization cost without hashing or store I/O. `BenchmarkHasherDigest` isolates pure hash throughput without encoding or backend work.

Recommendation: prefer `sha256` for hashing and `json` for the default portable payload codec, unless a workload is dominated by very small or very large object sizes, in which case `binary` is the better compact-format choice. `gob` remains the compatibility-only slow path and should not be the default selection.

##### Codec-only isolation

| Payload | Winner | Median ns/op | Runner-up | Notes |
|---|---|---:|---:|---|
| 64B | `binary` | 291 | `json` 666 | binary dominates tiny serialization |
| 256B | `binary` | 745 | `json` 1083 | binary still best |
| 1KiB | `json` | 2436 | `binary` 2809 | JSON is best in the middle |
| 8KiB | `json` | 16571 | `binary` 19309 | JSON stays best at modest sizes |
| 64KiB | `binary` | 133084 | `json` 138702 | binary regains the lead |
| 1MiB | `binary` | 1196082 | `gob` 1387775 | binary is fastest on very large payloads |

##### Hasher-only isolation

| Payload | Winner | Median ns/op | Runner-up | Notes |
|---|---|---:|---:|---|
| 64B | `sha256` | 203 | `sha512_256` 322 | sha256 is clearly faster |
| 256B | `sha256` | 258 | `sha512_256` 622 | same pattern |
| 1KiB | `sha256` | 577 | `sha512_256` 1498 | strong gap |
| 8KiB | `sha256` | 3730 | `sha512_256` 10015 | strong gap |
| 64KiB | `sha256` | 28736 | `sha512_256` 78971 | large gap |
| 1MiB | `sha256` | 458242 | `sha512_256` 1261319 | sha256 is much faster |

### 3.4 Run them

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

### 4.7 Choosing N (budget)

- **Memory:** ~a few hundred bytes/object (map entry + payload); 10^7 objects needs single-digit GBs of RAM.
- **FS:** on-disk cost is FS-dominated (block/cluster size + dir entries + fan-out) — expect ~the cluster size (commonly 4 KiB) per object: 10^6 ≈ 4+ GB, 10^7 ≈ 40+ GB. Start small.
- The FS temp dir is removed on a clean run; a failed/`Ctrl+C` run can leave it (under the system temp dir, named by `b.TempDir`).

## 5. Comparing results

- Benchmarks are comparable only on the **same machine** (and roughly same load) — why CI doesn't gate wall-clock.
- Use `-count=5`; compare medians/mins, not single runs.
- `ns/op` is the headline; `allocs/op` is machine-stable and the number to watch for regressions (performance §5, P-03).

## 6. Reference

- `performance.md` §5 (suite contract), §11 (scenario targets).
- [`AGENT.md`](./AGENT.md) (frozen package-local benchmark rules).
- `defaults.md` §6 (default targets, e.g. memory small Put/Get ≥ 100k obj/s, ≤ 5 allocs/op).
- Commands above assume PowerShell (Windows) or bash; the `go test` flags are identical everywhere.
