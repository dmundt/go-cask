---
type: Specification
title: Performance — go-cask
description: Performance requirements and workflow for CASK — lock-free reads via atomic rename, one-pass streaming hashing, bounded allocations, scaling and object-count limits, packfiles as an extension, performance-test requirements, benchmarks and profiling.
version: v15
---

# Performance — go-cask

Every optimization MUST preserve the invariants of `cas-core.md`. Measure before and after — never micro-optimize without a benchmark. No unsafe/cgo/assembly/third-party speed dependencies; std-lib only (coding-guidelines §3). Related: cas-core, coding-guidelines, testing-strategy, operations.

## 1. Performance goals

| # | Goal | How |
|---|---|---|
| P-01 | Lock-free read path | `Get`/`Exists`/`List`/`Stats` take no lock (§2) |
| P-02 | One-pass serialization | the envelope is marshaled once, digested, then streamed to `Backend.Put`; hash-on-write surfaces (CLI, `examples/api`) stream through `io.MultiWriter`/`io.Copy`; never re-serialize or re-read source bytes |
| P-03 | Bounded allocations | hot paths flat; every benchmark calls `b.ReportAllocs()` |
| P-04 | No reflection | generics monomorphize; no runtime type assertions in hot paths |
| P-05 | Large objects never buffered | `Backend` streams `io.Reader`; HTTP layer streams bodies |

## 2. Lock-free reads (`fs.Backend`)

Writes are atomic (temp file → `f.Sync()` → `os.Rename`; Go's `os.Rename` also replaces an existing destination on Windows). Hence:
- `Get`/`Exists`/`List`/`Stats` MUST NOT acquire a lock — `os.Open`/`os.Stat` observe the old or new file, never a partial one.
- `Put` is idempotent (same hash ⇒ identical bytes): concurrent same-hash writers are safe; the last identical writer wins.
- On POSIX, unlink/rename keep open FDs valid: `Delete` during an in-flight read is safe.
- `Put`/`Delete` MAY use one `sync.Mutex` (never `RWMutex`).
- Document the atomicity argument in the `fs.Backend` type comment so the lock-free design survives refactors.

## 3. One-pass hashing (`Store.Put`)

Serialize once: `Store.Put` marshals the envelope into one buffer, digests that buffer with the injected `Hasher`, then streams it to `raw.Put` — the envelope is never marshaled twice. Surfaces that hash while writing (the CLI's `put` and `examples/api`'s upload) spool and hash in a single pass through `io.MultiWriter`/`io.Copy` into `sha256.NewHasher()`. `Backend.Put(ctx, d, r)` MUST stream `r` without buffering; the digest `d` is the trusted address (`Verify` is the integrity check).

## 4. Allocation & streaming rules

- `Store.Put`/`Get` (small objects) and `fs.Backend.Put`/`Get` SHOULD keep allocations flat/bounded; prove with `b.ReportAllocs()`.
- Reuse buffers via `sync.Pool` for scratch in the HTTP layer and verify/hexdump paths.
- Never `io.ReadAll` a large object in a byte-layer `Backend.Get` or `Store.GetRaw` — stream or use a bounded read. `Store.Get` MAY buffer because `Codec.Unmarshal` needs bytes; document that.
- Avoid `fmt` in hot paths — use `encoding/hex` directly, not `%x` loops.

## 5. Benchmark suite

Benchmarks live in `benchmarks/`. Suite: `BenchmarkStorePut`/`BenchmarkStoreGet` (64 B, 1 KiB, 1 MiB); `fs`-backend Put/Get (same sizes, flat vs fan-out); `BenchmarkRoundTrip`; `BenchmarkVerify`; `BenchmarkParseDigest` (valid + invalid); `BenchmarkParallelPutGet` (exercises §2); `BenchmarkScale{...}`.

- Every benchmark calls `b.ReportAllocs()` and `b.SetBytes()`.
- Store-logic benchmarks run against the in-memory `memory` backend (deterministic, no disk noise); disk behavior is covered by the `fs`-backend cases.
- No committed baseline and **no CI gate** (shared CI runners are too noisy; allocation regressions are caught by P-03 and review). No other document may promise a "benchstat gate" — `nightly.yml` only records `-bench` output. Run on demand: `go test ./benchmarks/ -bench=. -benchmem -count=5` (the suite lives in `benchmarks/`; see `benchmarks/README.md`).
- **State-scaling probes** (`BenchmarkScalePut/Get/Exists/List/Delete/Stats`) prefill a store to N, time the op at that size, and log a projection for 10^10 objects. Not part of CI twice over (CI runs no `-bench`, and each skips unless `CASK_SCALE_OBJECTS` is set), e.g. `CASK_SCALE_OBJECTS=1000000 go test ./benchmarks/ -run=^$ -bench=Scale -benchtime=100x -v`.
- The lock-free claim is exercised by `-race` tests and `BenchmarkParallelPutGet`.

## 6. Profiling workflow

1. Reproduce: `go test -bench=BenchmarkRoundTrip -benchmem`.
2. Profile: `go test -bench=... -cpuprofile p.out -memprofile m.out`, or `net/http/pprof` on the viewer server (expose only when explicitly enabled).
3. Attack order: allocations first (`pprof -alloc_objects`), then CPU (flamegraph), then lock contention (`-mutexprofile`) — expect none in the read path.

## 7. What NOT to optimize

Correctness, clarity, and documented contracts come first; reject any micro-optimization that obscures an invariant. No unsafe/cgo/assembly/third-party pools. Do not cache object bytes in memory as an implicit fast path (changes memory semantics) — use the documented cache layer.

## 8. Scaling & limits

### 8.1 Object count vs layout

One file per loose object; first hard limit is usually inodes/dir-entry performance, not disk. Within one directory ~10k–100k entries are fine on ext4; choose `FanOut`/`FanLevels` so leaf dirs stay under ~10k entries for the expected deduplicated count. `List`/`Stats` walk every file (O(object count)) — background work at 1M+; packfiles reduce to O(packs) via the index (§9).

| Layout | Directories | Practical loose ceiling (ext4, SSD) | Use |
|---|---|---|---|
| flat (0/0) | 1 | ~10k–100k | tiny stores / tests |
| Git-like (2,1) | 256 | ~1M–10M | default |
| wide (4,1) | 65,536 | ~10M–100M | many-object stores |
| deep 2/2 (2,2) | 65,536 leaf | ~10M–100M | many-object stores |
| + packfiles | ~2/pack | billions (bounded by pack count/disk) | ≥100M small (§9) |

### 8.2 Other limits

- Dedup reduces effective object count — size for the deduplicated count.
- Memory backend: RAM-bound, ~100+ B/object map overhead plus data; for tests/benchmarks/ephemeral stores only.
- SHA-256 (or SHA-1) collision risk is negligible at realistic scale.
- Reads stream one FD each; concurrent readers bounded by `ulimit` (packs amortize to 1 FD/pack).
- The single `Put` mutex serializes writers on one store; for write-heavy work shard, batch small writes into packs (§9), or scale behind the CAS API (which rate-limits per IP).
- Disk and inode exhaustion are eventual limits; dedup + GC (reachability) reclaim disk; inodes can precede space — packfiles fix both.

## 9. Packfiles (deferred extension)

Answer to "too many small loose objects"; implement behind the same `Backend` contract once the loose-store design is proven.

- **Motivation:** at ~1M+ objects — inode exhaustion, slow `List`/`Stats`, slow backups, per-file overhead.
- **Format** (Git-inspired, std-lib only): `.pack` (magic, version, size-prefixed objects appended, trailing whole-pack checksum) + `.idx` (sorted hash→offset with a Git-like fan-out table → O(1)–O(log n) lookup without full load).
- **Write policy:** objects ≤ threshold (e.g. 8 KiB) go into the current pack; flush at a target size (e.g. 64 MiB) or time budget; flushed packs are **immutable**.
- **Read:** `Backend.Get` stays streaming — index lookup, then `io.SectionReader`/`ReadAt` range read; never load a pack fully. Objects above threshold stay loose.
- **List/Stats:** from the index files — O(packs) + O(entries), not O(files).
- **GC:** rewrite packs dropping unreachable objects; verify pack checksums on rewrite.
- **Concurrency:** flushed packs immutable → lock-free reads survive; the open pack is append-only under the `Put` mutex.
- **Durability:** `f.Sync()` pack + index before exposing (operations §1); the batch window is the documented trade-off.
- **Trade-offs:** random reads cost an index lookup + seek; write latency becomes batched; keep behind the `Backend` contract so `Store[T]`, caches, and HTTP are untouched.
- **Acceptance:** same read API and all tests pass; p99 small-object read ≤ loose; `List`/`Stats` at 1M materially faster; `GC` handles packs; Put/Get win at ≥1M and inode count drops by orders of magnitude.

## 10. Content-defined chunking (deferred)

Rolling-hash chunking for very large blobs (dedup at chunk granularity). Design decision first, then behind the same `Backend` contract. Not started.

## 11. Performance-test requirements

Go benchmarks (with `-benchmem`) are the unit level. Scenario tests prove end-to-end scale; run every material core change (CI smoke: subset) and fully in nightly.

### 11.1 Scenarios

| # | Scenario | Setup |
|---|---|---|
| T-01 | Small-object storm | 100k × 1 KiB, `memory` + `fs` |
| T-02 | Large-object streaming | 10 × 1 GiB, `fs`; watch RSS during Put/Get |
| T-03 | Mixed workload | 90% small reads + 10% writes, warm store |
| T-04 | Concurrent readers | 32 goroutines reading the same 100k objects |
| T-05 | Concurrent writers | 8 goroutines writing distinct small objects |
| T-06 | List/Stats at scale | 100k and 1M objects; loose vs packed (§9 when landed) |
| T-07 | Fan-out comparison | flat vs (2,1) vs (2,2) vs (4,1) at 100k objects |
| T-08 | HTTP end-to-end | client → CAS API: streaming upload/download, 429 under load |

### 11.2 Metrics

Throughput (objects/s, MiB/s), latency p50/p95/p99, allocs/op, peak RSS, disk usage, inode count, open FDs, mutex contention (`-mutexprofile`).

### 11.3 Reference thresholds (defaults; calibrate on CI hardware)

| Metric | Target |
|---|---|
| Memory-backend small Put/Get | ≥100k obj/s; p99 ≤1 ms; ≤5 allocs/op |
| FS-backend small Put/Get (warm) | ≥10k obj/s; p99 ≤5 ms |
| Large-object streaming (1 GiB) | RSS stays ≤64 MiB above baseline |
| List at 1M objects (fs, (2,2)) | ≤30 s; Stats similar |
| Concurrent readers (T-04) | scales ~linearly; clean mutex profile |

### 11.4 Report & environment

Record CPU model, RAM, disk type, filesystem, Go version; run each scenario 3× and take the median. Run Go benchmarks with `-benchmem`; review allocs/op deltas by hand (no committed baseline/CI gate, §5). Scenario tests run via a dedicated `cmd/perftest` harness or `-tags=perftest`, printing a `scenario / metric / target / result` table. Attach the table to PRs touching the core; nightly compares against the previous baseline and flags regressions.

## 12. Checklist

- [x] `Get`/`Exists`/`List`/`Stats` are lock-free
- [x] hash-on-write in a single pass (CLI/HTTP: `io.MultiWriter` + `io.Copy`; core: marshal once, digest, stream)
- [x] benchmarks with `ReportAllocs` + `SetBytes` for small and large cases
- [x] `-race` concurrent Put/Get/Delete test green
- [x] no reflection/`unsafe`/external speed dependencies
- [x] profiling workflow documented and reproducible
- [x] fan-out layout chosen per expected object count (§8.1)
- [x] scenario tests T-01…T-08 exist and pass their thresholds
- [x] packfiles (§9), when implemented, meet all acceptance criteria
