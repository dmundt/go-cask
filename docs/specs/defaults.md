---
type: Specification
title: Defaults & Behavior — go-cask
description: The canonical reference for go-cask's basic design/architecture, default behavior, and every default value/constant — one place to look up how the system behaves out of the box and what the numbers are.
version: v19
---

# Defaults & Behavior — go-cask

Single reference for "how does it behave by default?" and "what are the numbers?", grouped by area with pointers to the owning spec. **Canonical for default values**: area specs MAY elaborate but MUST NOT contradict this list (AGENT.md §8). On a default change, update this document AND the owning spec and bump both versions (AGENT.md §3).

## 1. Basic design & architecture

- Three layers (cas-core §3): byte (non-generic `Digest`/`Backend`/backends) → typed (generic `Object[T]`/`Codec[T]`/`Store[T]`/`Walker[T]`/caches) → application (per-app types; `gitlike` is the reference).
- One HTTP surface (api-design §2): the viewer (`/viewer/*`, HTML). No network JSON API ships (backend-architecture §1); `examples/api` demonstrates a JSON surface.
- One server, one mux (backend-architecture §3–4), fixed middleware order: session auth → role → CSRF → handler.
- Five maintenance operations (consistency §8): `Verify`, `ScanRefs`, `GC`, `Prune`, `Stats`.

## 2. Core defaults & constants (`cas`)

| Item | Default/value | Defined in |
|---|---|---|
| Hash algorithm in the core | none — `cas` names no algorithm; the client injects a `cas.Hasher` | cas-core §4.2 |
| Shipped client hasher | `sha256` (`cas/hash/sha256`: `sha256.New()`; go-cask's own clients — CLI, viewer, `gitlike`, examples — wire it) | cas-core §4.2 |
| Digest text form | lowercase hex with no algorithm prefix (`Digest.String`/`MarshalText`) | cas-core §4.1 |
| Digest validation | core `ParseDigest`/`UnmarshalText` accept lowercase hex only; the client's `sha256.Parse` also accepts the printable `sha256:` prefix | cas-core §4.1, §4.2 |
| Printable digest form | `"sha256:hexdigest"` (`sha256.Format`; bare hex also parses) | cas-core §4.2 |
| Fan-out layout | `FanOut=2`, `FanLevels=1` → `<base>/<fan-out dirs>/<full hex digest>`; no algorithm directory; file name always the full digest | cas-core §4.4 |
| Fan-out bound | `FanLevels × FanOut ≤ 64` | cas-core §4.4 |
| Dir / file perms | `0o755` / `0o644` | cas-core §4.4 |
| Default codec | JSON (`json.New[T]()`) | cas-core §4.6 |
| Read concurrency | lock-free (`Get`/`Exists`/`List`/`Stats`) | cas-core §4.4 |
| Write concurrency | one `sync.Mutex` for `Put`/`Delete` | cas-core §4.4 |
| Hash-on-write | one pass, spool + hasher (`io.MultiWriter`) | performance §3 |
| Cache key | `d.String()` → `*CachedObject[T]` in `sync.Map` | cas-core §4.10 |
| LRU `maxSize` | MUST be > 0 | cas-core §4.10 |
| Sentinel errors | `ErrNotFound`, `ErrDigestMismatch`, `ErrInvalidDigest`, `ErrUnknownType`, `ErrCorrupt` | library-design §2 |
| Object type name | `<type>@<major>`; absent version reads as `@1` | object-versioning §2 |
| Serialization envelope | TLV `[version u8][uvarint typeLen][type][uvarint payloadLen][payload]` | cas-core §8 d1 |
| `Prune` dry-run default | `true` (delete needs explicit flag) | consistency §5 |
| `clean` default min-age | 24 h | cli §2 |
| Object age source | file mtime ≈ first-`Put` time | consistency §5 |

## 3. HTTP defaults

| Item | Default/value | Defined in |
|---|---|---|
| Viewer prefix | `/viewer/` (HTML, unversioned) | api-design §2 |
| Example JSON prefix | `/api/cas/v1/` in `examples/api` | api-design §12 |
| Example JSON rate limit | 2 req/s per IP, burst 20; loopback exempt; 429 + `Retry-After` + `X-RateLimit-*` | api-design §8 |
| Example list pagination | `limit=100` (1–1000), `offset=0` (≥0); `{total, objects}` envelope | api-design §10 |
| Error body (JSON) | `{"error": "<message>"}` | api-design §6 |
| Binary payloads | `application/octet-stream` + `X-CAS-Algorithm/Size` headers | api-design §11 |
| Viewer 401/403 | **empty body** (never disclose existence) | api-design §5 |
| OpenAPI | separate embedded `.yaml` per JSON surface (`examples/api/server/openapi.yaml`); viewer needs none | api-design §13 |

## 4. Viewer defaults

| Item | Default/value | Defined in |
|---|---|---|
| Startup | `cask web` IS the viewer; loopback-only default bind; admin token printed once | cli §2, viewer-security |
| Default bind | `127.0.0.1:8080` | viewer-security |
| Short-hash display | 8 hex chars (`9f86d081`) | viewer-design §7 |
| Generic-list hash format | `<shorthash> (<type>)` | viewer-design §7 |
| Session idle timeout | 30 min | viewer-security |
| Session max lifetime | 8 h | viewer-security |
| Session cookie | `HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS | viewer-security |
| Login throttle | max 5 failures/IP/min with backoff | viewer-security |
| Active-search trigger | `input changed delay:300ms` | viewer-design §5 |
| GC progress polling | `hx-trigger="every 2s"` | viewer-design §5 |
| Dashboard stat cards | total objects, total size + the addressing note (digests are raw hex; this viewer uses `sha256`) | viewer-design §7 |
| Roles | viewer (read) / operator (+store, verify) / admin (+delete, GC, prune) | viewer-security |

## 5. Maintenance & consistency defaults

| Item | Default/value | Defined in |
|---|---|---|
| GC trigger | explicit only (never automatic) | consistency §4 |
| GC algorithm | mark-and-sweep from application roots | consistency §4 |
| `Verify` cadence | scheduled full (nightly) + sampled on `List` | consistency §6 |
| Broken-object handling | quarantine + audit-log + alert (never auto-fix) | consistency §2 |
| Dangling-ref handling | diagnostics only; repair is the app's job | consistency §3 |
| Orphan `*.tmp` | ignored by `List`/`Stats`; removed by `clean` | operations §2 |
| Write durability | temp file → `f.Sync()` → `os.Rename` (dir fsync optional) | operations §1 |
| Migration | re-digest + rewrite, verify-before-delete; no algorithm dir and no registry to consult (a store holds one digest format) | operations §5 |

## 6. Performance baselines

| Metric | Default target | Defined in |
|---|---|---|
| Memory-backend small (64 B) **byte-layer** Put/Get | ≥100k obj/s; p99 ≤1 ms; ≤5 allocs/op — measured by `BenchmarkMemBackendPut/Get` (2026-09: Put/64 B ≈ 5 allocs, Get ≈ 2 allocs; the Store-level path adds codec + envelope + hashing and is not held to this number) | performance §11 |
| FS-backend small Put/Get (warm) | ≥10k obj/s; p99 ≤5 ms | performance §11 |
| Large-object streaming (1 GiB) | RSS ≤64 MiB above baseline | performance §11 |
| `List` at 1M objects (fs, (2,2)) | ≤30 s | performance §11 |
| Pack threshold (future) | objects ≤8 KiB; flush at 64 MiB | performance §9 |

Baselines are calibratable on CI hardware (performance §11.4) — default targets, not absolutes.

## 7. Go & project defaults

| Item | Default/value | Defined in |
|---|---|---|
| Toolchain / `go.mod` | Go 1.27 | coding-guidelines §1 |
| Library baseline | Go 1.24+ (`omitzero` JSON tags) | library-design §5 |
| Dependencies | std-lib only (external only if justified + vendored) | coding-guidelines §3 |
| Frontend scripting | htmx only; no hand-written JS/CSS | coding-guidelines §4 |
| Lean-core budget | `cas/` ≤ ~1600 LOC, ≤ ~40 exported | library-design §1 |
| Stable core surface | identifiers in cas-core §7.1 | cas-core §7.1 |
| Extension rule | extend don't modify; own packages; stable surface only | extensions §1 |

## 8. Changing a default

- Defaults are part of the **compatibility contract**: changing one is a **material change** — update this document AND the owning spec and bump both (AGENT.md §3).
- A changed default MUST NOT break the stable surface (library-design §5); prefer additive options (e.g. a new `WithX`) over silently changing behavior.
- When in doubt, keep the default — change needs a benchmark or a use case, not taste.

## 9. Checklist

- [x] Every default in this document matches its owning spec
- [x] Area specs reference (not contradict) this list for defaults
- [x] Defaults changed only via the §8 procedure (both docs + version bumps)
- [x] New defaults added here when new capabilities land (e.g. packfiles, prune tuning)
