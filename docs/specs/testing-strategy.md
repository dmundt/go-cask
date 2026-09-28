---
type: Specification
title: Testing Strategy — go-cask
description: The correctness bar for CASK — the CAS laws, requirement traceability (every feature/requirement tested at least once), corner and error cases, fuzz/race/corruption/golden tests, and a coverage gate as high as practical.
version: v34
---

# Testing Strategy — go-cask

Tests prove CASK's invariants (same bytes ⇒ same digest ⇒ stored once, immutable, verifiable) and **every requirement**; a requirement without a test is a suite bug. Related: cas-core, performance, library-design, consistency, defaults, examples.

## 1. The CAS laws (every suite must cover)

| Law | Test |
|---|---|
| Determinism | same bytes → same `Digest` every time (`Equal`, equal `Digest.String()`) |
| Dedup | `Put` twice → one object; `PutDedup` reports `deduplicated: true` |
| Round-trip | byte layer: `Put`→`Backend.Get`→identical bytes; typed: `Put`→`Get`→equal via codec |
| Immutability | stored bytes never change after `Put` |
| Integrity | `Verify` passes intact, fails after ANY byte flip |
| Layout equivalence | same content addressable under every `FanOut`/`FanLevels` combo |
| Path round-trip | `pathToDigest(digestPath(d))` equals `d` for every layout |
| Errors | missing object on any read → `ErrNotFound`; `ParseDigest` garbage → `ErrInvalidDigest` |
| Invariants | type declaring `Validate()` cannot be written invalid (`Put` rejects), stored violator → `ErrCorrupt` on `Get`; nil object rejected on `Put`, payload decoding to nil → `ErrCorrupt` |

### 1.1 Every test is explicit (normative)

- Every behavior/edge case MUST have a **named, deterministic test** stating the case (table row or `t.Run`; never anonymous branches).
- Fuzz/race/golden/benchmark runs are **supplements, never the only guard**: a seed or corpus entry with no explicit test is a gap.
- Tests MUST NOT depend on execution order or shared mutable state: each builds its own fixture (`t.TempDir`); core shares no registry or mutable global.
- Assertions direct (`errors.Is`, exact values/digests); passing only via printing or another test's side effect is a defect.
- A fuzz-surfaced constraint or skipped branch is pinned with an explicit test in the same change.
- Mock-backed contract tests (`cas/backend/mock_backend_test.go`) required where a package needs a deterministic in-memory backend shim independent of the filesystem or a concrete backend.
- Package-local fuzz guards for small but meaningful contracts, and for example helper invariants: `cas/bloom/fuzz_test.go`, `cas/cache/fuzz_test.go`, `cas/pack/fuzz_test.go`, `cas/hash/fuzz_test.go`, `examples/api/demo/fuzz_test.go`, `examples/artifacts/fuzz_test.go`, `examples/files/fuzz_test.go`.
- **A red test is fixed in the code, not in the assertion.** An author MUST NOT weaken, delete, skip or re-scope a test to reach green; a changed requirement is recorded in the owning spec in that same change, never a quieter assertion. Moving a failing package into §5's exemption register, or lowering its tier, is the same defect by another route.

## 2. Requirement traceability

Every ID'd requirement and named contract MUST have ≥ one test (test name or mapping table); a new requirement without a test fails review. Requirement ID in the test name (`TestStore_P01_LockFreeReads`), grep-able.

| Source | Exercised by |
|---|---|
| `examples/api` HTTP pattern (`server_test.go`) | httptest round-trip, roles, streaming, 429 |
| `performance` P-01…P-05 | `benchmarks/` covers each P-ID's subject (one-pass hashing/serialization, lock-free reads, bounded allocations, streaming); benchmarks carry `ReportAllocs`; P-IDs are spec IDs, not test names |
| Sentinel errors (every one `cas` declares — the list in `library-design.md` §2) | one positive `errors.Is` per error |
| Maintenance ops (`Stats`/`Verify`/`GC`/`Prune`) | one test per op, incl. dry-run + destructive |
| Object versioning | versioned `Type()` names, coexisting majors, `ErrUnknownType` |
| Object invariants (`cas.Validator`) | `Put`/`PutDedup` reject an invalid object, `Get` reports `ErrCorrupt`, `GetRaw` does not validate, nil object/payload rejected (`cas/validator_test.go`) |
| Viewer references | host-provided inbound/outbound edges render in the table, inspector count, references tab |
| Defaults | each default asserted (fan-out (2,1), the shipped `sha256` hasher, perms) |
| Branch/CLI/versioning docs | where code exists (`cmd/cask`, `version` output) |

## 3. Corner and error cases (mandatory inventory)

| Area | Cases |
|---|---|
| Digest/rendering | `Prefix(n)`: absent or `n <= 0` → `""`; shorter than `n` → whole hex form; `n` past hex form → whole string; always prefix of `String()` |
| Digest/parsing | absent (zero) `Digest`, empty string, nil vs empty bytes; malformed text (odd-length hex, uppercase, non-hex, legacy `"sha256:hexdigest"`) → `ErrInvalidDigest`; `Equal` same/different digests, absent-vs-absent false; `MarshalText`/`UnmarshalText` round-trip; client hasher `Validate` rejects absent, wrong-width digests (`sha256`: 32 bytes) at the store boundary |
| Codec/object model | empty value, all-zero struct, nested/edge values; `Decode(Encode(v))==v`; versioned names (`type@1`/`type@2`), legacy unversioned (`@1`), unknown → `ErrUnknownType` |
| Store | empty store (`GetRaw`/`Get`→`ErrNotFound`, `Exists` false, `Delete` no-op); `Put` empty bytes; `PutDedup` first vs repeat; `GetRaw` vs `Get`; type mismatch → `ErrUnknownType` |
| Object invariants | `Put`/`PutDedup` of invalid value fails with value's own error (wrapped `cas: put: …`), stores nothing; bytes written around the check read back as `ErrCorrupt`; type without `Validate()` unaffected (contract optional); nil object on `Put`, payload decoding to nil on `Get` rejected |
| Backends (both, table-driven) | missing → `ErrNotFound`; corrupt file (fs); `.tmp` leftovers ignored; fan-out 0/negative/over-deep (`FanLevels×FanOut>64`) rejected; flat/(2,1)/(2,2)/(4,1) equivalence; mem overwrite same digest, delete-missing, `List` returns every digest (no algorithm filter) |
| Concurrency (`-race`) | concurrent `Put` same digest; `Get` during `Delete` (POSIX open-FD); parallel `List`/`Stats` during writes |
| Maintenance | `Verify` intact / single flip (first/middle/last) / missing; `GC` empty roots (deletes all), all-reachable (deletes none), partial, unknown in reachable set; `Prune` dry-run no delete, `minAge=0`, younger kept, all-older destructive needs explicit flag |
| HTTP (httptest) | every route success + 400/401/403/404/429; malformed `{hash}`→400; missing/expired session→401 empty; wrong role→403 empty; rate limit→429+headers; streaming round-trip; oversized input rejected |

## 4. Test layers

| # | Layer | Guards |
|---|---|---|
| 1 | Unit | table-driven per component, covering §3 |
| 2 | Property-style | deterministic loops over generated digests (client's `sha256.Of`), varied digest widths, all fan layouts; std-lib only, small explicit generators (no property library) |
| 3 | Fuzz (`go test fuzz`) | `FuzzParseDigest` (in `cas`: never panics, valid round-trip), `FuzzPathRoundTrip` (in `cas/backend/fs`: arbitrary digest + layout), `FuzzCodecRoundTrip` (JSON `Decode(Encode(x))==x`), `FuzzVerify` (in `cas/backend/fs`: digest + injected hasher, corrupt bytes fail); package-local files for `cas/backend/packfs`, `cas/bloom`, `cas/cache`, `cas/pack`, `cas/hash` and example helpers `examples/api/demo`, `examples/artifacts`, `examples/files`; committed corpora under `testdata/fuzz` for `cas`, `cas/backend/fs`, `cas/codec/json`, `cas/refs`, other targets from in-code seeds, each corpus reviewed when its target changes; seconds in CI, longer on demand |
| 4 | Concurrency/race | `go test -race` concurrent `Put`/`Get`/`Delete`/`List` on one store, proving lock-free reads and double-checked locking |
| 5 | Corruption | flip bytes on disk → `Verify` fails; `Backend.Get` returns corrupted bytes (store MUST NOT silently fix) |
| 6 | Golden vectors | shipped `sha256` hasher's digest bytes pinned against `crypto/sha256` (`cas/hash/sha256`); text forms exact: `Digest.String()` bare lowercase hex, `sha256.Format(d)` = `"sha256:hexdigest"`; core owns no vectors, it names no algorithm |
| 7 | HTTP | `httptest` for CAS API handlers (role matrix, 429, streaming, OpenAPI) and viewer routes (login, session, CSRF, fragments) — every route/status per §2/§3 |
| 8 | Backends | unit/property/fuzz default to in-memory `memory` (fast, deterministic); CAS laws and §3 table-driven over **both** `memory` and `fs` (all fan layouts), keeping fs atomic-write/fan-out/`.tmp` behavior covered where it differs |

## 5. Layout, coverage gate and CI

| Item | Rule |
|---|---|
| Layout | co-located `*_test.go`; `Example` tests as documentation |
| CI | `go test -race ./...`, fuzz smoke; benchmarks, `benchstat` not gates (performance §5); `benchmarks/data/baseline.txt` committed machine-specific dump |
| Tiers | every `cas/**` package, numeric threshold; two tiers |
| 90 | core `cas`; backends `cas/backend`, `cas/backend/fs`, `cas/backend/mem`, `cas/backend/snapshot`; refs `cas/repo`, `cas/refs`, `cas/pack`; default codec `cas/codec/flate`; `cas/bloom/persistent` (only `golang.org/x/sys` consumer); seam `internal/store` |
| 80 | codec wrappers, hash clients, verification helpers, advisory index layer, cache validation layer + three caches, `cas/backend/packfs`, viewer + index, `gitlike`, `cmd/cask` |
| Policy owner | `internal/build/coverage`: format, shape check, pass/fail. Table (threshold, package, tier name per gated package; tier documents, threshold enforces): `internal/build/policy`. Ungated packages: parallel exemption register + reason, empty today. List omitted. `go run ./cmd/buildtool coverage-tier` reports it |
| Totality | `internal/build/coverage` compares `go list ./cas/...` against table + register; a `cas/` package in neither → non-zero + offenders. Every gated package: **one** `go test -race -cover` pass, per-package numbers from the profile it writes, not a log line — one run reports a failing suite and every sub-tier package |
| Gate entry | `./scripts/verify.sh`: permanent gate name, one entry point for local runs and CI. `exec buildtool.sh verify` shim resolves the toolchain. Step list `go run ./cmd/buildtool verify`; no step is a rule of its own — scope, concurrency and escape hatches in `internal/build/verify`, engine checks `internal/build/*`, tables in `internal/build/policy`. Gate-local tables, pattern lists and `case` matrices are tested nowhere; `go test` in `internal/build/*` answers in seconds (scripts/AGENT.md, "`verify.sh` stays forever") |
| Exemptions | decision, not measurement: ungated packages never measured, only registered with a reason; the adding PR classifies the package in the same PR; revisited at first consumer or 80-tier coverage |
| Baseline | tier from coverage measured under the real gate; thresholds never lowered; a sub-tier package is raised with real tests in the same change or recorded in the lower tier with a reason; coverage never bought with line-executing, non-asserting tests — unreachable defensive branches stay in the lower tier and are documented as unreachable |
| Identifiers | every exported identifier exercised; untested branches need a comment why — error branches no filesystem state or injected seam can produce are listed with their reason in the package's own tests; viewer: every named template rendered in ≥ one test |
| Platform split | Linux-only measurement; matrix targets compiled, not executed. Coverage in the single Linux `verify` job: `cas/bloom/persistent` (per-OS files `persistent_unix.go` / `persistent_windows.go`) reports Linux numbers against the 90 tier. `platform-matrix` cross-compiles and vets `windows/amd64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64` on one Linux runner, never `go test ./...` natively (`.github/AGENT.md`, "Workflow policy"): no non-Linux binary executed. Windows compiles and passes `go vet`, never runs — no CI covers `persistent_windows.go` or `syncParentDir`'s Windows early return; same on macOS. Revisit on a second such package |
| Non-`cas` | `gitlike`, `internal/index`, `internal/store`, `internal/web`, `cmd/cask`: same table, same two tiers; tier check covers `cas/**` |
| Smoke | CI smoke: four targets named by `internal/build/policy`'s gate table — `FuzzParseDigest`, `FuzzPathRoundTrip`, `FuzzVerify`, `FuzzCodecRoundTrip`; rest on demand; report on core PRs |
| Ownership | `cas/*` ownership: `docs/index.md`; no index row, no gate entry = visibly unowned |

## 6. Checklist

- [x] all CAS laws in §1 covered
- [x] every requirement ID (P-01…P-05, sentinel errors, ops, defaults, example-API) tested; P-IDs covered by the `benchmarks/` suite (spec IDs, not test names — `Test<Component>_P0x_…` where a test *is* the requirement)
- [x] §3 corner/error inventory covered per component
- [x] fuzz targets present; the four named smoke targets run in CI, with committed corpora for `cas`, `cas/backend/fs`, `cas/codec/json` and `cas/refs`
- [x] `-race` concurrent test green
- [x] corruption test proves `Verify` fails on a flipped byte
- [x] golden vectors assert exact digests
- [x] tiered coverage gates enforced over every package under `cas/**` (total, any exemption written down and justified), failing when a package has no tier; every exported identifier exercised; untested branches commented
- [x] every HTTP route tested (success + 400/401/403/404/429)
- [x] new requirements come with their test (review-gated)
