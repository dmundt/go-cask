---
title: Defaults & Behavior — go-cask
description: The canonical reference for go-cask's basic design/architecture, default behavior, and every default value/constant — one place to look up how the system behaves out of the box and what the numbers are.
version: v10
---

# Defaults & Behavior — go-cask

> This document is the single reference for **"how does it behave by
> default?"** and **"what are the numbers?"** — grouped by area, each with a
> pointer to the owning spec. It is **canonical for default values**: area
> specs define contracts and may elaborate, but MUST NOT contradict this list
> (AGENT.md §8). When a default changes, update this document and the owning
> spec together, and bump versions per AGENT.md §3.

---

## 1. Basic Design & Architecture (summary)

- **Three layers** (cas-core §3): byte layer (non-generic: `Hash`,
  `RawStore`, backends) → typed layer (generic: `Object[T]`, `Codec[T]`,
  `Store[T]`, `Walker[T]`, caches) → application layer (per-app types;
  `gitlike` is the reference example).
- **One HTTP surface** (api-design §2): the viewer (`/viewer/*`, HTML). The
  product ships no network JSON API (backend-architecture §1); the
  `examples/api` pattern demonstrates a JSON surface.
- **One server, one mux** (backend-architecture §3–4) with the fixed
  middleware order: session auth → role → CSRF → handler.
- **Five maintenance operations** (consistency §8): `Verify`, `ScanRefs`,
  `GC`, `Prune`, `Stats`.
- Detail lives in `cas-core`, `backend-architecture`, `frontend-architecture` —
  this document only fixes the defaults.

---

## 2. Core Defaults & Constants (`cas`)

| Item                              | Default / value                                    | Defined in          |
| --------------------------------- | -------------------------------------------------- | ------------------- |
| Default hash algorithm            | `sha256`                                           | cas-core §4.2       |
| Built-in hash algorithms          | `sha1`, `sha256` (others via `RegisterHash`)       | cas-core §4.2       |
| Hash string format                | `"<algo>:<lowercase-hex>"` (e.g. `sha256:a1b2…`)   | cas-core §4.1       |
| Hash validation pattern           | `^[a-z0-9]+:[0-9a-f]+$`                            | api-design §3       |
| Fan-out layout                    | `FanOut=2`, `FanLevels=1` — prefix dirs; file name is always the full digest | cas-core §4.4       |
| Fan-out bound                     | `FanLevels × FanOut ≤ 64` (40 for SHA-1)           | cas-core §4.4       |
| Directory permissions             | `0o755`                                            | cas-core §4.4       |
| File permissions                  | `0o644`                                            | cas-core §4.4       |
| Default codec                     | `JSONCodec[T]`                                     | cas-core §4.6       |
| Read concurrency                  | lock-free (`Get`/`Exists`/`List`/`Stats`)          | cas-core §4.4       |
| Write concurrency                 | one `sync.Mutex` for `Put`/`Delete`                | cas-core §4.4       |
| Hash-on-write                     | single pass via `io.TeeReader`                     | performance §3      |
| Cache key                         | `h.String()` → `*CachedObject[T]` in `sync.Map`    | cas-core §4.10      |
| LRU `maxSize`                     | MUST be > 0                                        | cas-core §4.10      |
| Sentinel errors                   | `ErrNotFound`, `ErrHashMismatch`, `ErrUnknownAlgorithm`, `ErrInvalidHash`, `ErrUnknownType`, `ErrCorrupt` | library-design §2 |
| Object type name format           | `<type>@<major>` (e.g. `commit@1`); absent version reads as `@1` | object-versioning §2 |
| Object serialization envelope     | `{"type": "<type>@<major>", "data": <codec bytes>}` (JSON envelope) | cas-core §8, decision 1 |
| `Prune` dry-run default           | `true` (delete requires explicit flag)             | consistency §5      |
| `clean` default min-age           | 24 h                                                | cli §2              |
| Object age source                 | file mtime ≈ first-`Put` time                      | consistency §5      |

---

## 3. HTTP Defaults

| Item                          | Default / value                                        | Defined in        |
| ----------------------------- | ------------------------------------------------------ | ----------------- |
| Viewer prefix                 | `/viewer/` (HTML, unversioned)                         | api-design §2      |
| Example JSON prefix (pattern) | `/api/cas/v1/` in `examples/api`                       | api-design §12      |
| Example JSON rate limit       | 2 req/s per IP, burst 20; loopback exempt; 429 + `Retry-After` + `X-RateLimit-*` | api-design §8 |
| Example list pagination       | `limit=100` (1–1000), `offset=0` (≥0); `{total, objects}` envelope | api-design §10 |
| Error body (JSON surfaces)    | `{"error": "<message>"}`                               | api-design §6       |
| Binary payloads               | `application/octet-stream` + `X-CAS-Algorithm/Size` headers | api-design §11 |
| 401/403 (viewer)              | **empty body** (never disclose existence)              | api-design §5       |
| OpenAPI documents             | separate embedded `.yaml` per JSON surface (`examples/api/server/openapi.yaml`); the viewer needs none | api-design §13 |

---

## 4. Viewer Defaults

| Item                          | Default / value                                        | Defined in           |
| ----------------------------- | ------------------------------------------------------ | -------------------- |
| Viewer startup                | `cask web` IS the viewer; loopback-only default bind; startup admin token printed once | cli §2, viewer-security |
| Default bind                  | `127.0.0.1:8080` (loopback only)                       | viewer-security      |
| Short-hash display            | 8 hex chars of the digest (e.g. `9f86d081`)            | viewer-design §7     |
| Generic-list hash format      | `<shorthash> (<type>)` (e.g. `9f86d081 (blob)`)        | viewer-design §7     |
| Session idle timeout          | 30 minutes                                             | viewer-security      |
| Session maximum lifetime      | 8 hours                                                | viewer-security      |
| Session cookie               | `HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS     | viewer-security      |
| Login throttle                | max 5 failures/IP/minute with backoff                  | viewer-security      |
| Active-search trigger         | `input changed delay:300ms`                            | viewer-design §5     |
| GC progress polling           | `hx-trigger="every 2s"`                                | viewer-design §5     |
| Dashboard stat cards          | total objects, total size, algorithms in use           | viewer-design §7     |
| Roles                         | viewer (reads) / operator (+ store, verify) / admin (+ delete, GC, prune) | viewer-security |

---

## 5. Maintenance & Consistency Defaults

| Item                          | Default / value                                        | Defined in        |
| ----------------------------- | ------------------------------------------------------ | ----------------- |
| GC trigger                    | explicit only (never automatic)                        | consistency §4    |
| GC algorithm                  | mark-and-sweep from application roots                  | consistency §4    |
| `Verify` cadence              | scheduled full scan (nightly) + sampled scan on `List` | consistency §6    |
| Broken-object handling        | quarantine + audit-log + alert (never auto-fix)        | consistency §2    |
| Dangling-reference handling   | diagnostics only; repair is the app's job              | consistency §3    |
| Orphan `*.tmp` handling       | ignored by `List`/`Stats`; removed by the `clean` op   | operations §2     |
| Write durability              | temp file → `f.Sync()` → `os.Rename` (dir fsync optional) | operations §1  |
| Migration                     | optional; verify-before-delete; both algorithms coexist | operations §5    |

---

## 6. Performance Baselines

| Metric                                    | Default reference target                 | Defined in      |
| ----------------------------------------- | ---------------------------------------- | --------------- |
| Memory-backend small Put/Get              | ≥ 100k obj/s; p99 ≤ 1 ms; ≤ 5 allocs/op  | performance §11 |
| FS-backend small Put/Get (warm)           | ≥ 10k obj/s; p99 ≤ 5 ms                  | performance §11 |
| Large-object streaming (1 GiB)            | RSS ≤ 64 MiB above baseline              | performance §11 |
| `List` at 1M objects (fs, (2,2))          | ≤ 30 s                                   | performance §11 |
| Pack threshold (future)                   | objects ≤ 8 KiB; flush at 64 MiB         | performance §9  |

Baselines are calibratable on CI hardware (performance §11.4) — they are the
default targets, not absolutes.

---

## 7. Go & Project Defaults

| Item                          | Default / value                                        | Defined in           |
| ----------------------------- | ------------------------------------------------------ | -------------------- |
| Toolchain / `go.mod`          | Go 1.27                                                | coding-guidelines §1 |
| Library baseline              | Go 1.27 (self-managing toolchain)                       | library-design §5    |
| Dependencies                  | standard library only (external only if justified + vendored) | coding-guidelines §3 |
| Frontend scripting            | htmx only; no hand-written JS/CSS                      | coding-guidelines §4 |
| Lean-core budget              | `cas/` ≤ ~1500 LOC, ≤ ~20 exported identifiers         | library-design §1    |
| Stable core surface           | the identifiers in cas-core §7.1                       | cas-core §7.1        |
| Extension rule                | extend don't modify; own packages; stable surface only | extensions §1        |

---

## 8. Changing a Default

- Defaults are part of the **compatibility contract**: changing one is a
  **material change** — update this document AND the owning spec, and bump
  the versions of both (AGENT.md §3).
- A changed default must not break the stable surface (library-design §5);
  prefer additive options (e.g. a new `WithX` option) over silently changing
  behavior.
- When in doubt, keep the default — the defaults above are the "simplest
  powerful" choices; changing them needs a benchmark or a use case, not
  taste.

---

## 9. Checklist

- [x] Every default value/constant in this document matches its owning spec
- [x] Area specs reference (not contradict) this list for defaults
- [ ] Defaults changed only via the §8 procedure (both docs + version bumps)
- [ ] New defaults added here when new capabilities land (e.g. packfiles,
      prune tuning)
