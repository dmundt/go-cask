---
type: Specification
title: Testing Strategy — go-cask
description: The correctness bar for CASK — the CAS laws, requirement traceability (every feature/requirement tested at least once), corner and error cases, fuzz/race/corruption/golden tests, and a coverage gate as high as practical.
version: v24
---

# Testing Strategy — go-cask

CASK's value is its invariants (same bytes ⇒ same digest ⇒ stored once, immutable, verifiable); tests prove them and **every requirement**, not just happy paths. Coverage is pushed as high as practical; a requirement without a test is a suite bug. Related: cas-core, performance, library-design, consistency, defaults, examples.

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
- Fuzz/race/golden/benchmark runs are **supplements, never the only guard** — a fuzz seed or corpus entry without an explicit test for the behavior is a gap (e.g. JSON codec's invalid-UTF-8 lossiness is pinned by an explicit test).
- Tests MUST NOT depend on execution order or shared mutable state: each builds its own fixture (`t.TempDir`); the core has no registry or other mutable global to share.
- Assertions are direct (`errors.Is`, exact values/digests); a test passing only by printing or by another test's side effect is a defect.
- When fuzzing surfaces a real constraint/skipped branch, pin it with an explicit test in the same change.
- Example packages also contribute fuzz guards for helper invariants (`examples/api/demo/fuzz_test.go`, `examples/artifacts/fuzz_test.go`, `examples/files/fuzz_test.go`), so the documentation examples stay validated even when their logic is intentionally minimal and copyable.
- The CAS layer keeps package-local fuzz coverage where the logic is small but operationally meaningful: `cas/bloom/fuzz_test.go`, `cas/cache/fuzz_test.go`, `cas/pack/fuzz_test.go`, and `cas/hash/fuzz_test.go` cover the direct contracts that are too small for a separate integration harness but too important to leave unguarded. Mock-backed contract tests (`cas/backend/mock_backend_test.go`) are also required when a package needs a deterministic in-memory backend shim without depending on the filesystem or a concrete deployment backend.

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

- **Digest/rendering:** `Prefix(n)`: absent and `n <= 0` → `""`, a digest whose hex form is shorter than `n` returned whole, `n` beyond the hex form → the whole string, and the result is always a prefix of `String()`. **Digest/parsing:** absent (zero) `Digest`, empty string, nil vs empty bytes; malformed text (odd-length hex, uppercase, non-hex, a legacy `"sha256:hexdigest"` reference → `ErrInvalidDigest`); `Equal` same/different digests and absent-vs-absent (false); `MarshalText`/`UnmarshalText` round-trip; the client hasher's `Validate` rejecting absent and wrong-width digests (`sha256`: 32 bytes) at the store boundary.
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
3. **Fuzz** (`go test fuzz`): `FuzzParseDigest` (in `cas`; never panics, valid round-trip), `FuzzPathRoundTrip` (in `cas/backend/fs`; arbitrary digest + layout), `FuzzCodecRoundTrip` (JSON `Decode(Encode(x))==x`), `FuzzVerify` (in `cas/backend/fs`; takes a digest and the injected hasher — corrupt bytes fail), plus separate package-local fuzz files for `cas/backend/packfs`, `cas/bloom`, `cas/cache`, `cas/pack`, `cas/hash`, and the example helpers (`examples/api/demo`, `examples/artifacts`, `examples/files`). Committed corpora live under `testdata/fuzz` for `cas`, `cas/backend/fs`, `cas/codec/json` and `cas/refs`; the other targets run from their in-code seeds. A target's corpus is reviewed whenever the target changes and is never treated as disposable output. Mock-backed contract tests in `cas/backend/mock_backend_test.go` are the explicit guardrail when a small package needs a deterministic in-memory implementation to exercise the same semantics without a real filesystem backend. Commit corpora; seconds in CI, longer runs on demand.
4. **Concurrency/race** — `go test -race` concurrent `Put`/`Get`/`Delete`/`List` on one store (proves lock-free reads, double-checked locking).
5. **Corruption** — flip bytes on disk → `Verify` fails; `Backend.Get` returns corrupted bytes (store MUST NOT silently fix).
6. **Golden vectors** — the shipped `sha256` hasher's digest bytes are pinned against `crypto/sha256` (`cas/hash/sha256`), and the text forms are asserted exactly: `Digest.String()` is bare lowercase hex, `sha256.Format(d)` is `"sha256:hexdigest"`. The core itself owns no vectors — it names no algorithm.
7. **HTTP** — `httptest` for CAS API handlers (role matrix, 429, streaming, OpenAPI) and viewer routes (login, session, CSRF, fragments) — every route/status per §2/§3.
8. **Backends** — unit/property/fuzz default to in-memory `memory` (fast, deterministic); the CAS laws and §3 are table-driven over **both** `memory` and `fs` (all fan layouts), so fs atomic-write/fan-out/`.tmp` behavior stays covered where it differs.

## 5. Layout, coverage gate and CI

- Co-located `*_test.go`; `Example` tests as documentation.
- CI: `go test -race ./...`; fuzz smoke. Benchmarks are **not** a CI gate and no `benchstat` gate exists (performance §5): the suite is manual and on demand, with `benchmarks/data/baseline.txt` as a committed, machine-specific reference dump.
- **Tiered coverage gates (the rule, not a list):** every package under `cas/**` carries a numeric threshold, and the two tiers mean:
  - **90** — foundational storage and reference packages, plus anything on the default write or verification path: the core (`cas`), the object backends (`cas/backend`, `cas/backend/fs`, `cas/backend/mem`, `cas/backend/snapshot`), the reference packages that carry a store's durability or its object model (`cas/repo`, `cas/refs`, `cas/pack`), the documented default compression codec (`cas/codec/flate`), the only `golang.org/x/sys` consumer (`cas/bloom/persistent`), and the backend-selection seam every CLI subcommand talks to (`internal/store`).
  - **80** — the remaining supporting packages that are part of the shipped surface: the codec wrappers, the hash clients, the verification helpers, the advisory index layer, the shared cache validation layer and the three caches, the shipped packfile backend (`cas/backend/packfs`), the viewer and its index, the `gitlike` reference library, and the `cmd/cask` command.
- **`scripts/verify.sh` is the single authority for the numbers.** It holds one table, `coverage_targets`, with one entry per gated package in the form `<threshold>|<package>|<tier>`; the tier name is documentation, the threshold is what the gate enforces. Deliberately ungated packages go in the parallel `coverage_exempt` register as `<package>|<reason>`; it is empty today, because no package under `cas/` is ungated. This section states the rule and the entry format so the two documents cannot disagree; it deliberately does not restate the list, so a threshold change touches one file.
- **The gate is total over `cas/**` and drift-proof.** A `coverage tier check` step in `scripts/verify.sh` compares `go list ./cas/...` against the packages named in `coverage_targets` and `coverage_exempt` and exits non-zero, printing the offending packages, when any `cas/` package appears in neither. A new package therefore fails verification instead of silently falling outside the bar, and a package can only be ungated as a written decision. Gated packages are built and measured with `go test -race -cover` in the same step, one coverage line each.
- **An exemption is a decision, not a measurement.** The gate does not measure ungated packages at all: a package is ungated only because an author put it in `coverage_exempt` with a reason. The PR author adding a package classifies it — this tier, that tier, or an exemption with a reason — in the same PR that adds it, so the decision is reviewable in the diff; an exemption is revisited when the package gains its first consumer or its coverage reaches the 80 tier.
- **Baseline first, then raise.** The tier is set from coverage measured under the real gate; an existing threshold is never lowered, and a package below its tier is either raised with real tests in the same change or recorded in the lower tier with a written reason. Coverage is never bought with tests that execute lines without asserting behaviour: a package whose remaining uncovered statements are defensive branches no test can reach deterministically stays in the lower tier and says so.
- Every exported identifier must still be exercised and any untested branch needs a comment why — error branches no filesystem state or injected seam can produce are listed with their reason in the package's own tests. HTTP: every route via `httptest`. Viewer: every named template rendered in ≥ one test.
- **Platform-split packages are measured on Linux only, deliberately.** The coverage step runs in the single Linux `verify` job, so `cas/bloom/persistent` — the one package with per-OS files (`persistent_unix.go` / `persistent_windows.go`) — reports its Linux numbers against the 90 tier; the `platform-matrix` jobs run `go test ./...` and prove the Windows build and behavior without asserting a second per-OS percentage. Revisit if a second such package appears or if Windows-only breakage ever escapes the matrix.
- Non-`cas` targets (`gitlike`, `internal/index`, `internal/store`, `internal/web`, `cmd/cask`) sit in the same `coverage_targets` table and carry the same two tiers; the `coverage tier check` covers `cas/**`, because that is the tree this section governs.
- CI runs `go test -race -cover` per gated package (the package and threshold list lives in `scripts/verify.sh`) and fails below that package's tier; report attached to core PRs. The CI smoke pass runs the four targets named in `scripts/verify.sh` (`FuzzParseDigest`, `FuzzPathRoundTrip`, `FuzzVerify`, `FuzzCodecRoundTrip`); the remaining package-local targets run on demand.
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
