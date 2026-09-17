# Benchmark evaluation — `BenchmarkStoreCodecHashRoundTrip`

## Table of contents

- [Scope and source](#scope-and-source)
- [Executive summary](#executive-summary)
- [Winner by payload size](#winner-by-payload-size)
- [Throughput chart](#throughput-chart)
- [Time per operation chart](#time-per-operation-chart)
- [Bytes per operation chart](#bytes-per-operation-chart)
- [Allocations per operation chart](#allocations-per-operation-chart)
- [Codec and hasher pattern](#codec-and-hasher-pattern)
- [Observed deltas](#observed-deltas)
- [Recommendation](#recommendation)

This note summarizes the canonical benchmark matrix in [`store-codec-hash-roundtrip.json`](./store-codec-hash-roundtrip.json). It is intentionally concise, table-driven, and graph-friendly so it can be reviewed quickly and kept aligned with the source-of-truth JSON.

## Scope and source

| Item | Value |
|---|---|
| Benchmark | `BenchmarkStoreCodecHashRoundTrip` |
| Data source | [`store-codec-hash-roundtrip.json`](./store-codec-hash-roundtrip.json) |
| Payload sizes | 64B, 256B, 1KiB, 4KiB, 16KiB, 64KiB, 256KiB, 1MiB |
| Codecs | JSON, gzip, zlib, flate, gob, binary, cbor |
| Hashers | sha256, sha512, sha512_256 |
| Statistic | median of 1 local run |
| Runner note | Fresh single-run snapshot from the current local machine; not a portable cross-machine claim |

The benchmark is the canonical comparison for codec + hasher performance across the same object lifecycle. The result file records the winning combination per payload size plus the full per-row matrix. The same suite also includes the maintenance verification helper family (`BenchmarkVerifyMaintenanceChecks`), which compares the cost of stronger object-address validation (`sha256`) against auxiliary checks (`crc32`, `crc64`, `adler32`) without changing the underlying storage model.

## Executive summary

- Small payloads are dominated by fixed overhead. The fastest path is usually `binary` or a compact wire format, with the `sha512_256` hasher winning at the very smallest sizes.
- Medium payloads transition to `cbor` as the best encoder; the win is strong at 1KiB–4KiB and remains consistent at 16KiB and above.
- Large payloads saturate on a steady throughput band near ~430–480 MB/s once the dataset is large enough to amortize setup work.
- The `gzip`/`zlib`/`flate` family remains dramatically slower because compression cost dominates the object lifecycle, especially for tiny payloads.
- The best absolute end-to-end result in this snapshot is `cbor + sha256` at 256KiB and 1MiB, with 483.14 MB/s and 476.54 MB/s respectively.

## Winner by payload size

| Payload | Winner | Codec | Hasher | Throughput | ns/op | B/op | allocs/op |
|---|---|---|---|---:|---:|---:|---:|
| 64B | fastest | `binary` | `sha512_256` | 30.79 MB/s | 2,079 | 2,136 | 27 |
| 256B | fastest | `binary` | `sha512_256` | 88.96 MB/s | 2,878 | 3,528 | 32 |
| 1KiB | fastest | `cbor` | `sha512_256` | 114.88 MB/s | 8,914 | 12,208 | 43 |
| 4KiB | fastest | `cbor` | `sha512_256` | 129.23 MB/s | 31,696 | 48,497 | 53 |
| 16KiB | fastest | `cbor` | `sha256` | 298.57 MB/s | 54,876 | 178,450 | 61 |
| 64KiB | fastest | `cbor` | `sha256` | 428.64 MB/s | 152,893 | 687,126 | 67 |
| 256KiB | fastest | `cbor` | `sha256` | 483.14 MB/s | 542,588 | 2,751,524 | 75 |
| 1MiB | fastest | `cbor` | `sha256` | 476.54 MB/s | 2,200,407 | 10,511,204 | 83 |

## Throughput chart

```text
MB/s
64B    | ██████ 30.8
256B   | ███████████████ 89.0
1KiB   | ██████████████████████ 114.9
4KiB   | ██████████████████████████ 129.2
16KiB  | ███████████████████████████████ 298.6
64KiB  | ███████████████████████████████████████ 428.6
256KiB | ████████████████████████████████████████████ 483.1
1MiB   | ████████████████████████████████████████████ 476.5
```

## Time per operation chart

```text
ns/op
64B    | ██████████ 2,079
256B   | █████████████████ 2,878
1KiB   | ███████████████████████████ 8,914
4KiB   | ███████████████████████████████████ 31,696
16KiB  | █████████████████████████████████████████ 54,876
64KiB  | ███████████████████████████████████████████████████ 152,893
256KiB | ██████████████████████████████████████████████████████████████ 542,588
1MiB   | ████████████████████████████████████████████████████████████████████ 2,200,407
```

## Bytes per operation chart

```text
B/op
64B    | █████████ 2,136
256B   | ███████████████ 3,528
1KiB   | █████████████████████ 12,208
4KiB   | ████████████████████████████████ 48,497
16KiB  | ████████████████████████████████████████████ 178,450
64KiB  | ████████████████████████████████████████████████████ 687,126
256KiB | ████████████████████████████████████████████████████████████████ 2,751,524
1MiB   | ████████████████████████████████████████████████████████████████████████ 10,511,204
```

## Allocations per operation chart

```text
allocs/op
64B    | █████████████ 27
256B   | █████████████████ 32
1KiB   | ███████████████████████ 43
4KiB   | ███████████████████████████ 53
16KiB  | ███████████████████████████████ 61
64KiB  | ███████████████████████████████████ 67
256KiB | ████████████████████████████████████████ 75
1MiB   | ████████████████████████████████████████████ 83
```

## Codec and hasher pattern

The dominant pattern is not a single winner across all sizes; the winner shifts with payload size.

| Payload band | Dominant winner | Why it matters |
|---|---|---|
| 64B–256B | `binary + sha512_256` | Fixed overhead dominates; compact encoding and lighter digest work win more than compression or richer encoding ergonomics. |
| 1KiB–4KiB | `cbor + sha512_256` | The encoding has enough payload to amortize metadata costs while keeping the digest path lean. |
| 16KiB–1MiB | `cbor + sha256` | Throughput saturates at a strong steady band; `sha256` remains the most balanced choice for the larger payload range. |

## Observed deltas

These numbers are derived from the winner table and show how much the pool changes as payloads grow.

| Comparison | Result |
|---|---|
| 64B to 256B increase | 2.9x throughput lift (`30.8` to `89.0` MB/s) |
| 256B to 1KiB increase | 1.3x throughput lift (`89.0` to `114.9` MB/s) |
| 1KiB to 16KiB increase | 2.6x throughput lift (`114.9` to `298.6` MB/s) |
| 16KiB to 64KiB increase | 1.4x throughput lift (`298.6` to `428.6` MB/s) |
| 64KiB to 256KiB increase | 1.1x throughput lift (`428.6` to `483.1` MB/s) |
| 256KiB to 1MiB drop | 1.4% decline (`483.1` to `476.5` MB/s) |

Interpretation:

- Performance climbs steeply as the fixed-overhead cost is absorbed by real payload work.
- The curve flattens after ~64KiB, which is the expected point where the hot loop becomes throughput-limited by memory and CPU rather than allocation churn.
- The 1MiB case shows the system is already in a stable band; the difference from 256KiB is within normal noise for a local single-run measurement.

## Cost and allocation health

| Payload | Best winner | allocs/op | bytes/op | Interpretation |
|---|---|---:|---:|---|
| 64B | `binary` + `sha512_256` | 27 | 2,136 | Very low allocation churn but still with fixed setup overhead |
| 256B | `binary` + `sha512_256` | 32 | 3,528 | Still tiny; binary remains efficient |
| 1KiB | `cbor` + `sha512_256` | 43 | 12,208 | Allocation cost is still modest and almost flat relative to throughput |
| 4KiB | `cbor` + `sha512_256` | 53 | 48,497 | The higher payload makes the encoding strategy worth it |
| 16KiB | `cbor` + `sha256` | 61 | 178,450 | Strong efficiency once payload is substantial |
| 64KiB | `cbor` + `sha256` | 67 | 687,126 | Good steady-state characteristics |
| 256KiB | `cbor` + `sha256` | 75 | 2,751,524 | Peak bandwidth in this snapshot |
| 1MiB | `cbor` + `sha256` | 83 | 10,511,204 | Very large dataset; still near the performance ceiling |

The allocation profile grows gradually with payload size, which is the expected pattern for a codec + digest path. The major story is not a spike in allocations but the fact that `cbor` and `sha256` keep the hot path efficient across larger object sizes.

## Compression codecs: the outlier group

| Codec | Typical outcome | Notes |
|---|---|---|
| `gzip` | worst | Compression overhead dominates; tiny payloads collapse to near-zero throughput |
| `zlib` | worst | Similar to gzip, with slightly different cost geometry |
| `flate` | worst | Compression still dominates total cost |

The compression codecs are not useful for the canonical round-trip winner table when the goal is throughput. They are best thought of as compatibility or storage-optimization modes, not as the default high-throughput choice in this benchmark set.

## Recommendation

1. Use `cbor + sha256` as the default comparison benchmark for larger object sizes on this repo.
2. Use `binary + sha512_256` for tiny payloads when reducing fixed overhead matters more than cross-language compatibility.
3. Keep compression codecs out of the default decision path unless the application explicitly values size reduction over throughput.
4. Treat this as a local benchmark snapshot. Re-run with `-count=5` before using it as a release-quality performance claim.

## Best-fit summary bar

```text
Best end-to-end default for this local snapshot:
- 64B–256B: binary + sha512_256
- 1KiB–1MiB: cbor + sha256
```

This is a practical rule of thumb rather than a universal law. The real benchmark source remains the JSON matrix, which is the source of truth for any future comparison.
