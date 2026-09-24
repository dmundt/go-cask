---
type: Specification
title: Performance — go-cask
description: Performance requirements and workflow for CASK — lock-free reads via atomic rename, one-pass streaming hashing, bounded allocations, scaling and object-count limits, the optional packfile backend, performance-test requirements, benchmarks and profiling.
version: v20
---

# Performance — go-cask

Every optimization MUST preserve the invariants of `cas-core.md`. Measure before and after — never micro-optimize without a benchmark. No unsafe/cgo/assembly/third-party speed dependencies; std-lib only (coding-guidelines §3). Related: cas-core, coding-guidelines, testing-strategy, operations.

## 1. Performance goals

| # | Goal | How |
|---|---|---|
| P-01 | Lock-free read path | `Get`/`Exists`/`List`/`Stats` take no lock (§2) |
| P-02 | One-pass serialization | the envelope is marshaled once, digested, then streamed to `Backend.Put`; hash-on-write surfaces (CLI, `examples/api`) stream through `io.MultiWriter`/`io.Copy`; never re-serialize or re-read source bytes |
| P-03 | Bounded allocations | hot paths flat; every benchmark calls `b.ReportAllocs()` |
| P-04 | No reflection-based dispatch | generics monomorphize and no exported value is `any`; the typed layer's one structural `Validator` assertion and the internal nil check are the recorded exceptions, paid once per `Put`/`Get` |
| P-05 | Large objects never buffered | `Backend` streams `io.Reader`; HTTP layer streams bodies |

**P-04's two exceptions are deliberate, and they are measurable.** Every `Store.Put` and `Store.Get` runs one structural `any(obj).(Validator)` assertion (`cas/store.go`, the object-invariant contract, cas-core §4.8) and one `reflect.ValueOf` — the latter through `isNilValue`, the core's only use of reflection, which rejects a nil-interface or nil-pointer object before it is encoded or returned. A reviewer weighing a hot-path change should count them: the assertion and the reflection are paid per operation, and removing them is a design change (a `Validator`-carrying type constraint, or a fast path for types that do not implement it) that needs a benchmark, not this document.

## 2. Lock-free reads (`fs.Backend`)

Writes are atomic (temp file → `f.Sync()` → `os.Rename`; Go's `os.Rename` also replaces an existing destination on Windows). Hence:
- `Get`/`Exists`/`List`/`Stats` MUST NOT acquire a lock — `os.Open`/`os.Stat` observe the old or new file, never a partial one.
- `Put` is idempotent (same hash ⇒ identical bytes): concurrent same-hash writers are safe; the last identical writer wins.
- On POSIX, unlink/rename keep open FDs valid: `Delete` during an in-flight read is safe.
- `Put`/`Delete` MAY use one `sync.Mutex` (never `RWMutex`).
- Document the atomicity argument in the `fs.Backend` type comment so the lock-free design survives refactors.

## 3. One-pass hashing (`Store.Put`)

Serialize once: `Store.Put` marshals the envelope into one buffer, digests that buffer with the injected `Hasher`, then streams it to `raw.Put` — the envelope is never marshaled twice. Surfaces that hash while writing (the CLI's `put` and `examples/api`'s upload) spool and hash in a single pass through `io.MultiWriter`/`io.Copy` into `sha256.NewHasher()`. `Backend.Put(ctx, d, r)` MUST stream `r` without buffering; the digest `d` is the trusted address (`Verify` is the integrity check).

**Recorded sidecar checksums cost one extra read per recorded `Put`** (`cas/verify/sidecar`, operations §6). A record must describe the *stored* bytes, and `cas.Hasher` exposes only a reader-based `Digest` — no incremental writer, and the sidecar may not add one to the core — so the checksum is computed in one streaming pass over what `Get` returns after the write publishes the object, plus one extra file and atomic rename per `Put`. Reads (`Get`/`Exists`/`List`/`Stats`) are untouched, so the cost sits only on the write path of a store that opted in; `Rec.Verifier(...)`'s `VerifyAll` is likewise one streaming read per recorded object. This is the price of the feature, stated rather than hidden: recording is off by default, and a caller that never wraps a backend pays nothing.

## 4. Allocation and streaming rules

- `Store.Put`/`Get` (small objects) and `fs.Backend.Put`/`Get` SHOULD keep allocations flat/bounded; prove with `b.ReportAllocs()`.
- Reuse buffers via `sync.Pool` for scratch in the HTTP layer and verify/hexdump paths.
- Never `io.ReadAll` a large object in a byte-layer `Backend.Get` or `Store.GetRaw` — stream or use a bounded read. `Store.Get` MAY buffer because `Codec.Decode` needs bytes; document that.
- Avoid `fmt` in hot paths — use `encoding/hex` directly, not `%x` loops.

## 5. Benchmark suite

Benchmarks live in `benchmarks/`. Suite: `BenchmarkStorePut` (steady-state + cold-start, 64 B–1 MiB) and the read patterns `BenchmarkStoreGetHot`/`BenchmarkStoreGetCold`/`BenchmarkStoreGetMixed`; the raw byte path `BenchmarkBackendWriteRead` (`mem`/`fs` × steady-state/cold-start); the codec/hash matrix `BenchmarkCodecPackageRoundTrip` (`json`/`gzip`/`zlib`/`flate`/`gob`/`binary`/`cbor` × `sha256`/`sha512`/`sha512_256` × size); `BenchmarkRoundTrip`; `BenchmarkVerify`; `BenchmarkParseDigest` (valid + invalid); `BenchmarkParallelPutGet` (exercises §2); `BenchmarkScale{...}`; `BenchmarkBloom*` families for advisory pre-check layers. `benchmarks/AGENT.md` freezes the package-local measurement and maintenance rules; `benchmarks/README.md` is the run-and-read guide.

### 5.1 Optional Bloom acceleration

The `cas/bloom` layer is an optional, advisory optimization and MUST NOT change the storage-core correctness model. Bloom filters are allowed for hot-path absence checks and duplicate suppression in front-end indexes, but they are never the source of truth for object existence or reachability.

- A negative Bloom result is definitive for a well-formed filter and may short-circuit a lookup.
- A positive Bloom result is only a hint: the wrapped backend/store must still verify the digest's real existence.
- Standard, counting, and persistent variants are all production-safe only as advisory front ends; they do not participate in `Verify`, `GC`, or `Prune` semantics.
- The Bloom filter's bit-index derivation is independent from the CAS digest algorithm: the object hash remains the caller-owned `cas.Hasher` contract, while the Bloom filter chooses bit positions in its own bitmap.

This rule keeps the optional optimization layer outside the core invariants while still allowing a large store to skip wasted backend lookups in the hot path.

- Every timed benchmark calls `b.ReportAllocs()`. Non-timed layout/economics probes MAY omit it.
- Benchmarks call `b.SetBytes()` only when one operation processes one payload of known size; operations such as `Exists`, `Delete`, `List`, `Stats`, digest parsing, and mixed concurrent workloads MUST NOT invent a byte count.
- Store-logic benchmarks run against the in-memory `memory` backend (deterministic, no disk noise); disk behavior is covered by the `fs`-backend cases.
- The codec/hash matrix MUST apply identical objects, sizes, backend, and Put+Get work to every combination. It is a comparative end-to-end benchmark, not a standalone codec or hash microbenchmark.
- **No CI gate and no scheduled run.** CI runs no `-bench` (`ci.yml` has no benchmark job), and the repository has no nightly workflow at all — benchmark results are never a required check (shared CI runners are too noisy; allocation regressions are caught by P-03 and review). No other document may promise a "benchstat gate" or a nightly benchmark job. What exists instead is one committed, machine-specific reference dump — `benchmarks/data/baseline.txt` — plus two manual scripts: `scripts/bench-baseline.sh` re-captures the suite (`-count=1`), archives the raw output as `benchmarks/data/archive/baseline-<UTC-stamp>.txt`, and refreshes the canonical copy unless `--capture-only` is given; `scripts/bench-compare.sh` picks the baseline first, captures through `bench-baseline.sh --capture-only` into `benchmarks/data/current.txt`, never writes the canonical reference itself, and prints a `benchstat` diff when `benchstat` is installed (exit code 2 when it is not). A maintainer refreshes the reference by hand, on demand, on a quiet machine — never on a schedule and never per PR — and the dump is a comparison point, not a threshold. Run the suite on demand: `go test ./benchmarks/ -bench=. -benchmem -count=5` (the suite lives in `benchmarks/`; see `benchmarks/README.md`).
- **State-scaling probes** (`BenchmarkScalePut/Get/Exists/List/Delete/Stats`) prefill a store to N, time the op at that size, and log a projection for 10^10 objects. Not part of CI twice over (CI runs no `-bench`, and each skips unless `CASK_SCALE_OBJECTS` is set), e.g. `CASK_SCALE_OBJECTS=1000000 go test ./benchmarks/ -run=^$ -bench=Scale -benchtime=100x -v`.
- The lock-free claim is exercised by `-race` tests and `BenchmarkParallelPutGet`.

## 6. Profiling workflow

1. Reproduce: `go test -bench=BenchmarkRoundTrip -benchmem`.
2. Profile: `go test -bench=... -cpuprofile p.out -memprofile m.out`, or `net/http/pprof` on the viewer server (expose only when explicitly enabled).
3. Attack order: allocations first (`pprof -alloc_objects`), then CPU (flamegraph), then lock contention (`-mutexprofile`) — expect none in the read path.

## 7. What NOT to optimize

Correctness, clarity, and documented contracts come first; reject any micro-optimization that obscures an invariant. No unsafe/cgo/assembly/third-party pools. Do not cache object bytes in memory as an implicit fast path (changes memory semantics) — use the documented cache layer.

## 8. Scaling and limits

### 8.1 Object count vs layout

One file per loose object; first hard limit is usually inodes/dir-entry performance, not disk. Within one directory ~10k–100k entries are fine on ext4; choose `FanOut`/`FanLevels` so leaf dirs stay under ~10k entries for the expected deduplicated count. `List`/`Stats` walk every file (O(object count)) — background work at 1M+; the shipped packfile backend does not change that, because it keeps the loose tree alongside its packs (§9).

| Layout | Directories | Practical loose ceiling (ext4, SSD) | Use |
|---|---|---|---|
| flat (0/0) | 1 | ~10k–100k | tiny stores / tests |
| Git-like (2,1) | 256 | ~1M–10M | default |
| wide (4,1) | 65,536 | ~10M–100M | many-object stores |
| deep 2/2 (2,2) | 65,536 leaf | ~10M–100M | many-object stores |
| + packfs | loose tree + `packs/` | unchanged (same loose files, plus pack copies) | read-open amortization, not density (§9) |

### 8.2 Other limits

- Dedup reduces effective object count — size for the deduplicated count.
- Memory backend: RAM-bound, ~100+ B/object map overhead plus data; for tests/benchmarks/ephemeral stores only.
- SHA-256 (or SHA-1) collision risk is negligible at realistic scale.
- Reads stream one FD each; concurrent readers bounded by `ulimit` (a packfs batch amortizes to 1 FD/pack).
- The single `Put` mutex serializes writers on one store; for write-heavy work, shard or scale behind the CAS API (which rate-limits per IP). The packfile backend does not batch writes — it appends each `Put` immediately — and serializes reads against writes on the same mutex (§9).
- Disk and inode exhaustion are eventual limits; dedup + GC (reachability) reclaim disk on `fs` and `mem`; inode count is unchanged by the packfile backend, which writes both the loose object and the pack copy (§9).

## 9. Packfiles (shipped extension: `cas/backend/packfs`)

`cas/backend/packfs` ships as an opt-in backend — the loose tree plus append-only pack files and a JSON
index, selected by `cask -backend packfs` (cas-core §4.14). This section states what it does; the design
sketch it replaced (a magic/version/checksum `.pack` with a `.idx` fan-out table, an 8 KiB size threshold,
"objects above the threshold stay loose", pack-rewrite GC) is withdrawn, because none of it is what runs.

- **Shape.** `<base>/loose/` is a full `fs.Backend` (fan-out default). `<base>/packs/current.pack` is the
  active pack, appended under `O_APPEND` and rotated at `PackMaxBytes` (default 64 MiB) or `PackMaxEntries`
  (default 10 000) into `<base>/packs/pack-<unixnano>.pack`; `<base>/packs/index.json` maps each packed
  digest (hex) to its `{pack, offset, size}`. A record is `[uint32 BE digest length][digest][uint64 BE
  payload length]` followed by the payload — no magic, no format version, no checksum, so the index is the
  only description of a pack's contents.
- **Write policy: every object is both loose and packed.** There is no size threshold and nothing stays only
  loose: `Put` spools the stream to a scratch file, writes the object through the fs backend's atomic
  `Sync`+rename path, appends it to the active pack, and rewrites the index atomically. A re-`Put` of the
  same digest appends a second copy. Durability comes from the loose copy; the pack append is not separately
  fsynced, and there is no batch window to trade against.
- **Read.** `Get` is an index lookup plus an `io.SectionReader` over the pack — streaming, one open per
  object, never a full-pack read. The per-open win is the **batch**: `GetMany` (cas-core §4.13) serves a group
  of digests from one open per pack, measured at ≈ 6.0 ms and one open versus ≈ 13.7 ms and 4000 opens for
  the sequential loop over 4000 objects in one pack. Reads take the in-memory index under the same mutex as
  `Put`/`Delete`, so they are not lock-free the way `fs` reads are.
- **List/Stats.** `List` merges the loose digests with the index keys and `Stats` adds the indexed payload
  sizes of objects not present loose; both still walk the loose tree, so neither is O(packs), and `Stats`
  reports logical bytes rather than the packs' physical size.
- **GC: correctness yes, space no (de-claimed).** `packfs` implements no `GC`/`Prune`; `cask gc`/`prune` run
  the portable `cas.Sweep` (cas-core §4.11), which drops the loose object and the index record so the object
  becomes unreachable — the guarantee mark-and-sweep makes (consistency §4). The packed payload stays: packs
  are append-only and are never rewritten or truncated, so a packed store's disk usage never falls on its
  own, not even after a sweep, and an idempotent re-`Put` grows it. The earlier requirement that GC "rewrite
  packs dropping unreachable objects" is withdrawn rather than promised (cas-core §8 d12); compaction is an
  open follow-up (§12). Reclaiming space today means rebuilding — `cas/backend/snapshot.Export`/`Import`
  (cas-core §4.3) into a fresh base — or deleting pack files an operator has decided are disposable.
- **Delete/Clean.** `Delete` unlinks the loose object and drops the record; the pack is untouched. `Clean`
  sweeps orphan `*.tmp` scratch older than the threshold in the loose tree and the pack directory.
- **Aging.** `ModTime` reports the pack file's timestamp, not the object's first-`Put` time, so an age-gated
  sweep ages objects by their pack; `Size` reports the recorded payload length.
- **Choose it for the right reason.** Its implemented win is amortized read opens (§4.13 and the benchmarks
  in cas-core). It does not reduce inode count, does not speed up `List`/`Stats`, and does not shrink a store
  — the loose mirror plus the packs make a store *larger* on disk than the same objects on `fs` alone.

## 10. Content-defined chunking (deferred)

Rolling-hash chunking for very large blobs (dedup at chunk granularity). Design decision first, then behind the same `Backend` contract. Not started.

## 11. Performance-test requirements

Go benchmarks (with `-benchmem`) are the unit level. The scenario suite below is **aspirational**: no `cmd/perftest` harness, no `-tags=perftest` build tag, and no scenario runner exists in the repository, so T-01…T-08 do not run anywhere today. The scaled measurements that do exist are the opt-in `BenchmarkScale*` and `BenchmarkViewerObjectsScale` probes (§5 and `benchmarks/README.md` §4), which are manual and gated by `CASK_SCALE_OBJECTS`. CI runs neither benchmarks nor scenarios. The scenario suite is recorded in the deferred catalog (`extensions.md` §3) and is built only when a real need appears.

### 11.1 Scenarios

| # | Scenario | Setup |
|---|---|---|
| T-01 | Small-object storm | 100k × 1 KiB, `memory` + `fs` |
| T-02 | Large-object streaming | 10 × 1 GiB, `fs`; watch RSS during Put/Get |
| T-03 | Mixed workload | 90% small reads + 10% writes, warm store |
| T-04 | Concurrent readers | 32 goroutines reading the same 100k objects |
| T-05 | Concurrent writers | 8 goroutines writing distinct small objects |
| T-06 | List/Stats at scale | 100k and 1M objects; loose vs packed (§9) |
| T-07 | Fan-out comparison | flat vs (2,1) vs (2,2) vs (4,1) at 100k objects |
| T-08 | HTTP end-to-end | client → CAS API: streaming upload/download, 429 under load |

### 11.2 Metrics

Throughput (objects/s, MiB/s), latency p50/p95/p99, allocs/op, peak RSS, disk usage, inode count, open FDs, mutex contention (`-mutexprofile`).

### 11.3 Reference thresholds (defaults; aspirational — nothing enforces them)

| Metric | Target |
|---|---|
| Memory-backend small Put/Get | ≥100k obj/s; p99 ≤1 ms; ≤5 allocs/op |
| FS-backend small Put/Get (warm) | ≥10k obj/s; p99 ≤5 ms |
| Large-object streaming (1 GiB) | RSS stays ≤64 MiB above baseline |
| List at 1M objects (fs, (2,2)) | ≤30 s; Stats similar |
| Concurrent readers (T-04) | scales ~linearly; clean mutex profile |

### 11.4 Report and environment

Record CPU model, RAM, disk type, filesystem, Go version; run each scenario 3× and take the median. Run Go benchmarks with `-benchmem`; review allocs/op deltas by hand — there is no CI gate, and the committed `benchmarks/data/baseline.txt` is a manual reference, not a threshold (§5). There is no `cmd/perftest` harness and no `-tags=perftest` build tag: the `scenario / metric / target / result` table belongs to the aspirational suite above (deferred catalog, `extensions.md` §3), and no nightly job exists to compare runs against a previous baseline. When a measurement matters, attach it to the PR touching the core.

## 12. Checklist

- [x] `Get`/`Exists`/`List`/`Stats` are lock-free on `fs` (`mem` uses an `RWMutex`; the packfile backend takes its index mutex, §9)
- [x] hash-on-write in a single pass (CLI/HTTP: `io.MultiWriter` + `io.Copy`; core: marshal once, digest, stream)
- [x] timed benchmarks report allocations; payload-defined operations report bytes
- [x] `-race` concurrent Put/Get/Delete test green
- [x] no reflection-based dispatch, no `unsafe`, no external speed dependencies — with the two recorded exceptions above: the structural `Validator` assertion and `isNilValue`'s internal `reflect.ValueOf`, once per `Put`/`Get`
- [x] profiling workflow documented and reproducible
- [x] fan-out layout chosen per expected object count (§8.1)
- [ ] scenario tests T-01…T-08 do **not** exist — aspirational, deferred (`extensions.md` §3); only the env-gated `BenchmarkScale*` probes run today
- [x] packfs ships behind the same `Backend` contract; its measured win (batched read opens) is benchmarked, and the unclaimed pieces (pack-level GC, space reclamation, O(packs) listing, inode reduction) are documented as not implemented rather than promised (§9, cas-core §4.14)
