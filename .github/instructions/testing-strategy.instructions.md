---
title: Testing Strategy — go-cask
description: The correctness bar for CASK — the CAS laws, requirement traceability (every feature/requirement tested at least once), corner and error cases, fuzz/race/corruption/golden tests, and a coverage gate as high as practical.
version: v3
---

# Testing Strategy — go-cask

> CASK's value is its invariants: same bytes ⇒ same hash ⇒ stored once,
> immutable, verifiable. Tests exist to **prove** them — and to prove **every
> requirement**, not just the happy paths. Coverage is pushed **as high as
> practical**; a requirement without a test is a bug in the suite.
>
> Related: `cas-core.instructions.md` (the contracts under test),
> `cas-api.instructions.md` (requirements R-01…R-14 the HTTP tests trace),
> `performance.instructions.md` (P-01…P-05, benchmark gates),
> `library-design.instructions.md` (sentinel errors), `consistency.
> instructions.md` (GC/prune), `defaults.instructions.md` (defaults under
> test), `examples.instructions.md` (integration-level tests).

---

## 1. The CAS Laws (invariants every suite must cover)

| Law                   | Test                                                              |
| --------------------- | ----------------------------------------------------------------- |
| Determinism           | same bytes → same `Hash.String()` every time                      |
| Dedup                 | `Put` twice → one object; `PutDedup` reports `deduplicated: true` |
| Round-trip            | `Put` → `Get` → identical bytes; `GetTyped` → value equal via codec |
| Immutability          | stored bytes never change after `Put`                              |
| Integrity             | `Verify` passes on an intact object, fails after ANY byte flip    |
| Layout equivalence    | same content addressable under every `FanOut`/`FanLevels` combo   |
| Path round-trip       | `pathToHash(hashPath(h)) == h` for every layout and algorithm     |
| Errors                | `Get` missing → `errors.Is(err, ErrNotFound)`; `ParseHash` garbage → `ErrInvalidHash` |

---

## 2. Requirement Traceability (each feature/requirement tested at least once)

Every requirement with an ID, and every named contract, MUST have **at least
one test that exercises it**. Coverage is checked by test name or a mapping
table (below); a new requirement without a test fails review.

| Requirement source                                  | Must be exercised by                                    |
| --------------------------------------------------- | ------------------------------------------------------- |
| `cas-api` R-01…R-14 (content addressing … rate limiting) | an `httptest` case per R-ID, named `TestAPI_R0X_…` |
| `performance` P-01…P-05                             | a benchmark or test per P-ID (lock-free reads, streaming, allocs) |
| Sentinel errors (library-design §2, five errors)    | one positive `errors.Is` assertion per error            |
| Maintenance ops (cas-core §4.11): `Stats`/`Verify`/`GC`/`Prune` | one test per op, incl. dry-run and destructive paths |
| Object-model versioning (object-versioning §2–§4)   | versioned `Type()` names, coexisting majors, `ErrUnknownType` |
| Defaults (defaults §2–§7)                           | each default asserted by a test (e.g. fan-out (2,1), algo sha256, perms) |
| Branch/CLI/versioning docs                          | where code exists (`cmd/cask` subcommands, `version` output) |

Convention: tests that trace a requirement carry its ID in the name
(`TestAPI_R05_Pagination`, `TestStore_P01_LockFreeReads`), so traceability is
grep-able.

---

## 3. Corner & Error Cases (mandatory inventory)

Beyond the happy paths, every component MUST cover its edge and error cases:

**Hash & parsing**
- empty digest, zero `Hash`, digest lengths for SHA-1 (40) and SHA-256 (64)
- malformed strings: no `:`, empty algo, empty hex, odd-length hex,
  uppercase, unknown algorithm → `ErrUnknownAlgorithm` / `ErrInvalidHash`
- `Equal`: same algo+bytes, same bytes different algo, nil vs non-nil

**Codec & object model**
- empty value, all-zero struct, nested/edge values; `Decode(Encode(v)) == v`
- versioned type names (`type@1`, `type@2`), legacy unversioned (`@1`
  default), unknown type/major → `ErrUnknownType`

**Store**
- empty store: `Get`/`GetTyped` → `ErrNotFound`; `Exists` false; `Delete`
  no-op
- `Put` of empty bytes; `PutDedup` first vs repeat; `GetRaw` vs `GetTyped`
- type mismatch path (`Get` on a store for the wrong object type)

**Backends (both, table-driven)**
- missing object (ErrNotFound), corrupt file (fs), `.tmp` leftovers ignored
- fan-out bounds: 0, negative, over-deep (`FanLevels×FanOut > 64`) rejected
- flat vs (2,1) vs (2,2) vs (4,1) equivalence (CAS law: layout equivalence)
- memory backend: overwrite same hash, delete-missing, list filter by algo

**Concurrency (with `-race`)**
- concurrent `Put` of the SAME hash (idempotent writers)
- concurrent `Get` while `Delete` runs (POSIX open-FD behavior)
- parallel `List`/`Stats` during writes (lock-free read path)

**Maintenance**
- `Verify`: intact, single flipped byte (each of first/middle/last), missing
  object
- `GC`: empty roots (deletes everything), all-reachable (deletes nothing),
  partial, unknown hashes in reachable set
- `Prune`: dry-run returns without deleting; `minAge=0`; younger objects kept;
  all-older destructive variant requires the explicit flag

**HTTP (httptest)**
- every route: success + 400/401/403/404/429 per the api-design table
- malformed `{hash}` params → 400; missing/expired session → 401 empty;
  wrong role → 403 empty; rate limit exceeded → 429 + headers
- streaming upload/download round-trip; oversized input rejected

---

## 4. Test Layers

1. **Unit** — table-driven per component (`hash`, `codec`, `store`,
   `fsstore`, `cache`), covering the §3 corner/error inventory.
2. **Property-style** — deterministic loops over generated hashes, digests of
   varying length, and all fan layouts; no external property library
   (std-lib only, coding-guidelines §3) — write small explicit generators.
3. **Fuzz** (`go test fuzz`):
   - `FuzzParseHash` — never panics; valid outputs round-trip
   - `FuzzPathRoundTrip` — arbitrary digest + layout → `hashPath` →
     `pathToHash` equality
   - `FuzzCodecRoundTrip` — `JSONCodec.Decode(Encode(x)) == x` for generated
     structs
   - `FuzzVerify` — corrupted bytes must fail `Verify`
   Commit corpora for regressions; run each target for a few seconds in CI,
   longer in nightly.
4. **Concurrency/race** — `go test -race` with concurrent
   `Put`/`Get`/`Delete`/`List` on one store; proves the lock-free read path
   (performance §2) and the cache's double-checked locking (§3 inventory).
5. **Corruption** — flip bytes on disk → `Verify` fails; `Get` returns the
   corrupted bytes (the store MUST NOT silently fix).
6. **Golden/NIST vectors** — `sha256("") == e3b0c442…`,
   `sha256("abc") == ba7816bf…`, SHA-1 vectors; assert the `Hash.String()`
   `"algo:hex"` format.
7. **HTTP** — `httptest` for CAS API handlers (role matrix → 401/403, rate
   limit → 429, streaming round-trip, OpenAPI served) and viewer routes
   (login, session, CSRF, fragments) — every route and every status per §2/§3.
8. **Backends under test** — unit/property/fuzz tests run against
   `MemoryRawStore` by default (fast, deterministic, no disk I/O); the CAS
   laws (§1) and the §3 inventory are table-driven over **both**
   `MemoryRawStore` and `FSRawStore` (including every fan-out layout), so the
   fs backend's integration behavior — atomic writes, fan-out paths, `.tmp`
   handling — stays covered where it differs.

---

## 5. Layout, Coverage Gate & CI

- Co-located `*_test.go` files; `Example` tests (`ExampleParseHash`, …) as
  documentation (coding-guidelines §7).
- CI: `go test -race ./...`; fuzz smoke runs; `benchstat` gate
  (performance §5).
- **Coverage — as high as practical**:
  - `cas/` core, `client/` (the public SDK) AND `examples/gitlike/`:
    **≥ 90%** statement coverage (excluding generated code); every exported
    identifier must be exercised at least once; any untested branch requires
    a comment explaining why.
  - HTTP surfaces: **every route** covered by `httptest` (§2 traceability);
  - Viewer templates: every named template rendered in at least one test.
- CI runs `go test -coverprofile` and fails the build below the bar; the
  coverage report is attached to PRs touching the core.
- Fuzz corpora are committed so regressions reproduce.

---

## 6. Checklist

- [ ] all CAS laws in §1 covered by tests
- [ ] every requirement ID (R-01…R-14, P-01…P-05, sentinel errors, ops,
      defaults) has at least one test, named with the ID (§2)
- [ ] corner & error inventory of §3 covered per component
- [ ] fuzz targets present, corpora committed, CI runs them
- [ ] `-race` concurrent test green (lock-free read path proven)
- [ ] corruption test proves `Verify` fails on a flipped byte
- [ ] golden hash vectors assert exact digests
- [ ] coverage ≥ 90% on `cas/` + `client/` + `examples/gitlike/`; every
      exported identifier exercised; untested branches commented
- [ ] every HTTP route tested (success + 400/401/403/404/429)
- [ ] new requirements come with their test (traceability is review-gated)
