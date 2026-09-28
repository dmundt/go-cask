---
type: Specification
title: Performance — go-cask
description: Performance requirements and workflow for CASK — lock-free reads via atomic rename, one-pass streaming hashing, bounded allocations, scaling and object-count limits, the optional packfile backend, performance-test requirements, benchmarks and profiling.
version: v27
---

# Performance — go-cask

Every optimization MUST preserve `cas-core.md` invariants. Measure before and after — never
micro-optimize without a benchmark. No unsafe/cgo/assembly/third-party speed dependencies; std-lib
only (coding-guidelines §3). Related: cas-core, coding-guidelines, testing-strategy, operations.

## 1. Performance goals

| # | Goal | How |
|---|---|---|
| P-01 | Lock-free read path | `Get`/`Exists`/`List`/`Stats` take no lock (§2) |
| P-02 | One-pass serialization | the envelope is marshaled once, digested, then streamed to `Backend.Put`; hash-on-write surfaces (CLI, `examples/api`) stream through `io.MultiWriter`/`io.Copy`; never re-serialize or re-read source bytes |
| P-03 | Bounded allocations | hot paths flat; every benchmark calls `b.ReportAllocs()` |
| P-04 | No reflection-based dispatch | generics monomorphize and no exported value is `any`; the typed layer's one structural `Validator` assertion and the internal nil check are the recorded exceptions, paid once per `Put`/`Get` |
| P-05 | Large objects never buffered | `Backend` streams `io.Reader`; HTTP layer streams bodies |

- **P-04's two exceptions:** one structural `any(obj).(Validator)` assertion (`cas/store.go`, object-invariant contract, cas-core §4.8) and one `reflect.ValueOf` through `isNilValue` (the core's only reflection, rejecting a nil-interface or nil-pointer object before encoding or return), each paid per `Put`/`Get`. Removing either is a design change needing a benchmark.

## 2. Lock-free reads (`fs.Backend`)

- Writes are atomic: temp file → `f.Sync()` → `os.Rename` (Go's `os.Rename` also replaces an existing destination on Windows).
- `Get`/`Exists`/`List`/`Stats` MUST NOT acquire a lock — `os.Open`/`os.Stat` observe the old or new file, never a partial one.
- `Put` is idempotent (same hash ⇒ identical bytes): concurrent same-hash writers are safe; the last identical writer wins.
- On POSIX, unlink/rename keep open FDs valid: `Delete` during an in-flight read is safe.
- `Put`/`Delete` MAY use one `sync.Mutex` (never `RWMutex`).
- Document the atomicity argument in the `fs.Backend` type comment, so the lock-free design survives refactors.

## 3. One-pass hashing (`Store.Put`)

- `Store.Put`: marshal the envelope once into one buffer → digest with the injected `Hasher` → stream to `raw.Put`. Never marshaled twice.
- Hash-on-write surfaces (the CLI's `put`, `examples/api`'s upload): spool and hash in one pass through `io.MultiWriter`/`io.Copy` into `sha256.NewHasher()`.
- `Backend.Put(ctx, d, r)` MUST stream `r` without buffering; the digest `d` is the trusted address (`Verify` is the integrity check).
- Recorded sidecar checksums cost one extra read per recorded `Put` (`cas/verify/sidecar`, operations §6): one streaming pass over what `Get` returns after the write publishes the object, plus one extra file and atomic rename per `Put`.
- `cas.Hasher` exposes only a reader-based `Digest` — no incremental writer, and the sidecar may not add one to the core.
- Reads (`Get`/`Exists`/`List`/`Stats`) are untouched; `Rec.Verifier(...)`'s `VerifyAll` is one streaming read per recorded object.
- Recording is off by default; a caller that never wraps a backend pays nothing.

## 4. Allocation and streaming rules

- `Store.Put`/`Get` (small objects) and `fs.Backend.Put`/`Get` SHOULD keep allocations flat/bounded; prove with `b.ReportAllocs()`.
- Reuse buffers via `sync.Pool` for scratch in the HTTP layer and verify/hexdump paths.
- Never `io.ReadAll` a large object in a byte-layer `Backend.Get` or `Store.GetRaw` — stream or use a bounded read. `Store.GetReader` is that stream: the same guards and the same backend `Get`, with the reader handed to the caller, so a tooling path that wants a prefix, a hash or a copy to another store pays one buffer instead of the object. `Store.GetRaw` keeps its buffering contract and is exempt as the deliberate inspection accessor — it is one `readThenClose` over `GetReader` (go-cask#381); `Store.Get` MAY buffer because `Codec.Decode` needs bytes; document that.
- Size a whole-object read when the length is knowable: `backend.ReadWhole(ctx, r, declared)` pre-sizes the buffer from the caller's declared length or the reader's own `Len()`, so `mem.Put` (whose reader is the `*bytes.Reader` `Store.Put` hands over) and `snapshot.Export` (which asks `cas.Statter.Size`) allocate once instead of paying `io.ReadAll`'s doubling. A length is never a read limit and the pre-allocation is capped at `maxPrealloc`; with no hint the read grows exactly as before. `backend.ReadPayload` keeps its deliberate refusal to size anything from an untrusted declared header (go-cask#385).
- Ask `cas.PhysicalStatter.Stat` when a caller needs an object's size **and** modification time: it is one physical read where `cas.Statter`'s `Size` + `ModTime` are two, and `index.BuildSnapshot` uses it whenever its source implements it (fs, packfs), halving the per-object syscalls of a snapshot build (go-cask#373).
- Avoid `fmt` in hot paths — use `encoding/hex` directly, not `%x` loops.

## 5. Benchmark suite

Benchmarks live in `benchmarks/`. Suite: `BenchmarkStorePut` (steady-state + cold-start, 64 B–1 MiB) and the read patterns `BenchmarkStoreGetHot`/`BenchmarkStoreGetCold`/`BenchmarkStoreGetMixed`, the paired allocation measurement `BenchmarkStoreGetRawStream` (buffered `GetRaw` vs `GetReader` streamed through the reader); the raw byte path `BenchmarkBackendWriteRead` (`mem`/`fs` × steady-state/cold-start); the codec/hash matrix `BenchmarkCodecPackageRoundTrip` (`json`/`gzip`/`zlib`/`flate`/`gob`/`binary`/`cbor` × `sha256`/`sha512`/`sha512_256` × size); `BenchmarkRoundTrip`; `BenchmarkVerify`; `BenchmarkParseDigest` (valid + invalid); `BenchmarkParallelPutGet` (exercises §2); `BenchmarkScale{...}`; `BenchmarkBloom*` families for advisory pre-check layers. `benchmarks/AGENT.md` freezes the package-local measurement and maintenance rules; `benchmarks/README.md` is the run-and-read guide.

### 5.1 Optional Bloom acceleration

`cas/bloom` is optional and advisory, and MUST NOT change the storage-core correctness model.

| Rule | Detail |
| --- | --- |
| Negative result | definitive for a well-formed filter; may short-circuit a lookup |
| Positive result | a hint only — the wrapped backend/store must still verify the digest's real existence |
| Variants | standard, counting, persistent — advisory front ends only; they do not participate in `Verify`, `GC`, or `Prune` semantics |
| Bit-index derivation | independent of the CAS digest algorithm: the object hash stays the caller-owned `cas.Hasher` contract; the filter picks bit positions in its own bitmap |
| Bits outliving the process | MUST derive bit positions deterministically. A filter reopened under a different rule answers `false` for digests it recorded, and a `bloom.Guard` turns that into an authoritative "absent" |
| Per-process seeds | `bloom.DefaultIndexHash` is seeded per process and may only back an in-memory filter |
| Persistent seeds | `cas/bloom/persistent` persists a 32-byte index key in the file header (format `CASKBLM1`) and derives its default index hash from it, so its bits stay readable by the next process; the header records which kind wrote the file, so a file written under the other kind is rebuilt rather than trusted, and it carries a checksum over `kind || key`, so a corrupted key is rebuilt rather than reindexing every recorded digest into an authoritative absence (`Filter.Rebuilt()` reports the discard) (`cas/bloom/persistent`, go-cask#254, go-cask#361) |
| Caller-supplied | a caller-supplied `bloom.IndexHash` carries the same obligation |

- Every timed benchmark calls `b.ReportAllocs()`; non-timed layout/economics probes MAY omit it.
- `b.SetBytes()` only when one operation processes one known-size payload; `Exists`, `Delete`, `List`, `Stats`, digest parsing and mixed concurrent workloads MUST NOT invent a byte count.
- Store-logic benchmarks run against the in-memory `memory` backend (deterministic, no disk noise); disk behavior is covered by the `fs` cases.
- The codec/hash matrix MUST apply identical objects, sizes, backend, and Put+Get work to every combination — a comparative end-to-end benchmark, not a standalone codec or hash microbenchmark.
- **No CI gate and no scheduled run.** CI runs no `-bench` (`ci.yml` has no benchmark job) and there is no nightly workflow, so results are never a required check; allocation regressions are caught by P-03 and review. No other document may promise a "benchstat gate" or a nightly benchmark job.

| Manual command | Effect |
| --- | --- |
| `go run ./cmd/buildtool bench-baseline` | re-captures the suite (`-count=1`), archives the raw output as `benchmarks/data/archive/baseline-<UTC-stamp>.txt`, refreshes the canonical copy unless `--capture-only` is given |
| `go run ./cmd/buildtool bench-compare` | picks the baseline first, captures into `benchmarks/data/current.txt`, never writes the canonical reference itself, prints a `benchstat` diff when `benchstat` is installed (exit code 2 when it is not) |
| `go test ./benchmarks/ -bench=. -benchmem -count=5` | runs the suite on demand |

- `benchmarks/data/baseline.txt` is the only committed reference — machine-specific, refreshed by hand on a quiet machine, never on a schedule, never per PR. A comparison point, not a threshold.
- `BenchmarkScalePut/Get/Exists/List/Delete/Stats` prefill a store to N, time the op at that size, log a projection for 10^10 objects; each skips unless `CASK_SCALE_OBJECTS` is set, e.g. `CASK_SCALE_OBJECTS=1000000 go test ./benchmarks/ -run=^$ -bench=Scale -benchtime=100x -v`.
- The lock-free claim is exercised by `-race` tests and `BenchmarkParallelPutGet`.

## 6. Profiling workflow

1. Reproduce: `go test -bench=BenchmarkRoundTrip -benchmem`.
2. Profile: `go test -bench=... -cpuprofile p.out -memprofile m.out`, or `net/http/pprof` on the viewer server (expose only when explicitly enabled).
3. Attack order: allocations first (`pprof -alloc_objects`), then CPU (flamegraph), then lock contention (`-mutexprofile`) — expect none in the read path.

## 7. What NOT to optimize

- Correctness, clarity and documented contracts come first; reject a micro-optimization that obscures an invariant.
- No unsafe/cgo/assembly/third-party pools.
- Do not cache object bytes in memory as an implicit fast path (changes memory semantics) — use the documented cache layer.

## 8. Scaling and limits

### 8.1 Object count vs layout

One file per loose object; the first hard limit is usually inodes/dir-entry performance, not disk. In
one directory ~10k–100k entries are fine on ext4; choose `FanOut`/`FanLevels` so leaf dirs stay under
~10k entries for the expected deduplicated count. `List`/`Stats` walk every file (O(object count)) —
background work at 1M+, unchanged by packfs, which keeps the loose tree alongside its packs (§9).

| Layout | Directories | Practical loose ceiling (ext4, SSD) | Use |
|---|---|---|---|
| flat (0/0) | 1 | ~10k–100k | tiny stores / tests |
| Git-like (2,1) | 256 | ~1M–10M | default |
| wide (4,1) | 65,536 | ~10M–100M | many-object stores |
| deep 2/2 (2,2) | 65,536 leaf | ~10M–100M | many-object stores |
| + packfs | loose tree + `packs/` | unchanged (same loose files, plus pack copies) | read-open amortization, not density (§9) |

### 8.2 Other limits

| Limit | Detail |
| --- | --- |
| Dedup | reduces effective object count — size for the deduplicated count |
| Memory backend | RAM-bound — ~100+ B/object map overhead plus data; tests/benchmarks/ephemeral stores only |
| SHA-256 (or SHA-1) | collision risk negligible at realistic scale |
| Reads | stream one FD each; concurrent readers bounded by `ulimit` (a packfs batch amortizes to 1 FD/pack) |
| Writers | the single `Put` mutex serializes writers on one store; for write-heavy work, shard or scale behind the CAS API (which rate-limits per IP). Packfs batches no writes — it appends each `Put` immediately — and serializes reads against writes on the same mutex (§9) |
| Disk and inodes | eventual limits; dedup + GC (reachability) reclaim disk on `fs` and `mem`; packfs writes both the loose object and the pack copy, so inode count is unchanged (§9) |

## 9. Packfiles (shipped extension: `cas/backend/packfs`)

`cas/backend/packfs` ships as an opt-in backend — loose tree plus append-only pack files and a JSON
index, selected by `cask -backend packfs` (cas-core §4.14).

- **Shape**: `<base>/loose/` is a full `fs.Backend` (fan-out default). `<base>/packs/current.pack` is the active pack, appended under `O_APPEND`, rotated at `PackMaxBytes` (default 64 MiB) or `PackMaxEntries` (default 10 000) into `<base>/packs/pack-<unixnano>.pack`. `<base>/packs/index.json` maps each packed digest (hex) to `{pack, offset, size}`.
- **Record**: `[uint32 BE digest length][digest][uint64 BE payload length]` then the payload — no magic, no format version, no checksum.
- **Write**: every object is both loose and packed; no size threshold. `Put` spools to a scratch file, writes through the fs backend's atomic `Sync`+rename path, appends to the active pack, rewrites the index atomically; a re-`Put` appends a second copy. Durability comes from the loose copy — the pack append is not separately fsynced.
- **Read**: an index lookup plus an `io.SectionReader` over the pack — streaming, one open per object, never a full-pack read; the in-memory index is taken under the same mutex as `Put`/`Delete`, so reads are not lock-free the way `fs` reads are.
- **Batch win**: `GetMany` (cas-core §4.13) serves a digest group from one open per pack — ≈ 6.0 ms and one open versus ≈ 13.7 ms and 4000 opens for the sequential loop over 4000 objects in one pack.
- **List/Stats**: `List` merges loose digests with index keys; `Stats` adds the indexed payload sizes of objects not present loose. Both walk the loose tree, so neither is O(packs), and `Stats` reports logical bytes, not physical pack size.
- **GC — correctness yes, space no (de-claimed)**: `packfs` implements no `GC`/`Prune`; `cask gc`/`prune` run the portable `cas.Sweep` (cas-core §4.11), dropping the loose object and the index record, so the object becomes unreachable — mark-and-sweep's guarantee (consistency §4). Packs are append-only: disk usage never falls on its own, and a re-`Put` grows it.
- Withdrawn, not promised (cas-core §8 d12): GC "rewrite packs dropping unreachable objects", and the design sketch it replaced — a magic/version/checksum `.pack` with a `.idx` fan-out table, an 8 KiB size threshold, "objects above the threshold stay loose", pack-rewrite GC. Compaction is an open follow-up (§12).
- **Reclaiming space today**: rebuild via `cas/backend/snapshot.Export`/`Import` (cas-core §4.3) into a fresh base, or delete pack files an operator decided are disposable — a **Go-level** remedy, since no `cask` subcommand wraps `Export`/`Import` (cli.md §2).
- **Delete/Clean**: `Delete` unlinks the loose object and drops the record; the pack is untouched. `Clean` sweeps orphan `*.tmp` scratch older than the threshold in the loose tree and the pack directory.
- **Aging**: `ModTime` reports the pack file's timestamp, not the object's first-`Put` time, so an age-gated sweep ages objects by their pack; `Size` reports the recorded payload length.
- **Choose it for the right reason**: its implemented win is amortized read opens (§4.13 and the benchmarks in cas-core). It reduces no inode count, speeds up no `List`/`Stats`, and shrinks no store — the loose mirror plus the packs make a store *larger* on disk than `fs` alone.

## 10. Content-defined chunking (deferred)

Rolling-hash chunking for very large blobs (dedup at chunk granularity). Design decision first, then
behind the same `Backend` contract. Not started.

## 11. Performance-test requirements

- Go benchmarks (with `-benchmem`) are the unit level.
- The scenario suite below is **aspirational**: no `cmd/perftest` harness, no `-tags=perftest` build tag, no scenario runner exists in the repository, so T-01…T-08 run nowhere today. CI runs neither benchmarks nor scenarios. The suite is recorded in the deferred catalog (`extensions.md` §3).
- The scaled measurements that do exist are the opt-in `BenchmarkScale*` and `BenchmarkViewerObjectsScale` probes (§5, `benchmarks/README.md` §4) — manual, gated by `CASK_SCALE_OBJECTS`.

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

Throughput (objects/s, MiB/s), latency p50/p95/p99, allocs/op, peak RSS, disk usage, inode count,
open FDs, mutex contention (`-mutexprofile`).

### 11.3 Reference thresholds (defaults; aspirational — nothing enforces them)

| Metric | Target |
|---|---|
| Memory-backend small Put/Get | ≥100k obj/s; p99 ≤1 ms; ≤5 allocs/op |
| FS-backend small Put/Get (warm) | ≥10k obj/s; p99 ≤5 ms |
| Large-object streaming (1 GiB) | RSS stays ≤64 MiB above baseline |
| List at 1M objects (fs, (2,2)) | ≤30 s; Stats similar |
| Concurrent readers (T-04) | scales ~linearly; clean mutex profile |

### 11.4 Report and environment

Record CPU model, RAM, disk type, filesystem, Go version; run each scenario 3× and take the median.
Run Go benchmarks with `-benchmem` and review allocs/op deltas by hand — no CI gate exists, and
`benchmarks/data/baseline.txt` is a manual reference, not a threshold (§5). The
`scenario / metric / target / result` table belongs to the aspirational suite above (`extensions.md`
§3); no nightly job compares runs against a previous baseline. When a measurement matters, attach it
to the PR touching the core.

## 12. Checklist

- [x] `Get`/`Exists`/`List`/`Stats` are lock-free on `fs` (`mem` uses an `RWMutex`; the packfile backend takes its index mutex, §9)
- [x] hash-on-write in a single pass (CLI/HTTP: `io.MultiWriter` + `io.Copy`; core: marshal once, digest, stream)
- [x] timed benchmarks report allocations; payload-defined operations report bytes
- [x] `-race` concurrent Put/Get/Delete test green
- [x] no reflection-based dispatch, no `unsafe`, no external speed dependencies — with the two recorded exceptions above: the structural `Validator` assertion and `isNilValue`'s internal `reflect.ValueOf`, once per `Put`/`Get`
- [x] profiling workflow documented and reproducible
- [x] fan-out layout chosen per expected object count (§8.1)
- [ ] scenario tests T-01…T-08 do **not** exist — aspirational, deferred (`extensions.md` §3); only the env-gated `BenchmarkScale*` probes run today
- [x] packfs ships behind the same `Backend` contract; its measured win (batched read opens) is benchmarked, and the unclaimed pieces (pack-level GC, space reclamation, O(packs) listing, inode reduction) are documented as unimplemented, not promised (§9, cas-core §4.14)
