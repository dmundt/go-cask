---
type: Guide
title: Benchmarks — go-cask
description: How to run and read the go-cask benchmark suites; the package-local benchmark files are split by subsystem, while the shared support file holds the common benchmark matrix and helpers.
version: v13
---

# Benchmarks — go-cask

The go-cask benchmarks measure the `cas` core's speed and allocations. They are **manual, on-demand tools** — CI never runs `-bench` (CI enforces correctness/race/coverage/fuzz). The normative contract is `performance.md` §5 and §11; this file is the operator's run-and-read guide.

## Table of contents

- [Benchmark layout](#1-benchmark-layout)
- [Common flags](#2-common-flags)
- [Regular perf suite](#3-regular-perf-suite)
- [Bloom filter benchmark results](#32-bloom-filter-benchmark-results)
- [Codec/hash matrix](#33-codechash-matrix)
- [Scale probes](#4-scale-probes-benchmarkscale)

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
| [`pack_bench_test.go`](./pack_bench_test.go) | Pack-layer chunking and sidecar metadata benchmarks |
| [`bloom_bench_test.go`](./bloom_bench_test.go) | Bloom filter add/contains and guard benchmarks |
| [`verify_bench_test.go`](./verify_bench_test.go) | Verify, parse, and concurrency checks |
| [`scale_bench_test.go`](./scale_bench_test.go) | On-demand state-scaling probes |
| `internal/index/index_bench_test.go` | Viewer metadata snapshot scan at 100/1,000 objects |

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

To capture a baseline artifact for a machine or branch, run:

```bash
./scripts/bench-baseline.sh
```

The script writes the raw benchmark output to `benchmarks/baseline.txt`. Keep that artifact alongside the machine details and compare future runs against it with `benchstat` or a similar diff tool.

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
| `BenchmarkCodecPackageRoundTrip` / `BenchmarkCodecPackageEncodeDecode` / `BenchmarkCodecRoundTripBaseline` | [`codec_bench_test.go`](./codec_bench_test.go) | JSON, CBOR, binary + hash matrix + anchor baseline | Comparable end-to-end codec/hash combinations and the lightweight CBOR metadata path |
| `BenchmarkHashPackageDigest` / `BenchmarkHashPackageParse` / `BenchmarkHashPackageDigestBaseline` | [`hash_bench_test.go`](./hash_bench_test.go) | `sha256`/`sha512`/`sha512_256` × sizes + valid/invalid parse | Hash-only throughput and parsing costs |
| `BenchmarkCacheMemoryGet` / `BenchmarkCacheMemoryGetBaseline` / `BenchmarkCacheLRUGet` | [`cache_bench_test.go`](./cache_bench_test.go) | cached object access path + baseline hit | Cache hit-path cost and a clean single-object reference |
| `BenchmarkPackSplitJoin` / `BenchmarkPackManifestRoundTrip` / `BenchmarkPackManifestSaveLoadFile` | [`pack_bench_test.go`](./pack_bench_test.go) | chunking and metadata round-trip cases | Pack-layer throughput and file-sidecar overhead without changing the CAS object model |
| `BenchmarkBloomStandard*` / `BenchmarkBloomStandardContainsHitBaseline` / `BenchmarkBloomCounting*` / `BenchmarkBloomPersistent*` / `BenchmarkBloomGuardExists` | [`bloom_bench_test.go`](./bloom_bench_test.go) | membership + update + guard checks + baseline hit | Bloom filter cost profile and a stable reference for hit-path checks |
| `BenchmarkVerify` / `BenchmarkVerifyBaseline` / `BenchmarkVerifyMaintenanceChecks` / `BenchmarkParseDigest` / `BenchmarkParallelPutGet` | [`verify_bench_test.go`](./verify_bench_test.go) | verify, maintenance checksum validators, parse, concurrency | Integrity, maintenance-layer checksum cost, and hot/cold parallel access |

Store cases run against the in-memory backend (deterministic); the `fs` cases write to an auto-cleaned temp dir. The suite intentionally distinguishes steady-state, warm, cold, and baseline cases so the developer can tell whether a change affects the core path or just the one-time setup path.

The maintenance verification family is intentionally separate from the canonical digest path: `BenchmarkVerifyMaintenanceChecks` measures `sha256`, `crc32`, `crc64`, and `adler32` checks against the same payload so the caller can compare the cost of an auxiliary consistency check without conflating it with the object-address algorithm.

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

- sizes: `64B`, `256B`, `1KiB`, `4KiB`, `16KiB`, `64KiB`, `256KiB`, `1MiB`
- codecs: `json`, `gzip`, `zlib`, `flate`, `gob`, `binary`, `cbor`
- hashers: `sha256`, `sha512`, `sha512_256`

Every row in the JSON is one `codec + hasher + payload-size` cell. The benchmark measures full typed-store round trips: codec encode/decode, envelope, hashing, and memory-backend Put/Get. It does not isolate codec or hash cost by itself. Gob remains the Go-compatibility comparison; JSON remains the portable default; binary and CBOR are the compact caller-defined formats.

The JSON file also records runner metadata and the `winner` list used below so future analyses can be repeated without re-editing the README by hand.

```powershell
go test ./benchmarks/ -run=^$ -bench='^BenchmarkStoreCodecHashRoundTrip$' -benchmem -count=1
```

The canonical JSON in this repo is a fresh local snapshot, not a universal cross-machine truth. Use `-count=5` or more when you need a medians-based comparison on a stable machine.

#### Winner by payload size

This is the current winner list from the JSON matrix on the local runner. Read `ns/op` first, then check `MB/s` and `allocs/op` together.

| Payload size | Winner | ns/op | MB/s | B/op | allocs/op |
|---|---|---:|---:|---:|---:|
| 64B | `binary` + `sha512_256` | 2079 | 30.79 | 2136 | 27 |
| 256B | `binary` + `sha512_256` | 2878 | 88.96 | 3528 | 32 |
| 1KiB | `cbor` + `sha512_256` | 8914 | 114.88 | 12208 | 43 |
| 4KiB | `cbor` + `sha512_256` | 31696 | 129.23 | 48497 | 53 |
| 16KiB | `cbor` + `sha256` | 54876 | 298.57 | 178450 | 61 |
| 64KiB | `cbor` + `sha256` | 152893 | 428.64 | 687126 | 67 |
| 256KiB | `cbor` + `sha256` | 542588 | 483.14 | 2751524 | 75 |
| 1MiB | `cbor` + `sha256` | 2200407 | 476.54 | 10511204 | 83 |

The main pattern is clear:

- `binary` still wins the tiny end-to-end matrix at `64B` and `256B`, especially with the shorter hashers in this snapshot.
- `cbor` is now the winner from `1KiB` upward across the current ladder after the hot-path rewrite, with `sha512_256` at `1KiB` and `4KiB` and `sha256` from `16KiB` onward.
- The crossover is steep and clean: small objects prefer `binary`, while the compact CBOR path takes over once the payload is large enough to amortize its overhead.
- `json` remains the portable default, but it is not the fastest in the current snapshot.
- Compression codecs (`gzip`, `zlib`, `flate`) remain much slower in this end-to-end round-trip path; they trade size reduction for a large runtime cost.

`ns/op` is the headline summary for this section because it directly measures the end-to-end round-trip cost; `MB/s` and `allocs/op` are companion views for throughput and allocation pressure. Use them together when choosing a default, not one metric alone.

#### How to choose a codec + hasher for a real workload

There is no single universal winner. Use the choice that matches the payload size, portability requirements, and the precision of the benchmark you trust.

| Workload shape | Best measured choice | Why | Use when |
|---|---|---|---|
| Very small objects (`64B`–`256B`) | `binary` + `sha512_256` | lowest end-to-end `ns/op` in the current snapshot | compact tiny metadata and low-latency hot paths |
| Small objects (`~1KiB`–`4KiB`) | `cbor` + `sha512_256` | the optimized CBOR path overtakes binary in this size band | compact metadata and manifest payloads where the value model is known |
| Medium objects (`~16KiB`–`256KiB`) | `cbor` + `sha256` | the large-object win is consistent across the current mid-range ladder | compact manifest and payload graphs that need the best throughput |
| Large objects (`~1MiB`) | `cbor` + `sha256` | continues to lead the largest size in the current snapshot | large compact payloads and wider object graphs |
| Portable/default policy | `json` + `sha256` | readable and broadly interop-friendly | debugging, tooling, exchange formats, human-inspected payloads |
| Compression-heavy workflow | `gzip`/`zlib`/`flate` only when size reduction matters more than speed | they can be acceptable when payloads are highly compressible and storage budget is tight | use only when the application explicitly prefers compressed size over raw throughput |

A practical default policy:

- Use `json` + `sha256` as the default portable choice when interoperability and readability matter.
- Use `binary` + `sha512_256` for the smallest hot objects in the current snapshot.
- Use `cbor` + `sha512_256` or `cbor` + `sha256` for the larger compact payloads that need the best current end-to-end throughput.
- Keep `gob` as a compatibility-only benchmark/reference path. It is not a good general-purpose default for CAS payloads.
- Compression wrappers remain a storage-optimization choice, not the runtime winner in the fresh matrix.

This summary is intentionally short. For deeper analysis, use the JSON matrix directly: filter by `payload`, `codec`, `hasher`, or compare how the `winner` set changes across size bands without reformatting a huge markdown table by hand.

#### Focused isolation benchmarks (median of 5 runs)

`BenchmarkCodecPackageEncodeDecode` isolates pure serialization cost without hashing or store I/O. `BenchmarkHashPackageDigest` isolates pure hash throughput without encoding or backend work.

These are diagnostic benchmarks, not product defaults. They help answer: “is the slowdown mostly codec cost or hash cost?” They do not replace the end-to-end matrix above.

Recommendation: prefer `sha256` for hashing, but do not assume `json` is the fastest payload format in the current matrix. After the CBOR hot-path rewrite, `cbor` wins the isolation benchmark across the mid/large payload range while `binary` still wins the tiny 64 B case. For portability and debugging, `json` remains the default readable choice. `gob` remains compatibility-only and not a general default.

##### Codec-only isolation

| Payload | Winner | Median ns/op | MB/s | Runner-up | Notes |
|---|---|---:|---:|---:|---|
| 64B | `binary` | 518.1 | 123.53 | `cbor` 713.7 | binary still wins the tiny-object case |
| 256B | `cbor` | 1091 | 234.56 | `binary` 1497 | the hot-path rewrite pulls CBOR ahead at small-but-real payloads |
| 1KiB | `cbor` | 2533 | 404.23 | `binary` 4515 | CBOR is meaningfully faster in the common metadata range |
| 8KiB | `cbor` | 15272 | 536.42 | `binary` 29740 | the gap grows sharply once payloads are no longer tiny |
| 64KiB | `cbor` | 118165 | 554.61 | `binary` 181002 | CBOR now dominates the medium/large serialization path |
| 1MiB | `cbor` | 1747146 | 600.17 | `binary` 3716153 | the optimization removed the previous generic allocation churn |

##### Hasher-only isolation

| Payload | Winner | Median ns/op | MB/s | Runner-up | Notes |
|---|---|---:|---:|---:|---|
| 64B | `sha256` | 257.1 | 248.90 | `sha512_256` 394.0 | sha256 is clearly faster |
| 256B | `sha256` | 571.6 | 447.84 | `sha512_256` 921.0 | same pattern |
| 1KiB | `sha256` | 1803 | 567.96 | `sha512_256` 2771 | strong gap |
| 8KiB | `sha256` | 13553 | 604.43 | `sha512_256` 19708 | strong gap |
| 64KiB | `sha256` | 100440 | 652.49 | `sha512_256` 142514 | large gap |
| 1MiB | `sha256` | 1683130 | 622.99 | `sha512_256` 2468173 | sha256 is much faster |

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

## 4. Scale probes

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
- `defaults.md` §6 (default targets, e.g. memory small Put/Get ≥ 100k obj/s, ≤ 5 allocs/op).
- Commands above assume PowerShell (Windows) or bash; the `go test` flags are identical everywhere.
