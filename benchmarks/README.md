---
type: Guide
title: Benchmarks — go-cask
description: How to run and read the go-cask benchmark suites; the package-local benchmark files are split by subsystem, while the shared support file holds the common benchmark matrix and helpers.
version: v12
---

# Benchmarks — go-cask

The go-cask benchmarks measure the `cas` core's speed and allocations. They are **manual, on-demand tools** — CI never runs `-bench` (CI enforces correctness/race/coverage/fuzz). The normative contract is `performance.md` §5 and §11; [`AGENT.md`](./AGENT.md) freezes package-local benchmark rules; this file is the operator's run-and-read guide.

## Table of contents

- [Benchmark layout](#1-benchmark-layout)
- [Common flags](#2-common-flags)
- [Regular perf suite](#3-regular-perf-suite)
- [Bloom filter benchmark results](#32-bloom-filter-benchmark-results)
- [How to use benchmark data](#4-how-to-use-benchmark-data)

## 1. Benchmark layout

The suite is split by subsystem so each family keeps a coherent ownership boundary.

| File | Role |
|---|---|
| [`shared_test.go`](./shared_test.go) | Shared benchmark scaffolding: `testNote`, size matrix, codec/hasher matrix, summary helper |
| [`store_bench_test.go`](./store_bench_test.go) | Core store API benchmarks (`Put`, `Get`, round-trip) |
| [`backend_bench_test.go`](./backend_bench_test.go) | Raw backend write/read path for in-memory and fs backends |
| [`codec_bench_test.go`](./codec_bench_test.go) | Codec-only and full codec+hasher round-trip benchmarks |
| [`hash_bench_test.go`](./hash_bench_test.go) | Hasher digest and parse benchmarks |
| [`cache_bench_test.go`](./cache_bench_test.go) | Cache hit-path benchmarks |
| [`bloom_bench_test.go`](./bloom_bench_test.go) | Bloom filter add/contains and guard benchmarks |
| [`verify_bench_test.go`](./verify_bench_test.go) | Verify, parse, and concurrency checks |
| [`scale_bench_test.go`](./scale_bench_test.go) | On-demand state-scaling probes |

All files live in `benchmarks/` and use standard `go test -bench`. Every timed benchmark reports allocations. `BenchmarkScaleStoreEconomics` is a layout/count probe that times nothing. Throughput is reported only where one payload of known size defines each operation; benchmarks never invent byte counts for metadata, parsing, mixed concurrent, or layout work.

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
| `CASK_BENCH_SUMMARY=1` | Emits the extra summary logs used for manual comparison and diagnosis; default output stays standard Go benchmark output |

## 3. Regular perf suite

### 3.1 Benchmarks

The regular perf suite is split across the subsystem files listed above. The canonical comparisons are grouped by concern, not by a single monolithic file.

| Benchmark | File | Cases | Tells you |
|---|---|---|---|
| `BenchmarkStorePut` | [`store_bench_test.go`](./store_bench_test.go) | `steady-state` + `cold-start` across 64 B–1 MiB | Typed `Store[T].Put` cost under different setup assumptions |
| `BenchmarkStoreGetHot` / `BenchmarkStoreGetCold` / `BenchmarkStoreGetMixed` | [`store_bench_test.go`](./store_bench_test.go) | hot, cold-start, and mixed hot/cold read patterns | Whether reads are dominated by object locality or one-time setup |
| `BenchmarkStoreBaselineJSONSHA256` | [`store_bench_test.go`](./store_bench_test.go) | 1 KiB anchor | Single canonical comparison point for JSON + SHA-256 |
| `BenchmarkStoreWorkflowWriteReadVerify` | [`store_bench_test.go`](./store_bench_test.go) | fixed-size write/read/verify workflow | Realistic end-to-end object lifecycle | 
| `BenchmarkRoundTrip` | [`store_bench_test.go`](./store_bench_test.go) | one fixed-size cycle | Minimal store round-trip cost |
| `BenchmarkBackendWriteRead` / `BenchmarkBackendWriteReadBaseline` | [`backend_bench_test.go`](./backend_bench_test.go) | `mem` + `fs` across the same size ladder | Raw backend byte-path behavior and a clean baseline |
| `BenchmarkCodecPackageRoundTrip` / `BenchmarkCodecPackageMarshalUnmarshal` / `BenchmarkCodecRoundTripBaseline` | [`codec_bench_test.go`](./codec_bench_test.go) | codec/hash matrix + anchor baseline | Comparable end-to-end codec/hash combinations |
| `BenchmarkHashPackageDigest` / `BenchmarkHashPackageParse` / `BenchmarkHashPackageDigestBaseline` | [`hash_bench_test.go`](./hash_bench_test.go) | `sha256`/`sha512`/`sha512_256` × sizes + valid/invalid parse | Hash-only throughput and parsing costs |
| `BenchmarkCacheMemoryGet` / `BenchmarkCacheMemoryGetBaseline` / `BenchmarkCacheLRUGet` | [`cache_bench_test.go`](./cache_bench_test.go) | cached object access path + baseline hit | Cache hit-path cost and a clean single-object reference |
| `BenchmarkBloomStandard*` / `BenchmarkBloomStandardContainsHitBaseline` / `BenchmarkBloomCounting*` / `BenchmarkBloomPersistent*` / `BenchmarkBloomGuardExists` | [`bloom_bench_test.go`](./bloom_bench_test.go) | membership + update + guard checks + baseline hit | Bloom filter cost profile and a stable reference for hit-path checks |
| `BenchmarkVerify` / `BenchmarkVerifyBaseline` / `BenchmarkParseDigest` / `BenchmarkParallelPutGet` | [`verify_bench_test.go`](./verify_bench_test.go) | verify, parse, concurrency | Integrity, parsing, and hot/cold parallel access |

Store cases run against the in-memory backend (deterministic); the `fs` cases write to an auto-cleaned temp dir. The suite intentionally distinguishes steady-state, warm, cold, and baseline cases so the developer can tell whether a change affects the core path or just the one-time setup path.

### 3.1.1 How to read benchmark numbers

Read benchmark output by workload, not by a single aggregated `ns/op` value.

- Compare like with like: same machine, same Go version, same payload mix, same backend, same codec+hasher combination, and a repeated run (`-count=5` or more).
- Separate setup cost from steady-state cost: `setup/cold-start` includes object creation or temp-dir creation; `steady-state` measures the repeated operation after setup is done.
- Prefer anchors: a baseline case such as JSON + SHA-256 is a reference point, not a universal winner. Use it to judge deltas within the same family.
- Keep hot and cold measurements distinct: a mixed hot/cold benchmark is not a substitute for a true cold-start or steady-state read. A mixed run should document the hot-set size and cold ratio in its name or comment.
- Noise guard: if a single benchmark is more than ~2x away from the median on the same machine, rerun before interpreting it as a real regression. A noisy outlier is usually a scheduling or cache-state artifact, not a trustworthy result.
- Do not over-interpret one machine run. Small deltas can be noise; large deltas only matter when the workload and payload mix are the same.

The benchmark matrix stays intentionally narrow: a small set of anchor sizes and one reference case per family keeps the suite diagnosable without turning it into a wall of unanchored numbers.

### 3.2 Bloom filter benchmark results

The Bloom family measures the advisory hot-path pre-check layer in `cas/bloom`: the filters are intentionally separate from the authoritative CAS backend and are evaluated only for `Exists`-style membership cost and update overhead.

Run them directly:

```powershell
go test ./benchmarks/ -run=^$ -bench='^BenchmarkBloom' -benchmem -count=3
```

Measured on this runner (median of 3 runs):

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
|---|---:|---:|---:|---|
| `BenchmarkBloomStandardAdd` | 281 | 96 | 2 | fastest in-memory insert path |
| `BenchmarkBloomStandardContainsHit` | 203 | 96 | 2 | hit check is essentially constant-time and cheap |
| `BenchmarkBloomStandardContainsMiss` | 189 | 96 | 2 | miss check remains within the same cost band |
| `BenchmarkBloomCountingAddRemove` | 620 | 192 | 4 | counting filter is slower because it mutates counters |
| `BenchmarkBloomPersistentAdd` | 285 | 96 | 2 | persistent file-backed path is close to the in-memory standard path |
| `BenchmarkBloomPersistentContains` | 222 | 96 | 2 | persistent membership stays near standard lookup cost |
| `BenchmarkBloomGuardExists` | 266 | 96 | 2 | guard adds minimal overhead over the underlying filter |

Discussion:

- `standard` and `persistent` filters share almost the same cost profile; both are good hot-path pre-checks for large negative lookups.
- The counting filter is roughly 2x slower because it maintains per-slot counters and does more work on `Add`/`Remove`.
- The backend guard does not materially change the cost of the guard's fast path: the Bloom filter short-circuits on a miss, and a positive hit falls through to a real backend `Exists`.
- Short-circuiting negative results is the intended win: a Bloom miss avoids backend work, while a Bloom hit still validates against the underlying store. This preserves the repo's correctness model while getting the usual advisory lookup reduction.

In practice, the standard filter is the best default for a hot-path `Exists` pre-check. Use the counting variant when remove/update semantics matter; use the persistent variant when a restart-safe index is needed; keep the guard as the integration point that preserves the storage layer as the source of truth.

### 3.3 Codec/hash matrix

The raw round-trip matrix now lives in [`data/store-codec-hash-roundtrip.json`](./data/store-codec-hash-roundtrip.json). The README keeps the dense narrative summary; the JSON file is the canonical, queryable source for deeper slicing by codec, hasher, payload size, or runner metadata.

The matrix covers:

- sizes: `64B`, `256B`, `1KiB`, `8KiB`, `64KiB`, `1MiB`
- codecs: `json`, `gzip`, `zlib`, `flate`, `gob`, `binary`
- hashers: `sha256`, `sha512`, `sha512_256`

Every row in the JSON is one `codec + hasher + payload-size` cell. The benchmark measures full typed-store round trips: codec marshal/unmarshal, envelope, hashing, and memory-backend Put/Get. It does not isolate codec or hash cost by itself. Gob remains the Go-compatibility comparison; JSON remains the portable default; binary is the compact caller-defined format.

The JSON file also records runner metadata and the `winner` list used below so future analyses can be repeated without re-editing the README by hand.

```powershell
go test ./benchmarks/ -run=^$ -bench='^BenchmarkStoreCodecHashRoundTrip$' -benchmem -count=5
```

#### Winner by payload size

This is the canonical winner list from the JSON matrix, using the median of 5 runs for each `codec + hasher + payload` cell. Read `ns/op` first, then check `MB/s` and `allocs/op` together.

| Payload size | Winner | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| 64B | `binary` + `sha512_256` | 1479 | 43.28 | 2072 | 25 |
| 256B | `binary` + `sha256` | 2270 | 112.78 | 2920 | 28 |
| 1KiB | `json` + `sha256` | 6533 | 156.74 | 8553 | 27 |
| 8KiB | `binary` + `sha256` | 41771 | 196.12 | 78744 | 46 |
| 64KiB | `binary` + `sha512` | 318732 | 205.61 | 621151 | 58 |
| 1MiB | `binary` + `sha256` | 3334822 | 314.43 | 9224400 | 74 |

The main pattern is clear:

- `binary` wins the very small and large end of the matrix, with `sha512_256` or `sha256` depending on the exact size band.
- `json` + `sha256` still wins at `1KiB`, which is the throughput sweet spot for the portable default payload format.
- Compression codecs (`gzip`, `zlib`, `flate`) remain much slower in this end-to-end round-trip path, even though they shrink the payload bytes on disk. The cost is dominated by compression overhead and allocator churn, not by hashing alone.
- `gob` remains the compatibility-only slow path and is not a good default choice for performance-sensitive workloads.

`ns/op` is the headline summary for this section because it directly measures the end-to-end round-trip cost; `MB/s` and `allocs/op` are companion views for throughput and allocation pressure. Use them together when choosing a default, not one metric alone.

#### How to choose a codec + hasher for a real workload

There is no single universal winner. Use the choice that matches the payload size and the portability/compatibility requirements of the workload.

| Workload shape | Best measured choice | Why | Use when |
|---|---|---|---|
| Very small objects (`64B`–`256B`) | `binary` + `sha256` or `binary` + `sha512_256` | lowest end-to-end `ns/op` in the tiny-size region; `binary` avoids JSON overhead | compact binary payloads, internal storage, tiny objects, low-latency hot paths |
| Small-to-medium portable objects (`~1KiB`) | `json` + `sha256` | best in the middle size band while staying human-readable and interoperable | objects that may be inspected, logged, or exchanged across tools |
| Moderate objects (`8KiB` and up) | `binary` + `sha256` | lowest cost in the tested middle-to-large range; lower allocation/serialization overhead | app-local binary blobs, compact metadata, large internal object graphs |
| Large objects (`64KiB`–`1MiB`) | `binary` + `sha512` or `binary` + `sha256` | keeps the fastest end-to-end path while avoiding compression overhead | large payloads, caches, media chunks, archival data |
| Portable/default policy | `flate` + `sha256` | project default for durable payloads: compressed JSON/portable data with SHA-256 identity | default for general-purpose CAS objects |
| Compression-heavy workflow | `gzip`/`zlib`/`flate` only when size reduction matters more than speed | they can be acceptable when payloads are highly compressible and storage budget is tight | use only when the application explicitly prefers compressed size over raw throughput |

A practical default policy:

- Use `flate` + `sha256` as the project default when you want a durable, compact, interoperable configuration.
- Use `binary` + `sha256` when the payload is compact/structured and speed matters more than human readability.
- Use `binary` + `sha512` (or `sha512_256`) for larger binary payloads when the workload favors raw throughput and the caller is comfortable with a non-portable compact format.
- Use `json` + `sha256` as a debugging- or interoperability-oriented choice when the payload is mostly small/medium and the cost of compression is not desired.
- The benchmark matrix still shows that `json` and `binary` often outperform compression wrappers in strict end-to-end runtime; `flate` is the chosen default policy for durability and compactness, not necessarily the fastest microbenchmark winner.

This summary is intentionally short. For deeper analysis, use the JSON matrix directly: filter by `payload`, `codec`, `hasher`, or compare how the `winner` set changes across size bands without reformatting a huge markdown table by hand.

#### Focused isolation benchmarks (median of 5 runs)

`BenchmarkCodecMarshalUnmarshal` isolates pure serialization cost without hashing or store I/O. `BenchmarkHasherDigest` isolates pure hash throughput without encoding or backend work.

These are diagnostic benchmarks, not product defaults. They help answer: “is the slowdown mostly codec cost or hash cost?” They do not replace the end-to-end matrix above.

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

### 3.5 Run them

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
