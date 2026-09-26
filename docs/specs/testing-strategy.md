---
type: Specification
title: Testing Strategy — go-cask
description: The correctness bar for CASK — the CAS laws, requirement traceability (every feature/requirement tested at least once), corner and error cases, fuzz/race/corruption/golden tests, and a coverage gate as high as practical.
version: v28
---

# Testing Strategy — go-cask

CASK's value is its invariants (same bytes ⇒ same digest ⇒ stored once,
immutable, verifiable); tests prove them and **every requirement**, not just
happy paths. Coverage is pushed as high as practical; a requirement without a
test is a suite bug. Related: cas-core, performance, library-design,
consistency, defaults, examples.

## 1. The CAS laws (every suite must cover)

| Law | Test |
|---|---|
| Determinism | same bytes → same `Digest` every time (`Equal`, and equal `Digest.String()`) |
| Dedup | `Put` twice → one object; `PutDedup` reports `deduplicated: true` |
| Round-trip | byte layer: `Put`→`Backend.Get`→identical bytes; typed: `Put`→`Get`→equal via codec |
| Immutability | stored bytes never change after `Put` |
| Integrity | `Verify` passes intact, fails after ANY byte flip |
| Layout equivalence | same content addressable under every `FanOut`/`FanLevels` combo |
| Path round-trip | `pathToDigest(digestPath(d))` equals `d` for every layout |
| Errors | missing object on any read → `ErrNotFound`; `ParseDigest` garbage → `ErrInvalidDigest` |
| Invariants | a type declaring `Validate()` cannot be written invalid (`Put` rejects) and a stored object violating it is `ErrCorrupt` on `Get`; a nil object is rejected on `Put`, a payload decoding to nil is `ErrCorrupt` |

### 1.1 Every test is explicit (normative)

- Every behavior/edge case MUST have a **named, deterministic test** stating the case (a table row or `t.Run` describes what it asserts; never anonymous branches).
- Fuzz/race/golden/benchmark runs are **supplements, never the only guard**: a fuzz seed or corpus entry without an explicit test for the behavior is a gap (e.g. JSON codec invalid-UTF-8 lossiness is pinned by an explicit test).
- Tests MUST NOT depend on execution order or shared mutable state: each builds its own fixture (`t.TempDir`); the core shares no registry or other mutable global.
- Assertions are direct (`errors.Is`, exact values/digests); a test passing only by printing or by another test's side effect is a defect.
- When fuzzing surfaces a real constraint/skipped branch, pin it with an explicit test in the same change.
- Example packages also contribute fuzz guards for helper invariants (`examples/api/demo/fuzz_test.go`, `examples/artifacts/fuzz_test.go`, `examples/files/fuzz_test.go`), keeping the documentation examples validated even when their logic is intentionally minimal and copyable.
- The CAS layer keeps package-local fuzz coverage where the logic is small but operationally meaningful: `cas/bloom/fuzz_test.go`, `cas/cache/fuzz_test.go`, `cas/pack/fuzz_test.go`, `cas/hash/fuzz_test.go` cover direct contracts too small for a separate integration harness yet too important to leave unguarded. Mock-backed contract tests (`cas/backend/mock_backend_test.go`) are also required when a package needs a deterministic in-memory backend shim independent of the filesystem or a concrete deployment backend.
## 2. Requirement traceability

Every ID'd requirement and every named contract MUST have ≥ one test. Traceability checked by test name or mapping table; a new requirement without a test fails review. Convention: tests carry the requirement ID in the name (`TestStore_P01_LockFreeReads`), so it is grep-able.

| Source | Exercised by |
|---|---|
| `examples/api` HTTP pattern (`server_test.go`) | httptest round-trip, roles, streaming, 429 |
| `performance` P-01…P-05 | the benchmark suite in `benchmarks/` covers each P-ID's subject (one-pass hashing/serialization, lock-free reads, bounded allocations, streaming); the benchmarks carry `ReportAllocs` and the P-IDs live in the spec, not in test names |
| Sentinel errors (every one `cas` declares — the list in `library-design.md` §2) | one positive `errors.Is` per error |
| Maintenance ops (`Stats`/`Verify`/`GC`/`Prune`) | one test per op, incl. dry-run + destructive |
| Object versioning | versioned `Type()` names, coexisting majors, `ErrUnknownType` |
| Object invariants (`cas.Validator`) | `Put`/`PutDedup` reject an invalid object, `Get` reports `ErrCorrupt`, `GetRaw` does not validate, nil object/payload rejected (`cas/validator_test.go`) |
| Viewer references | host-provided inbound/outbound edges render in the table, inspector count, and references tab |
| Defaults | each default asserted (fan-out (2,1), the shipped `sha256` hasher, perms) |
| Branch/CLI/versioning docs | where code exists (`cmd/cask`, `version` output) |

## 3. Corner and error cases (mandatory inventory)

- **Digest/rendering:** `Prefix(n)`: absent and `n <= 0` → `""`; a hex form shorter than `n` returned whole; `n` beyond the hex form → the whole string; the result is always a prefix of `String()`. **Digest/parsing:** absent (zero) `Digest`, empty string, nil vs empty bytes; malformed text (odd-length hex, uppercase, non-hex, a legacy `"sha256:hexdigest"` reference → `ErrInvalidDigest`); `Equal` same/different digests and absent-vs-absent (false); `MarshalText`/`UnmarshalText` round-trip; the client hasher's `Validate` rejecting absent and wrong-width digests (`sha256`: 32 bytes) at the store boundary.
- **Codec/object model:** empty value, all-zero struct, nested/edge values; `Decode(Encode(v))==v`; versioned names (`type@1`/`type@2`), legacy unversioned (`@1`), unknown → `ErrUnknownType`.
- **Store:** empty store (`GetRaw`/`Get`→`ErrNotFound`, `Exists` false, `Delete` no-op); `Put` empty bytes; `PutDedup` first vs repeat; `GetRaw` vs `Get`; type mismatch → `ErrUnknownType`.
- **Object invariants:** `Put`/`PutDedup` of an invalid value fails with the value's own error (wrapped `cas: put: …`) and stores nothing; the same bytes written around the check read back as `ErrCorrupt`; a type without `Validate()` is unaffected (the contract is optional); a nil object on `Put` and a payload decoding to nil on `Get` are rejected.
- **Backends (both, table-driven):** missing → `ErrNotFound`; corrupt file (fs); `.tmp` leftovers ignored; fan-out 0/negative/over-deep (`FanLevels×FanOut>64`) rejected; flat/(2,1)/(2,2)/(4,1) equivalence; mem overwrite same digest, delete-missing, `List` returns every digest (no algorithm filter).
- **Concurrency (`-race`):** concurrent `Put` same digest; `Get` during `Delete` (POSIX open-FD); parallel `List`/`Stats` during writes.
- **Maintenance:** `Verify` intact / single flip (first/middle/last) / missing; `GC` empty roots (deletes all), all-reachable (deletes none), partial, unknown in reachable set; `Prune` dry-run no delete, `minAge=0`, younger kept, all-older destructive needs explicit flag.
- **HTTP (httptest):** every route success + 400/401/403/404/429; malformed `{hash}`→400; missing/expired session→401 empty; wrong role→403 empty; rate limit→429+headers; streaming round-trip; oversized input rejected.
## 4. Test layers

1. **Unit** — table-driven per component, covering §3.
2. **Property-style** — deterministic loops over generated digests (the client's `sha256.Of`), varied digest widths, all fan layouts; std-lib only, small explicit generators (no property library).
3. **Fuzz** (`go test fuzz`): `FuzzParseDigest` (in `cas`; never panics, valid round-trip), `FuzzPathRoundTrip` (in `cas/backend/fs`; arbitrary digest + layout), `FuzzCodecRoundTrip` (JSON `Decode(Encode(x))==x`), `FuzzVerify` (in `cas/backend/fs`; digest + injected hasher — corrupt bytes fail), plus package-local fuzz files for `cas/backend/packfs`, `cas/bloom`, `cas/cache`, `cas/pack`, `cas/hash`, and the example helpers (`examples/api/demo`, `examples/artifacts`, `examples/files`). Committed corpora under `testdata/fuzz` for `cas`, `cas/backend/fs`, `cas/codec/json`, `cas/refs`; other targets run from in-code seeds. A target's corpus is reviewed whenever the target changes, never treated as disposable output. Mock-backed contract tests in `cas/backend/mock_backend_test.go` are the explicit guardrail for a small package needing a deterministic in-memory implementation exercising the same semantics without a real filesystem backend. Commit corpora; seconds in CI, longer runs on demand.
4. **Concurrency/race** — `go test -race` concurrent `Put`/`Get`/`Delete`/`List` on one store (proves lock-free reads, double-checked locking).
5. **Corruption** — flip bytes on disk → `Verify` fails; `Backend.Get` returns corrupted bytes (store MUST NOT silently fix).
6. **Golden vectors** — the shipped `sha256` hasher's digest bytes pinned against `crypto/sha256` (`cas/hash/sha256`), text forms asserted exactly: `Digest.String()` bare lowercase hex, `sha256.Format(d)` = `"sha256:hexdigest"`. The core owns no vectors — it names no algorithm.
7. **HTTP** — `httptest` for CAS API handlers (role matrix, 429, streaming, OpenAPI) and viewer routes (login, session, CSRF, fragments) — every route/status per §2/§3.
8. **Backends** — unit/property/fuzz default to in-memory `memory` (fast, deterministic); the CAS laws and §3 table-driven over **both** `memory` and `fs` (all fan layouts), so fs atomic-write/fan-out/`.tmp` behavior stays covered where it differs.
## 5. Layout, coverage gate and CI

- Co-located `*_test.go`; `Example` tests as documentation.
- CI: `go test -race ./...`; fuzz smoke. Benchmarks are **not** a CI gate, and no `benchstat` gate exists (performance §5): the suite is manual and on demand, `benchmarks/data/baseline.txt` being a committed, machine-specific reference dump.
- **Tiered coverage gates (the rule, not a list):** every package under `cas/**` carries a numeric threshold; the two tiers mean:
  - **90** — foundational storage and reference packages, plus anything on the default write or verification path: the core (`cas`), the object backends (`cas/backend`, `cas/backend/fs`, `cas/backend/mem`, `cas/backend/snapshot`), the reference packages carrying a store's durability or object model (`cas/repo`, `cas/refs`, `cas/pack`), the documented default compression codec (`cas/codec/flate`), the only `golang.org/x/sys` consumer (`cas/bloom/persistent`), and the backend-selection seam every CLI subcommand talks to (`internal/store`).
  - **80** — the remaining supporting packages on the shipped surface: codec wrappers, hash clients, verification helpers, the advisory index layer, the shared cache validation layer and the three caches, the shipped packfile backend (`cas/backend/packfs`), the viewer and its index, the `gitlike` reference library, and the `cmd/cask` command.
- **The coverage policy has one owner.** `internal/build/core/coverage` owns the format, the shape check and the pass/fail decision; go-cask's own table — one entry per gated package as a threshold, the package, and a tier name, where the tier name documents and the threshold enforces — is `internal/build/policy`'s. Deliberately ungated packages go in the parallel exemption register with a written reason; empty today, since no `cas/` package is ungated. This section states the rule and the entry format so the two documents cannot disagree, and omits the list so a threshold change touches one file. The policy is data with its own tests, and `go run ./cmd/buildtool coverage-tier` reports it.
- **The gate is total over `cas/**` and drift-proof.** The gate asks `internal/build/core/coverage` to compare `go list ./cas/...` against the table and the exemption register, exiting non-zero with the offending packages when a `cas/` package appears in neither: a new package fails verification rather than silently falling outside the bar, and only a written decision leaves one ungated. The same step builds and measures gated packages with `go test -race -cover`, one coverage line each.
- **`./scripts/verify.sh` is the gate's permanent name, and the steps are Go.** It is the one entry point every local run and CI share, so it stays — as `exec buildtool.sh verify`, a shim that resolves the toolchain and starts the command. The step list is `go run ./cmd/buildtool verify`, and no step in it is a rule of its own: the scope, the concurrency and the escape hatches are `internal/build/core/verify`; the engine's own checks are `internal/build/core/*`; go-cask's tables are `internal/build/policy`. A threshold table, a pattern list or a `case` matrix written into the gate would be executed everywhere and tested nowhere — the only way to learn it still held would be a full gate run, which is what a `go test` in `internal/build/*` answers in seconds (scripts/AGENT.md, "`verify.sh` stays forever").
- **An exemption is a decision, not a measurement.** Ungated packages are never measured: one is ungated only because an author put it in the exemption register with a reason. The PR author adding a package classifies it — this tier, that tier, or an exemption with a reason — in that same PR, making the decision reviewable in the diff; an exemption is revisited once the package gains its first consumer or its coverage reaches the 80 tier.
- **Baseline first, then raise.** The tier is set from coverage measured under the real gate; an existing threshold is never lowered, and a package below its tier is either raised with real tests in the same change or recorded in the lower tier with a written reason. Coverage is never bought with tests that execute lines without asserting behaviour: uncovered defensive branches no test can reach deterministically stay in the lower tier, and say so.
- Every exported identifier must still be exercised, and any untested branch needs a comment why — error branches no filesystem state or injected seam can produce are listed with their reason in the package's own tests. HTTP: every route via `httptest`. Viewer: every named template rendered in ≥ one test.
- **Platform-split packages are measured on Linux only, deliberately.** The coverage step runs in the single Linux `verify` job, so `cas/bloom/persistent` — the one package with per-OS files (`persistent_unix.go` / `persistent_windows.go`) — reports Linux numbers against the 90 tier; `platform-matrix` jobs run `go test ./...`, proving the Windows build and behavior without asserting a second per-OS percentage. Revisit if a second such package appears or Windows-only breakage escapes it.
- Non-`cas` targets (`gitlike`, `internal/index`, `internal/store`, `internal/web`, `cmd/cask`) sit in the same table under the same two tiers; the tier check covers `cas/**`, the tree this section governs.
- CI runs `go test -race -cover` per gated package (the list comes from `internal/build/core/coverage`), failing below that package's tier; report attached to core PRs. The CI smoke pass runs the four targets `internal/build/policy`'s gate table names (`FuzzParseDigest`, `FuzzPathRoundTrip`, `FuzzVerify`, `FuzzCodecRoundTrip`); the rest run on demand.
- Path ownership for every `cas/*` package lives in `docs/index.md`, so a package with no index row and no gate entry is visibly unowned in review.
## 6. Checklist

- [x] all CAS laws in §1 covered
- [x] every requirement ID (P-01…P-05, sentinel errors, ops, defaults, example-API) tested; the P-IDs are covered by the `benchmarks/` suite (they are spec IDs, not test names — `Test<Component>_P0x_…` naming applies where a test *is* the requirement, e.g. sentinels and ops)
- [x] §3 corner/error inventory covered per component
- [x] fuzz targets present; the four named smoke targets run in CI, with committed corpora for `cas`, `cas/backend/fs`, `cas/codec/json` and `cas/refs`
- [x] `-race` concurrent test green
- [x] corruption test proves `Verify` fails on a flipped byte
- [x] golden vectors assert exact digests
- [x] tiered coverage gates enforced over every package under `cas/**` (total, with any exemption written down and justified), the gate failing when a package has no tier; every exported identifier exercised; untested branches commented
- [x] every HTTP route tested (success + 400/401/403/404/429)
- [x] new requirements come with their test (review-gated)
