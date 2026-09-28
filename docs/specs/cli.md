---
type: Specification
title: CLI — go-cask
description: The contract for cmd/cask — the single entry point: a thin command-line client over the cas library, plus the embedded viewer via the web subcommand; subcommands, flags, output format, auth, and exit codes.
version: v40
---

# CLI — go-cask

Thin CLI over the cas library plus, via `web`, the embedded viewer. Every operation maps to a core
operation (cas-core §4) or the viewer server composition (backend-architecture §3); no network JSON
API (backend-architecture §1). Related: cas-core, backend-architecture, viewer-design,
viewer-security, consistency (GC/prune), versioning (version output).

## 1. Purpose and modes

Only entry point; no separate server binary. Every operation calls the library in-process.

| Mode | Flag | Talks to | Auth |
|---|---|---|---|
| local | `-store <path>` `-backend fs\|packfs` | library in-process over the selected storage backend (`fs` by default) | none (filesystem trust) |

- `-store` required for store operations; no remote mode.
- Store path must be the store's own directory: both backends reject an empty value, `.`, a
  filesystem or volume root and a parent-traversal path before creating anything
  (`fs.ValidateBase`, cas-core §4.4); `-store root/name` is fine.
- `-backend` = the backend every store operation runs against: `fs` (default, Git-like fan-out
  loose objects) or `packfs` (loose tree plus append-only pack files).
- One shared constructor opens it and reports `cas.Capabilities`, so
  `put`/`get`/`list`/`meta`/`stats`/`verify`/`gc`/`prune`/`clean` work over either
  (backend-architecture §5).
- Refusal matrix: either backend for every store operation, `fs` only for the viewer; `web` is the
  single refusal, naming operation, backend and remedy (§2, viewer-design §1).
- Hash algorithm = **client** constant: `cmd/cask` digests and validates with `cas/hash/sha256`
  (`sha256.Format` prints `sha256:hexdigest`; `sha256.Parse` accepts it or bare hex).
- No **store operation** takes an algorithm flag; the core names none (cas-core §4.2).
- Both viewer subcommands take `-hash-algo` (`sha256`, `sha512`, `sha512_256`): `web` for the
  viewer, `seed-preview` for the graph it seeds; the two MUST agree or the viewer finds no graph
  (§2, §4).
- `web` = the **viewer shape** (backend-architecture §3): store from `-store`, role=token pairs from
  `-tokens` (viewer-security); token display, bind rules and refusals: the `web` row, §4.

## 2. Subcommands

| Command | Behavior |
|---|---|
| `put <file>\|- [-json]` | store bytes (or stdin); prints the hash (`sha256:hexdigest`) |
| `get <hash> [-o <file>]` | retrieve to stdout, or a file with `-o` |
| `list [-limit <n>] [-offset <n>] [-type <type[@major]>] [-codec <tag>] [-json]` | list objects (`{total, objects}` shape, `total` = the matches when a filter is set); `-type`/`-codec` filter on the envelope header — a bare type name reads as `@1`, `-codec unspecified` selects frames carrying no identity; a value no object carries → empty result at exit 0; a digest-named file that is not a readable object (a stray file in the store directory) is skipped with a stderr warning, not a failure (cas-core §4.4) |
| `meta <hash> [-json]` | metadata of one object (size, type, frame version, codec, algorithm) |
| `stats [-json]` | statistics (`N objects, M bytes`) plus a header census: per type, per frame version, per codec (§3) |
| `verify [-hash-algo <name>] <hash>\|--all [-checksums [-checksum <algo>]]` | integrity check (single object or full scan; `-hash-algo` = the algorithm the addresses are expressed in; `-checksums` = check the recorded checksum instead of the address) |
| `gc --min-age <dur> <roots...>` | reclaim objects absent from `<roots...>` AND older than `--min-age` (grace default 1h; `--min-age 0` = immediate, dangerous); `<roots...>` must already be the complete reachable set, not just entry points |
| `prune --min-age <dur> <roots...> [--dry-run]` | age-based retention (dry-run default); same reachable-set contract as `gc` |
| `clean [--min-age <dur>]` | remove orphan `*.tmp` files older than `--min-age` (default 24 h) |
| `seed-preview [-count <n>] [-hash-algo <name>]` | add 500 deterministic, valid envelope objects for local viewer preview; `-count` accepts 1–10000 |
| `web [-store <dir>] [-backend <name>] [-bind <addr>] [-hash-algo <name>] [-tokens r=t,...] [-token-file <path>] [-trusted-proxy <ip\|cidr,...>] [-allow-insecure-bind] [-show-token] [-no-open]` | start the embedded viewer (backend-architecture §3); browser side: [`cmd/cask/README.md`](../../cmd/cask/README.md); token **never logged**; generated token shown once on **stdout**, **loopback bind only** — interactive stdout or `-show-token`; `-show-token=false` suppresses display and browser launch; absent flag keeps the terminal heuristic; unattended token: `-token-file <path>` or `CASK_VIEWER_TOKEN` (`-token-file` wins); opens the default browser unless `-no-open`, a non-loopback bind, or a suppressed display (viewer-security §11); Windows launch: `rundll32.exe url.dll,FileProtocolHandler`, not `cmd /c start`; `-backend` `fs` only; `-hash-algo` `sha256`/`sha512`/`sha512_256`, shown in Metadata → Identity → Algorithm; `-trusted-proxy` = reverse proxies whose forwarded client address the login throttle may believe (viewer-security §5.2), empty default believes none; entries IP, `ip:port`, CIDR; malformed entry → exit 1; loopback `-bind` spelling (`127.0.0.1`, `localhost`, `[::1]`) pinned to a numeric address before listening (`localhost` → `127.0.0.1`), `-bind localhost:<port>` accepted (viewer-security §4); non-loopback bind refused without `-allow-insecure-bind` (`:port`, `0.0.0.0:port`, `[::]:port`) with a prominent warning (viewer-security §4); cookies always `Secure` (viewer-security §7); notice prints the bind and the `https://` expectation, not a login link; `-config` deferred |
| `version` | print library + Go version |

- Hash args parsed with the selected algorithm's parser — `<name>:hexdigest` or bare hex, `sha256`
  unless `-hash-algo` names another (§4); malformed or another algorithm's prefix → usage error
  (exit 2).
- `gc`/`prune` destructive, **grace-gated**: reclaim only objects absent from `<roots...>` AND older
  than `--min-age` (default 1h) (cas-core §6).
- `<roots...>` = the complete reachable set at the byte layer, not entry points: the CLI cannot
  expand a root, so pass every digest that must survive (cas-core §4.11; `cas.Reachable`).
- `prune` defaults to `--dry-run`; `gc` prints the deleted count (consistency §4–§5); `--min-age 0`
  warns.
- **Store lock:** sweeps (`gc`/`prune`/`clean`) take the exclusive cross-process lock (`.cask.lock`
  at the store root, holding the PID); a second holder → exit 1 naming its PID and the
  stale-lock-file remedy.
- Writers (`put`) and the viewer (`web`) never lock; reads (`get`/`list`/`meta`/`stats`/`verify`)
  never lock.
- **Backend-agnostic maintenance:** `verify` via `cas.Verify`/`cas.VerifyAll`; `gc`/`prune` use the
  native `Prune` when present (`fs`), else the portable `cas.Sweep` (`packfs`); `clean` uses the
  backend's `cas.Cleaner`.
- Unsupported operation → error naming operation and backend, wrapping `cas.ErrUnsupported`
  (exit 1); never a silent success.
- Store opened and closed per command: `packfs` releases its open pack file (reporting a close
  failure) before returning; no index is buffered for a separate flush.
- `web` requires `fs`: physical metadata comes from the concrete filesystem backend, so
  `web -backend packfs` is refused with `cas.ErrUnsupported` (exit 1), not a directory other than
  the one `-store` named. The refusal names operation, backend **and remedy** (`-backend fs`, or
  `-store` with a loose store's directory). Viewer `-backend` defaults to the global flag (cli.md
  §1, viewer-design §1).
- `seed-preview`: valid, deterministically addressed TLV envelopes — representative type names,
  payload sizes, graph edges, alternating root-reachable segments; current format
  (`cas.EnvelopeVersion`), codec tag `preview`, synthetic payload no shipped codec produced; built
  by `cas.EncodeEnvelope`, the writer `Store.Put` frames through, so a seeded digest = the digest
  the store would compute for the same bytes (addresses change with the frame; go-cask#187).
- Block layout (8 objects): a Root (reachable, no inbound edges), orphans with inbound edges, a
  Detached orphan entry (unreachable, no inbound edges) — all four reference states; outgoing
  references cycle zero, one, two, three.
- Idempotent per `-count`: a rerun reports deduplicated objects.
- `-hash-algo` selects the graph's digest algorithm and **must match** `cask web -hash-algo`, or the
  viewer finds no graph.
- A missing object does not truncate the graph: sweeping a Detached entry drops only its own edges.
- `web` recognizes only this graph; other stores stay reference-free unless their host provides a
  viewer source.
- Every eighth preview object carries deliberately tampered bytes that do not hash to their own
  address, so `verify` fails for them; the ordinals sit in root-reachable segments, keeping the
  viewer's corrupt and orphan axes independent.
- One payload byte flips, so the envelope type still reads.
- Preview objects are local store data, never repository fixtures or fabricated viewer metadata.
- `web` is the only non-terminating subcommand: runs until signalled (graceful shutdown per
  backend-architecture §6).

## 3. Output and exit codes

- Plain text by default: one hash per line for `put`/`list`; human-readable summaries for
  `stats`/`meta`/`verify`/`gc`/`prune`.
- `-json` → `put` `{"hash": "sha256:hexdigest", "deduplicated": bool}`, `list`
  `{"total": n, "objects": [{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n, "type": "…", "version": 2, "codec": "…"}, …]}`,
  `meta` `{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n, "type": "…", "version": 2, "codec": "…"}`,
  `stats` `{"objects": n, "bytes": m, "unreadable": u, "headerless": h, "types": {"blob@1": n, …}, "versions": {"2": n, …}, "codecs": {"json": n, …}}`.
- `"algorithm"` = the client's constant; the core does not report it.
- **Header census, one convention across the surfaces:** `type` = versioned envelope type (`""` if
  none); `version` = frame's leading byte (`0` when no walkable header — raw bytes written by
  `put`); `codec` = writing codec's identity tag, literal `unspecified` if none (a version 1 frame,
  or a version 2 frame whose codec declared no tag): never blank, never a guess.
- `stats -json` counts per axis only objects whose header was read:
  `sum(types) == sum(versions) == sum(codecs) == objects - unreadable - headerless`; enveloped
  stores sum to the object count exactly.
- The same read feeds the viewer (viewer-design §3): `list -json`, `meta -json`, `stats -json`, the
  object table and the inspector agree digest by digest.
- Errors on stderr, never stdout; the viewer's one-time login notice (§1, §2) is deliberate output,
  so it goes to stdout.

| Exit | Meaning |
|---|---|
| 0 | success |
| 1 | runtime error (store/IO) — message on stderr |
| 2 | usage error (unknown command, bad flags, invalid hash) |

## 4. Conventions

- Flags: single-dash long names (`-store`, `-backend`, `-json`, `-o`, `-min-age`, `-dry-run`,
  `-limit`, `-offset`, `-count`, `-bind`, `-hash-algo`, `-checksums`, `-checksum`, `-tokens`,
  `-token-file`, `-trusted-proxy`, `-allow-insecure-bind`, `-show-token`, `-no-open`, `-config`).
- `-show-token` = show the generated token's one-time login hint even without a terminal;
  `-show-token=false` never shows it and never opens the browser; a loopback `-bind` is required
  either way.
- `-no-open` = skip opening the browser; `-config` deferred.
- `-hash-algo` = the digest algorithm the **addresses** are expressed in: `sha256` (default),
  `sha512`, `sha512_256`; unknown name → usage error (exit 2).
- `verify` takes it for the single-object check, `--all` and `--checksums` alike: without it the
  sha256 hasher refuses a wider digest on width before reading a byte, and `--all` aborts.
- The viewer path (`web`, `seed-preview`) takes it too, and must agree with how the store was
  seeded.
- Every other store operation (`put`/`get`/`list`/`meta`/`stats`/`gc`/`prune`/`clean`) speaks the
  client constant `sha256` — the deliberate exception.
- `-checksums` = `verify`'s recorded-checksum mode (operations §6): read the record beside each
  object instead of recomputing its address — cheap, not an identity check.
- `-checksum <algo>` = which record: `crc32` (default), `adler32`, `crc64`; requires `-checksums`
  (else exit 2); unknown name → usage error. A record written by another algorithm → runtime error
  naming the mismatch, never corruption.
- No record → reports unchecked, does **not** fail the command; a mismatch or an unreadable record →
  exit 1.
- Mismatch line `CHECKSUM MISMATCH <hash>: …` on stderr, distinct from the address check's
  `CORRUPT <hash>: …`; unreadable record → `RECORD UNREADABLE <hash>: …`.
- `--all` still checks every other object and prints
  `checked N recorded objects, C corrupt, U unrecorded, R unreadable`; mismatches are reported,
  never repaired, and this mode writes no record (records come from `cas/verify/sidecar`).
- `gc` and a non-dry `prune` reconcile records after the sweep (dropping swept objects' records) and
  print a `checksum records: …` summary when the store has any; a foreign `.json` file in the record
  directory is skipped, named on stderr and left in place.
- `-backend` accepts `fs` (default) or `packfs`; anything else → usage error (exit 2). An absent flag
  selects the documented default, unvalidated.
- **Bind determinism and the host firewall** (viewer-security §4): `-bind` accepts the loopback
  spellings `127.0.0.1`, `localhost`, `[::1]`; a hostname never reaches the listener (the resolver
  would choose the address family). A loopback bind is pinned to a numeric address before listening
  (`localhost` → `127.0.0.1`); other values pass through.
- A bare `:port`, `0.0.0.0:port`, `[::]:port` or any other non-loopback address is refused without
  `-allow-insecure-bind`; such a bind is what triggers the host firewall prompt (Windows Defender
  Firewall, for one), which the loopback default never does.
- WSL relay prompts name `wslrelay.exe` or `vmmem`, not `cask`; the remedy is the address WSL
  reports, not a flag.
- `put`/`get` stream bytes; the CLI never buffers large objects (performance P-05).
- No secrets in output: the startup token is never logged at any level and never echoed; the
  one-time login hint on **stdout** — interactive stdout, or any run passing `-show-token` — is its
  only display, loopback bind only; `-token-file`/`CASK_VIEWER_TOKEN` supply it unattended.
- The browser launch carries the token deep link under the same rule: the deep link percent-encodes
  the token and the launcher takes the URL as a plain argument, so the token never reaches another
  process's argument vector. Errors name the flag or the file, never the token (viewer-security
  §5.1, §9, §11). `-token-file` MUST name a regular file holding at least 16 characters from
  `A-Z a-z 0-9 - . _ ~`, read under a 4 KiB bound; anything else fails startup (exit 1) naming the
  flag or the file.
- The viewer's token URL signs in only from the viewer's own origin (the URL the browser opens, or a
  same-origin form or link); a cross-site request bearing it → 403, empty body (viewer-security
  §5.1). Prints only for a loopback bind: the session cookie is always `Secure` (viewer-security
  §7), so a non-loopback bind prints the bind and the `https://` expectation instead.
- Std-lib only (`flag` package); documented per coding-guidelines §7.

## 5. Checklist

- [x] Local-only: `-store` mode; no `-algo` flag on store operations; viewer subcommands take
  `-hash-algo` (§1, §2); no remote flags
- [x] `web` starts the embedded viewer per backend-architecture §3; no separate server binary
- [x] `seed-preview` creates idempotent local viewer data with valid envelopes
- [x] Sweeps (`gc`/`prune`/`clean`) hold the store lock; a second refused with the holder's PID
  (exit 1); writers (`put`) and reads never lock
- [x] `gc`/`prune` grace-gated by `--min-age` (default 1h); forced `--min-age 0` warns
- [x] All subcommands map to core operations or the viewer server composition; no new CLI logic
- [x] Hash arguments parsed with `sha256.Parse` (exit 2 on malformed), or the `-hash-algo` the
  command accepts (`verify`, `web`, `seed-preview`)
- [x] `verify -hash-algo` checks a store addressed by `sha512`/`sha512_256`; other operations speak
  the client constant `sha256`
- [x] `-backend fs|packfs` selects the storage backend; every store operation runs over either
- [x] `verify`/`gc`/`prune`/`clean` work over a packed store, or fail with `cas.ErrUnsupported`
  naming the operation and the backend
- [x] `verify -checksums` reads recorded per-object checksums (`operations §6`); a mismatch or an
  unreadable record exits non-zero, record-less objects report unchecked, and those lines differ
  from the address `CORRUPT` line
- [x] `gc`/`prune` reconcile the records they sweep, skip and name a foreign `.json` file instead of
  aborting; a store with no records is unaffected
- [x] Store closed on the write path; `web` requires the `fs` backend
- [x] Plain text by default, `-json` on request; errors on stderr; login notice on stdout
- [x] Exit codes 0/1/2 per §3
- [x] Streaming for large objects; no token leakage
- [x] Startup token never logged; shown once on stdout for a loopback bind only (interactive
  terminal or `-show-token`; `-show-token=false` suppresses), or supplied via
  `-token-file`/`CASK_VIEWER_TOKEN` (viewer-security §5.1, §9, §11)
- [x] A non-loopback bind prints the bind and the `https://` expectation, not a login link, and
  displays no token (viewer-security §7, §11)
- [x] The browser launch is skipped for a non-loopback bind and when `-show-token=false` suppresses
  the display, so the token never reaches an argument vector (viewer-security §11)
