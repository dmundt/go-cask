---
type: Specification
title: Defaults and Behavior — go-cask
description: The canonical reference for go-cask's basic design/architecture, default behavior, and every default value/constant — one place to look up how the system behaves out of the box and what the numbers are.
version: v52
---

# Defaults and Behavior — go-cask

- Default behavior and the numbers, by area, with the owning spec (AGENT.md §1).
- **Canonical for default values**: an area spec MAY elaborate, MUST NOT contradict (AGENT.md §8).
- Change a default → this document AND the owning spec, both version-bumped (AGENT.md §3).

## 1. Basic design and architecture

- Three layers (cas-core §3): byte (non-generic `Digest`/`Backend`/backends) → typed (generic
  `Object[T]`/`Codec[T]`/`Store[T]`/`Walker[T]`/caches) → application (per-app types; `gitlike`
  the reference).
- One HTTP surface (api-design §2): the viewer (`/viewer/*`, HTML). No network JSON API ships
  (backend-architecture §1); `examples/api` demonstrates a JSON surface.
- One server, one mux (backend-architecture §3–4); middleware order session auth → role → CSRF
  → handler.
- Four maintenance operations (consistency §8): `Verify`, `GC`, `Prune`, `Stats`. A fifth,
  `ScanRefs`, and the rest of the deferred surface: designed, not implemented (extensions §3).

## 2. Core defaults and constants (`cas`)

| Item | Default/value | Defined in |
|---|---|---|
| Hash algorithm in the core | none — `cas` names none; the client injects a `cas.Hasher` | cas-core §4.2 |
| Shipped client hasher | `sha256` (`cas/hash/sha256`: `sha256.New()`), wired by go-cask's clients: CLI, viewer, `gitlike`, examples | cas-core §4.2 |
| Digest text form | lowercase hex, no algorithm prefix (`Digest.String`/`MarshalText`) | cas-core §4.1 |
| Digest validation | `ParseDigest`/`UnmarshalText`: lowercase hex only; `sha256.Parse` also `sha256:` | cas-core §4.1, §4.2 |
| Printable digest form | `"sha256:hexdigest"` (`sha256.Format`; bare hex parses too) | cas-core §4.2 |
| Fan-out layout | `FanOut=2`, `FanLevels=1` → `<base>/<fan-out dirs>/<full hex digest>`; no algorithm dir; file name = the full digest | cas-core §4.4 |
| Fan-out bound | `FanLevels × FanOut ≤ 64` | cas-core §4.4 |
| Dir / file perms | `0o755` / `0o644` | cas-core §4.4 |
| Default codec | JSON (`json.New[T]()`), uncompressed — no default constructor wraps a payload in a codec | cas-core §4.6 |
| Default compression codec | `flate` (`cas/codec/flate`) for a payload needing compression (smallest of the three, no header); opt-in, never from a default constructor | cas-core §4.6; extensions §3 |
| Compact binary codec | optional app codec (`binary.New(next, transform, restore)` or `binary.NewRaw(encode, decode)`) for stable, compact binary payloads | cas-core §4.6 |
| Decompression ceiling | `MaxDecodedBytes` = 1 GiB per `Decode` in `cas/codec/{flate,gzip,zlib}`; past it `ErrDecodedTooLarge`, one value for all three, so `errors.Is` holds | cas-core §4.6 |
| Decoder nesting ceiling | `cbor.MaxDepth` = 128 array/map levels per `Decode` in `cas/codec/cbor`; past it `ErrTooDeep`, never a stack overflow; at or below, unchanged | cas-core §4.6 |
| Header-peek ceiling | `PeekType` reads a codec tag or type name of at most 4096 bytes; a larger declared length is `ErrCorrupt`, never allocated | cas-core §4.6 |
| Version-peek cost | `PeekVersion`/`Store.Version` read one byte — the leading version byte — whatever the payload size, reported verbatim including a version this build does not know; only an empty stream or read failure is `ErrCorrupt` | cas-core §4.6, §4.8 |
| Read concurrency | `fs`: lock-free (`Get`/`Exists`/`List`/`Stats`); `mem`: `RWMutex`; `packfs`: its in-memory index mutex | cas-core §4.4, §4.14 |
| Write concurrency | one `sync.Mutex` for `Put`/`Delete` | cas-core §4.4 |
| Storage backend selection | `fs` (loose filesystem, Git-like fan-out) default; `packfs` opt-in (`cask -backend packfs`, `packfs.WithEnabled()`) | cli §1, cas-core §4.14 |
| Hash-on-write | one pass, spool + hasher (`io.MultiWriter`) | performance §3 |
| Cache key | `d.String()` → `*CachedObject[T]` in `sync.Map` | cas-core §4.10 |
| LRU `maxSize` | MUST be > 0 | cas-core §4.10 |
| Sentinel errors | `ErrNotFound`, `ErrDigestMismatch`, `ErrInvalidDigest`, `ErrUnknownType`, `ErrCorrupt`, `ErrCodecMismatch`, `ErrUnsupported` | library-design §2 |
| Object type name | `<type>@<major>`; absent version reads as `@1` | object-versioning §2 |
| Serialization envelope | TLV `[version u8 = 2][uvarint codecLen][codec][uvarint typeLen][type][uvarint payloadLen][payload]`; a version 1 envelope (no codec field) reads as "codec unspecified" | cas-core §8 d1 |
| Codec identity tags | `json`, `gob`, `cbor`, `binary`; a stacked codec composes the inner tag (`gzip+json`, `flate+gzip+json`); a codec with no tag (`cas.CodecNamer`) writes an empty tag, no comparison | cas-core §4.6 |
| `Prune` dry-run default | `true` (delete needs explicit flag) | consistency §5 |
| `clean` default min-age | 24 h | cli §2 |
| Object age source | file mtime ≈ first-`Put` time | consistency §5 |
| Bloom filter ceiling | `bloom.MaxBits = 1<<32` bits per filter — a standard/persistent bitmap 512 MiB, a counting filter 2 GiB (32-bit counter per slot); shard the key space rather than raise it | `cas/bloom/common.go` (no spec states this ceiling yet) |
| Recorded sidecar checksums | off by default — no record unless a caller wraps a backend with `sidecar.New(..., WithChecksum(algo, hasher))`; the checksum covers the stored bytes, written after the object is published | operations §6 |
| Sidecar record directory | `<base>/.meta`; `<base>` is the backend's `BasePath()` (`fs`: the path passed to `fs.New`; `packfs`: loose tree `<base>/loose`) | operations §6 |
| Sidecar record version | `1` (`sidecar.RecordVersion`); any other version reads as `cas.ErrCorrupt` | operations §6 |
| Sidecar record read cap | `sidecar.DefaultMaxRecordBytes` = 4096 bytes per record; a larger read is `cas.ErrCorrupt` (`WithMaxRecordBytes` overrides it). The same cap bounds the writer: a record past it drops the optional `type`/`codec` fields, and one that still does not fit is refused with `sidecar.ErrRecordTooLarge`, writing nothing | operations §6 |
| Sidecar scratch durability | the record temp file is fsynced before its rename; the directory fsync is opt-in (`WithDirSync`), matching the backend's default | operations §1, §6 |
| Recorded-checksum algorithm (CLI) | `crc32` for `cask verify -checksums -checksum <algo>` (also `adler32`, `crc64`); a read compares the record's `checksum_algo` | cli §4, operations §6 |

## 3. HTTP defaults

| Item | Default/value | Defined in |
|---|---|---|
| Viewer prefix | `/viewer/` (HTML, unversioned) | api-design §2 |
| Example JSON prefix | `/api/cas/v1/` in `examples/api` | api-design §12 |
| Example JSON rate limit | 2 req/s per IP, burst 20; loopback exempt; 429 + `Retry-After` + `X-RateLimit-*` | api-design §8 |
| Example JSON body bound | objects `64 MiB` (`-max-size`), `/gc` reachable set `8 MiB`; over the bound → `413` before reading | api-design §9 |
| Example JSON connection lifetimes | `ReadHeaderTimeout` 10 s, `ReadTimeout` 60 s, `WriteTimeout` 5 min, `IdleTimeout` 2 min | api-design §9 |
| Example JSON credentials | none shipped: `-tokens` has no default and the server refuses to start without it; no role name is a token | api-design §7 |
| Example list pagination | `limit=100` (1–1000), `offset=0` (≥0); `{total, objects}` envelope | api-design §10 |
| Error body (JSON) | `{"error": "<message>"}` | api-design §6 |
| Binary payloads | `application/octet-stream` + `X-CAS-Algorithm/Size` headers | api-design §11 |
| Viewer 401/403 | **empty body** (never disclose existence) | api-design §5 |
| OpenAPI | separate embedded `.yaml` per JSON surface (`examples/api/server/openapi.yaml`); viewer needs none | api-design §13 |

## 4. Viewer defaults

| Item | Default/value | Defined in |
|---|---|---|
| Startup | `cask web` IS the viewer; loopback-only default bind; the admin token is never logged — shown once on stdout **for a loopback bind only** (`-show-token` forces it without a terminal, `-show-token=false` suppresses it, absent keeps the terminal heuristic), or supplied with `-token-file`/`CASK_VIEWER_TOKEN`; a non-loopback bind prints no login link and no token, only the bind and the `https://` expectation; the browser launch needs a loopback bind and the display not suppressed | cli §2, viewer-security §11 |
| Default bind | `127.0.0.1:8080`; the loopback spellings `127.0.0.1`, `localhost` and `[::1]` are pinned to an explicit numeric loopback address before listening (`localhost` → `127.0.0.1`), so the listener never depends on the host resolver | viewer-security §4 |
| Short-hash display | 8 hex chars (`9f86d081`) — `cas.Digest.Prefix(8)`, the core's total display helper | viewer-design §7 |
| Generic-list row | two cells from the object row: short digest (`cas.Digest.Prefix(8)`) and type label (`unreadable` when the bytes could not be read); never a composite `<shorthash> (<type>)` string | viewer-design §1, §7 |
| Session idle timeout | 30 min | viewer-security |
| Session max lifetime | 8 h | viewer-security |
| Session cookie | always `HttpOnly`, `SameSite=Strict` and `Secure` | viewer-security |
| Login throttle | max 5 failures/caller-address/min with backoff | viewer-security §5 |
| Request body bound | **4 KiB** (`maxRequestBodyBytes`) on every viewer route, in one inherited middleware; a body over it answers `413` **before** parsing, so it is never read and a multipart body is never spooled; an oversized `POST /viewer/login` creates no session | viewer-security §13 |
| Server connection limits | `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `WriteTimeout` 120 s, `IdleTimeout` 120 s (`cmd/cask/web.go`); the write deadline also covers handler execution, leaving room for a verify-all sweep | viewer-security §13 |
| Expensive operations | verify-all and a metadata-snapshot rebuild: **one at a time**, per session a burst of 3 then one per 5 s; past it `429` + `Retry-After`; a refused rebuild serves the published snapshot (staleness ≤ 5 s) | viewer-design §3 |
| Trusted proxies | none (`-trusted-proxy` empty): a forwarded client address is ignored, the direct peer keys the throttle | viewer-security §5.2, cli §2 |
| Active-search trigger | `input changed delay:300ms` | viewer-design §5 |
| Object-list pagination | `limit=25`, `offset=0`; allowed limits `25`, `50`, `100`, `250` | viewer-design §5 |
| Object-list initial sort | hash ascending; sort/filter/page state URL-addressable | viewer-design §5 |
| Viewer admin action | verify only (one object, or every object); no delete, GC or prune route | viewer-design §5, viewer-security §8 |
| Roles | viewer (read) / operator (+verify) / admin (same as operator — no destructive action exposed) | viewer-security §8 |

## 5. Maintenance and consistency defaults

| Item | Default/value | Defined in |
|---|---|---|
| GC trigger | explicit only (never automatic) | consistency §4 |
| GC algorithm | mark-and-sweep from application roots | consistency §4 |
| GC grace default | `--min-age` **1 h** (`gcDefaultGrace`): a sweep reclaims only unreachable objects older than the grace, so a fresh write from a live writer survives a racing sweep; `--min-age 0` is the dangerous variant and warns | cli §2, consistency §4 |
| `Verify` cadence | on demand (CLI `verify <hash>\|--all`, viewer Verify control); nothing sampled, nothing scheduled | consistency §2, §6 |
| Broken-object handling | report + audit-log (`ErrDigestMismatch`/`Report.Bad`, CLI `CORRUPT` line, viewer audit); no quarantine, no alert | consistency §2 |
| Dangerous all-objects prune | no dedicated mode: `cask prune` needs a root list, `--dry-run` is the default, `--min-age 0` warns | consistency §5 |
| Deferred maintenance surface | quarantine, `ScanRefs`, sampled/scheduled `Verify`, store metric counters + viewer stats page, latency-threshold logging, `.meta/<digest>.json` descriptor, viewer delete/GC routes — designed only, each with what exists and what does not | extensions §3 |
| Dangling-ref handling | diagnostics only; repair is the app's job | consistency §3 |
| Orphan `*.tmp` | ignored by `List`/`Stats`; removed by `clean` | operations §2 |
| Write durability | temp file → `f.Sync()` → `os.Rename` (dir fsync optional) | operations §1 |
| Migration | re-digest + rewrite, verify-before-delete; no algorithm dir and no registry to consult (a store holds one digest format) | operations §5 |

## 6. Performance baselines

| Metric | Default target | Defined in |
|---|---|---|
| Memory-backend small (64 B) **byte-layer** Put/Get round trip | target ≥100k obj/s and ≤5 allocs/op — **aspirational**, nothing enforces it; the closest real record is `BenchmarkBackendWriteRead/mem/steady-state/64B` in `benchmarks/data/baseline.txt` (2026-09: 512.7 ns/op ≈ 1.95M ops/s, 10 allocs/op for the combined Put+Get round trip; the Store-level path adds codec + envelope + hashing and is not held to this number) | performance §11 |
| FS-backend small Put/Get (warm) | ≥10k obj/s; p99 ≤5 ms | performance §11 |
| Large-object streaming (1 GiB) | RSS ≤64 MiB above baseline | performance §11 |
| `List` at 1M objects (fs, (2,2)) | ≤30 s | performance §11 |
| Packfile rotation (`packfs`) | no size threshold — every `Put` mirrored loose **and** into the active pack; rotation at `PackMaxBytes` = 64 MiB or `PackMaxEntries` = 10 000 (`0` = unlimited) | cas-core §4.14, performance §9 |

- Aspirational targets, not absolutes — nothing enforces them (performance §11.3: no CI gate, no
  scenario harness, performance §5); the duration and RSS rows have no harness at all, and only
  the `benchmarks/` suite's `ns/op` and `allocs/op` are recorded, in `benchmarks/data/baseline.txt`.

## 7. Go and project defaults

| Item | Default/value | Defined in |
|---|---|---|
| Module floor (`go.mod`) | `go 1.24.0` — minimum supported Go, the `omitzero` JSON-tag floor | coding-guidelines §1, library-design §5 |
| Build/gate toolchain | `toolchain go1.27.1` — what the repository builds and gates with | coding-guidelines §1, library-design §5 |
| Library baseline | Go 1.24+ (`omitzero` JSON tags) | library-design §5 |
| Dependencies | standard library plus approved `golang.org/x/sys` mmap support; additions need justification | coding-guidelines §3 |
| Frontend scripting | htmx only; no hand-written JS; one scoped embedded viewer stylesheet | coding-guidelines §4 |
| Lean-core budget | `cas/` ≤ ~1600 code lines (non-blank, non-comment; 906 today), ≤ ~45 exported identifiers (45 today) | library-design §1 |
| Stable core surface | identifiers in cas-core §7.1 | cas-core §7.1 |
| Extension rule | extend don't modify; own packages; stable surface only | extensions §1 |

## 8. Changing a default

- Defaults are the **compatibility contract**: a change is **material** — update this document
  AND the owning spec, bumping both (AGENT.md §3).
- A changed default MUST NOT break the stable surface (library-design §5); prefer an additive
  option (a new `WithX`) to a silent behavior change.
- When in doubt, keep the default — a change needs a benchmark or a use case, not taste.

## 9. Checklist

- [x] Every default in this document matches its owning spec
- [x] Area specs reference (not contradict) this list for defaults
- [x] Defaults changed only via the §8 procedure (both docs + version bumps)
- [x] New defaults added here when new capabilities land (e.g. packfiles, prune tuning)
