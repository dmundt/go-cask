---
title: Performance — go-cask
description: Performance requirements and workflow for CASK — lock-free reads via atomic rename, one-pass streaming hashing, bounded allocations, scaling and object-count limits, packfiles as an extension, performance-test requirements, benchmarks and profiling.
version: v2
---

# Performance — go-cask

> "Fast" is a correctness-preserving property: every optimization MUST keep the
> invariants of `cas-core.instructions.md` intact. Measure before and
> after — no micro-optimization without a benchmark.
>
> Related: `cas-core.instructions.md` (design/contracts),
> `coding-guidelines.instructions.md` (std-lib only, no `any`),
> `testing-strategy.instructions.md` (proving the fast paths are also correct),
> `operations.instructions.md` (durability, backup — packfile trade-offs).

---

## 1. Performance Goals

| #    | Goal                          | How                                                                 |
| ---- | ----------------------------- | ------------------------------------------------------------------- |
| P-01 | **Lock-free read path**       | `Get`/`Exists`/`List`/`Stats` take no lock — atomic-rename argument (§2) |
| P-02 | **One-pass streaming**        | hash computed *while* writing via `io.TeeReader`; never read bytes twice |
| P-03 | **Bounded allocations**       | hot paths keep allocations flat; every benchmark calls `b.ReportAllocs()` |
| P-04 | **No reflection**             | generics monomorphize; no runtime type assertions in hot paths       |
| P-05 | **Large objects never buffered** | `RawStore` streams `io.Reader`; HTTP layer streams bodies           |

---

## 2. Lock-Free Reads (`FSRawStore`)

The single biggest "fast" win, and it makes the code *simpler*:

- Writes are already atomic: temp file → `f.Sync()` → `os.Rename`. POSIX
  `rename(2)` is atomic; Go's `os.Rename` also replaces an existing
  destination on Windows.
- Therefore `Get`/`Exists`/`List`/`Stats` MUST NOT take a lock: `os.Open` /
  `os.Stat` observe either the old or the new file, never a partial one.
- `Put` is idempotent (same hash ⇒ identical bytes): concurrent writers of the
  same hash are safe — the last identical writer wins.
- On POSIX, unlink/rename keep already-open file descriptors valid: `Delete`
  during an in-flight read is safe.
- Consequence: the coarse `sync.RWMutex` disappears. At most a single
  `sync.Mutex` coordinates `Put`/`Delete` on backends that need it. Reads are
  wait-free.

Rules:

1. `Get`/`Exists`/`List`/`Stats` MUST NOT acquire a lock.
2. `Put`/`Delete` MAY use one `sync.Mutex` (not `RWMutex`).
3. Document the atomicity argument in the `FSRawStore` type comment so the
   lock-free design survives refactors.

---

## 3. One-Pass Hashing (`Store.Put`)

- Serialize once, hash while streaming: feed the serialized bytes through an
  `io.TeeReader` into the hasher *before* `raw.Put` — never read the content
  twice.
- `RawStore.Put(ctx, h, r)` MUST stream `r` without buffering (the hash in
  `h` is trusted as the address; `Verify` is the integrity check).

---

## 4. Allocation & Streaming Rules

1. Hot paths (`Store.Put`/`Get` on small objects, `FSRawStore.Put`/`Get`)
   SHOULD keep allocations flat and bounded; prove it with
   `b.ReportAllocs()`.
2. Reuse buffers: `sync.Pool` for scratch buffers in the HTTP layer and the
   verify/hexdump paths.
3. Never `io.ReadAll` a large object in `Get`/`GetRaw` — stream, or use a
   bounded read. `GetTyped` may buffer only because `Codec.Decode` needs
   bytes; document that.
4. No `fmt` in hot paths where avoidable — use `encoding/hex` directly, not
   `%x` formatting loops.
5. No external dependencies for speed: no `unsafe`, no cgo, no assembly, no
   third-party pools (coding-guidelines §3).

---

## 5. Benchmark Suite & CI Gates

Benchmarks live next to the code (`cas/`, `gitlike/` where meaningful):

| Benchmark                  | Cases                                        |
| -------------------------- | -------------------------------------------- |
| `BenchmarkStorePut`        | 64 B, 1 KiB, 1 MiB                           |
| `BenchmarkStoreGet`        | same                                         |
| `BenchmarkFSRawStorePut/Get` | same, flat vs. fan-out layout               |
| `BenchmarkRoundTrip`       | Put + Get combined                           |
| `BenchmarkVerify`          | intact object                                |
| `BenchmarkParseHash`       | valid + invalid inputs                       |
| `BenchmarkParallelPutGet`  | concurrent writers/readers (exercises §2)    |

- Every benchmark calls `b.ReportAllocs()` and `b.SetBytes()`.
- Benchmarks that isolate store logic from disk noise run against
  `MemoryRawStore` (deterministic, no I/O variance); disk behavior is
  covered separately by the `BenchmarkFSRawStore*` cases.
- CI compares against a committed baseline with `benchstat`; a regression
  beyond the agreed threshold (e.g. >10% time or allocs on the small-object
  cases) fails the build.
- The lock-free claim is exercised by `-race` tests (testing-strategy §4.4)
  and `BenchmarkParallelPutGet`.

---

## 6. Profiling Workflow

1. Reproduce: `go test -bench=BenchmarkRoundTrip -benchmem`.
2. Profile: `go test -bench=... -cpuprofile p.out -memprofile m.out`, or
   `net/http/pprof` on the server (std-lib; gate it like the Swagger
   explorer — explicitly enabled, documented deviation if exposed).
3. Order of attack: allocations first (`pprof -alloc_objects`), then CPU
   (flamegraph), then lock contention (`-mutexprofile`) — expect none in the
   read path.

---

## 7. What NOT to Optimize

- Correctness, clarity, and the documented contracts come first. If a
  micro-optimization obscures an invariant, it is rejected.
- No `unsafe`, no cgo, no assembly, no third-party speed dependencies.
- Do not cache object *bytes* in memory as an implicit fast path — that
  changes memory semantics; use the documented cache layer instead.

---

## 8. Scaling & Limits (discussion)

These are the practical limits that drive layout and pack decisions. They are
**filesystem/hardware reality**, not Go limitations.

### 8.1 Object count vs. layout

The filesystem backend stores one file per loose object, so the first hard
limit is usually **inodes and directory-entry performance**, not disk space.

| Layout             | Directories        | Practical loose-object ceiling (ext4, SSD) | When to use                    |
| ------------------ | ------------------ | ------------------------------------------ | ------------------------------ |
| flat (0/0)         | 1                  | ~10k–100k (dir lookup degrades)            | tiny stores / tests            |
| Git-like (2,1)     | 256                | ~1M–10M                                    | default                        |
| wide (4,1)         | 65,536             | ~10M–100M                                  | many-object stores             |
| deep 2/2 (2,2)     | 65,536 leaf dirs   | ~10M–100M                                  | many-object stores             |
| any + packfiles    | ~2 per pack        | billions (bounded by pack count/disk)      | ≥ 100M small objects (§9)      |

- Within one directory, ~10k–100k entries are fine on ext4; beyond that,
  lookup and `List` degrade. Choose `FanOut`/`FanLevels` so leaf directories
  stay under ~10k entries for the *expected* object count (with dedup in
  mind, since identical content counts once).
- `List`/`Stats` walk every file: O(object count). At 1M+ objects that is a
  background operation, not a hot path. Packfiles reduce this to O(packs)
  via the index (§9).

### 8.2 Other limits

- **Dedup** reduces the effective object count: object count ≠ stored-file
  count when content repeats. Size your layout for the deduplicated count.
- **Memory backend**: bounded by RAM — map entry overhead (~100+ B/object)
  plus the data itself. Use for tests/benchmarks/ephemeral stores, not large
  production stores (cas-core §4.5).
- **Hash width**: SHA-256 (or even SHA-1) collision risk is negligible at any
  realistic scale — this is not a practical limit.
- **Open FDs**: reads stream one FD each; concurrent readers are bounded by
  `ulimit`. Packs amortize this to one FD per pack.
- **Write throughput**: the single `Put` mutex serializes writers on one
  store. For write-heavy workloads: shard stores, batch small writes into
  packfiles (§9), or scale out behind the CAS API (which also rate-limits per
  IP).
- **Disk**: space is the eventual limit; dedup + `GC` (reachability) reclaim
  it. Inode exhaustion can precede space exhaustion — packfiles fix both.

---

## 9. Packfiles (possible extension)

Packfiles are the planned answer to "too many small loose objects". Deferred
until the loose-store design is proven — then implemented behind the same
`RawStore` contract.

### 9.1 Motivation

At ~1M+ small objects: inode exhaustion, slow `List`/`Stats` (O(files)),
slow backups (millions of files), per-file open/create overhead. Packfiles
group many small objects into a few large files.

### 9.2 Design sketch

- **Format** (Git-inspired, std-lib only): a `.pack` file — magic, version,
  then objects appended (size-prefixed, full serialized bytes, with a
  trailing checksum for the whole pack) — plus a `.idx` file: sorted
  hash → offset (with a fan-out table like Git's, so lookup is O(1)–O(log n)
  without loading the index fully).
- **Write policy**: objects ≤ threshold (e.g. 8 KiB) go into the current
  pack; the pack is flushed when it reaches a target size (e.g. 64 MiB) or a
  time budget; flushed packs are **immutable**.
- **Read path**: `RawStore.Get` stays streaming — locate the offset via the
  index, read the range (`io.SectionReader` / `ReadAt`); never load a pack
  into memory. Objects above the threshold stay loose.
- **List/Stats**: derived from the index files — O(packs) + O(index
  entries), not O(files).
- **GC**: rewrite packs, dropping unreachable objects (mark-and-sweep at pack
  level); verify pack checksums on rewrite.
- **Concurrency**: flushed packs are immutable → the lock-free read path
  survives; the *open* pack is append-only under the `Put` mutex.
- **Durability**: `f.Sync()` the pack and index before exposing (operations
  §1); the batch window is the durability trade-off — document it.

### 9.3 Trade-offs

- Random-access reads cost an index lookup + seek (vs. direct open for
  loose).
- Write latency becomes batched (small objects wait for a flush).
- Complexity: keep it behind the `RawStore` contract (a `PackedRawStore`
  wrapper or an `FSRawStore` mode), so `Store[T]`, caches, and the HTTP layer
  are untouched.

### 9.4 Acceptance criteria

- Same read API; all existing tests (testing-strategy) pass for packed
  stores.
- `p99` small-object read ≤ loose-store read; `List`/`Stats` at 1M objects
  materially faster than loose.
- `GC` handles packs (rewrites, drops dead objects, verifies checksums).
- Benchmarks show a small-object Put/Get win at ≥ 1M objects; inode count
  drops by orders of magnitude.

---

## 10. Content-Defined Chunking (possible extension)

Rolling-hash chunking for very large blobs (dedup at chunk granularity).
Acceptance: a design decision first, then implementation behind the same
`RawStore` contract. Not started.

---

## 11. Performance Test Requirements

Go benchmarks (report allocs, benchstat gates) are the unit level. The
following **scenario tests** prove end-to-end behavior at scale. Run them on
every material core change (CI smoke: subset) and fully in nightly.

### 11.1 Scenarios

| # | Scenario                          | Setup                                                    |
| - | --------------------------------- | -------------------------------------------------------- |
| T-01 | Small-object storm            | 100k × 1 KiB, `MemoryRawStore` + `FSRawStore`            |
| T-02 | Large-object streaming        | 10 × 1 GiB, `FSRawStore`; watch RSS during Put/Get      |
| T-03 | Mixed workload                | 90% small reads + 10% small writes, warm store           |
| T-04 | Concurrent readers            | 32 goroutines reading the same 100k objects              |
| T-05 | Concurrent writers            | 8 goroutines writing distinct small objects              |
| T-06 | List/Stats at scale           | 100k and 1M objects; loose vs. packed (§9 when landed)   |
| T-07 | Fan-out comparison            | flat vs. (2,1) vs. (2,2) vs. (4,1) at 100k objects       |
| T-08 | HTTP end-to-end               | client → CAS API: streaming upload/download, 429 under load |

### 11.2 Metrics

Throughput (objects/s, MiB/s), latency p50/p95/p99, allocs/op, peak RSS,
disk usage, inode count, open FDs, mutex contention (`-mutexprofile`).

### 11.3 Reference thresholds (defaults, calibrate on CI hardware)

| Metric                                  | Target                                    |
| --------------------------------------- | ----------------------------------------- |
| Memory-backend small Put/Get            | ≥ 100k obj/s; p99 ≤ 1 ms; ≤ 5 allocs/op   |
| FS-backend small Put/Get (warm)         | ≥ 10k obj/s; p99 ≤ 5 ms                   |
| Large-object streaming (1 GiB)          | RSS stays ≤ 64 MiB above baseline         |
| List at 1M objects (fs, (2,2))          | ≤ 30 s; Stats similar                     |
| Concurrent readers (T-04)               | scales ~linearly; mutex profile clean     |

### 11.4 Report & environment

- Record: CPU model, RAM, disk type (SSD/HDD), filesystem, Go version; run
  each scenario 3× and take the median.
- Go benchmarks: `benchstat` against the committed baseline. Scenario tests:
  a dedicated `cmd/perftest` harness (or `-tags=perftest` tests) printing a
  `scenario / metric / target / result` table.
- Attach the table to PRs that touch the core; nightly runs compare against
  the previous baseline and flag regressions.

---

## 12. Checklist

- [ ] `Get`/`Exists`/`List`/`Stats` are lock-free (no lock in those methods)
- [ ] hash-on-write in a single pass (`io.TeeReader`)
- [ ] benchmarks with `ReportAllocs` + `SetBytes` for small and large cases
- [ ] CI `benchstat` gate against a committed baseline
- [ ] `-race` concurrent Put/Get/Delete test green
- [ ] no reflection/`unsafe`/external speed dependencies
- [ ] profiling workflow documented and reproducible
- [ ] fan-out layout chosen per expected object count (§8.1)
- [ ] performance-test scenarios T-01…T-08 exist and pass their thresholds
- [ ] packfiles (§9), when implemented, meet all acceptance criteria
