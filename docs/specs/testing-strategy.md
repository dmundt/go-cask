---
type: Specification
title: Testing Strategy — go-cask
description: The correctness bar for CASK — the CAS laws, requirement traceability (every feature/requirement tested at least once), corner and error cases, fuzz/race/corruption/golden tests, and a coverage gate as high as practical.
version: v18
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

## 2. Requirement traceability

Every ID'd requirement and every named contract MUST have ≥ one test. Traceability checked by test name or mapping table; a new requirement without a test fails review. Convention: tests carry the requirement ID in the name (`TestStore_P01_LockFreeReads`), so it is grep-able.

| Source | Exercised by |
|---|---|
| `examples/api` HTTP pattern (`server_test.go`) | httptest round-trip, roles, streaming, 429 |
| `performance` P-01…P-05 | the benchmark suite in `benchmarks/` covers each P-ID's subject (one-pass hashing/serialization, lock-free reads, bounded allocations, streaming); the benchmarks carry `ReportAllocs` and the P-IDs live in the spec, not in test names |
| Sentinel errors (five) | one positive `errors.Is` per error |
| Maintenance ops (`Stats`/`Verify`/`GC`/`Prune`) | one test per op, incl. dry-run + destructive |
| Object versioning | versioned `Type()` names, coexisting majors, `ErrUnknownType` |
| Object invariants (`cas.Validator`) | `Put`/`PutDedup` reject an invalid object, `Get` reports `ErrCorrupt`, `GetRaw` does not validate, nil object/payload rejected (`cas/validator_test.go`) |
| Defaults | each default asserted (fan-out (2,1), the shipped `sha256` hasher, perms) |
| Branch/CLI/versioning docs | where code exists (`cmd/cask`, `version` output) |

## 3. Corner and error cases (mandatory inventory)

- **Digest/rendering:** `Prefix(n)`: absent and `n <= 0` → `""`, a digest whose hex form is shorter than `n` returned whole, `n` beyond the hex form → the whole string, and the result is always a prefix of `String()`. **Digest/parsing:** absent (zero) `Digest`, empty string, nil vs empty bytes; malformed text (odd-length hex, uppercase, non-hex, a legacy `"sha256:hexdigest"` reference → `ErrInvalidDigest`); `Equal` same/different digests and absent-vs-absent (false); `MarshalText`/`UnmarshalText` round-trip; the client hasher's `Validate` rejecting absent and wrong-width digests (`sha256`: 32 bytes) at the store boundary.
- **Codec/object model:** empty value, all-zero struct, nested/edge values; `Unmarshal(Marshal(v))==v`; versioned names (`type@1`/`type@2`), legacy unversioned (`@1`), unknown → `ErrUnknownType`.
- **Store:** empty store (`GetRaw`/`Get`→`ErrNotFound`, `Exists` false, `Delete` no-op); `Put` empty bytes; `PutDedup` first vs repeat; `GetRaw` vs `Get`; type mismatch → `ErrUnknownType`.
- **Object invariants:** `Put`/`PutDedup` of an invalid value fails with the value's own error (wrapped `cas: put: …`) and stores nothing; the same bytes written around the check read back as `ErrCorrupt`; a type without `Validate()` is unaffected (the contract is optional); a nil object on `Put` and a payload decoding to nil on `Get` are rejected.
- **Backends (both, table-driven):** missing → `ErrNotFound`; corrupt file (fs); `.tmp` leftovers ignored; fan-out 0/negative/over-deep (`FanLevels×FanOut>64`) rejected; flat/(2,1)/(2,2)/(4,1) equivalence; mem overwrite same digest, delete-missing, `List` returns every digest (no algorithm filter).
- **Concurrency (`-race`):** concurrent `Put` same digest; `Get` during `Delete` (POSIX open-FD); parallel `List`/`Stats` during writes.
- **Maintenance:** `Verify` intact / single flip (first/middle/last) / missing; `GC` empty roots (deletes all), all-reachable (deletes none), partial, unknown in reachable set; `Prune` dry-run no delete, `minAge=0`, younger kept, all-older destructive needs explicit flag.
- **HTTP (httptest):** every route success + 400/401/403/404/429; malformed `{hash}`→400; missing/expired session→401 empty; wrong role→403 empty; rate limit→429+headers; streaming round-trip; oversized input rejected.

## 4. Test layers

1. **Unit** — table-driven per component, covering §3.
2. **Property-style** — deterministic loops over generated digests (the client's `sha256.Of`), varied digest widths, all fan layouts; std-lib only, small explicit generators (no property library).
3. **Fuzz** (`go test fuzz`): `FuzzParseDigest` (in `cas`; never panics, valid round-trip), `FuzzPathRoundTrip` (in `cas/backend/fs`; arbitrary digest + layout), `FuzzCodecRoundTrip` (JSON `Unmarshal(Marshal(x))==x`), `FuzzVerify` (in `cas/backend/fs`; takes a digest and the injected hasher — corrupt bytes fail). Commit corpora; seconds in CI, longer nightly.
4. **Concurrency/race** — `go test -race` concurrent `Put`/`Get`/`Delete`/`List` on one store (proves lock-free reads, double-checked locking).
5. **Corruption** — flip bytes on disk → `Verify` fails; `Backend.Get` returns corrupted bytes (store MUST NOT silently fix).
6. **Golden vectors** — the shipped `sha256` hasher's digest bytes are pinned against `crypto/sha256` (`cas/hash/sha256`), and the text forms are asserted exactly: `Digest.String()` is bare lowercase hex, `sha256.Format(d)` is `"sha256:hexdigest"`. The core itself owns no vectors — it names no algorithm.
7. **HTTP** — `httptest` for CAS API handlers (role matrix, 429, streaming, OpenAPI) and viewer routes (login, session, CSRF, fragments) — every route/status per §2/§3.
8. **Backends** — unit/property/fuzz default to in-memory `memory` (fast, deterministic); the CAS laws and §3 are table-driven over **both** `memory` and `fs` (all fan layouts), so fs atomic-write/fan-out/`.tmp` behavior stays covered where it differs.

## 5. Layout, coverage gate and CI

- Co-located `*_test.go`; `Example` tests as documentation.
- CI: `go test -race ./...`; fuzz smoke; `benchstat` gate (performance §5).
- **Coverage as high as practical:** `cas/` core and `gitlike/` ≥ **90%** statement coverage (excluding generated); every exported identifier exercised; any untested branch needs a comment why. HTTP: every route via `httptest`. Viewer: every named template rendered in ≥ one test.
- CI runs `go test -coverprofile` per gated package (the list in `.github/workflows/ci.yml`, which includes the shipped `cas/hash/sha256`) and fails below the bar; report attached to core PRs. Fuzz seeds are in-code `f.Add` calls (there is no committed corpus directory) and the four targets run in CI.

## 6. Checklist

- [x] all CAS laws in §1 covered
- [x] every requirement ID (P-01…P-05, sentinel errors, ops, defaults, example-API) tested; the P-IDs are covered by the `benchmarks/` suite (they are spec IDs, not test names — `Test<Component>_P0x_…` naming applies where a test *is* the requirement, e.g. sentinels and ops)
- [x] §3 corner/error inventory covered per component
- [x] fuzz targets present with in-code seed corpora, run in CI
- [x] `-race` concurrent test green
- [x] corruption test proves `Verify` fails on a flipped byte
- [x] golden vectors assert exact digests
- [x] coverage ≥ 90% on `cas/` + `gitlike/`; every exported identifier exercised; untested branches commented
- [x] every HTTP route tested (success + 400/401/403/404/429)
- [x] new requirements come with their test (review-gated)
