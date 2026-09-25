---
type: Specification
title: CLI — go-cask
description: The contract for cmd/cask — the single entry point: a thin command-line client over the cas library, plus the embedded viewer via the web subcommand; subcommands, flags, output format, auth, and exit codes.
version: v36
---

# CLI — go-cask

Contract for `cmd/cask`, the single binary: thin CLI over the cas library plus, via `web`, the embedded viewer — a wrapper, not a second implementation. Every operation maps to a core operation (cas-core §4) or the viewer server composition (backend-architecture §3). The product ships no network JSON API (backend-architecture §1). Related: cas-core, backend-architecture, viewer-design, viewer-security, consistency (GC/prune), versioning (version output).

## 1. Purpose and modes

`cmd/cask` is the only entry point; no separate server binary. Store operations run in-process over the library.

| Mode | Flag | Talks to | Auth |
|---|---|---|---|
| local | `-store <path>` `-backend fs\|packfs` | library in-process over the selected storage backend (`fs` by default) | none (filesystem trust) |

- `-store` is required for store operations. No remote mode. The path must be the store's own directory: both backends reject an empty value, `.`, a filesystem or volume root and a parent-traversal path before creating anything (`fs.ValidateBase`, cas-core §4.4); a store path below the working directory (`-store root/name`) is fine.
- `-backend` selects the storage backend every store operation runs against:
  `fs` (the default, Git-like fan-out loose objects) or `packfs` (a loose tree
  plus append-only pack files). One shared internal constructor opens the
  selected backend and reports its `cas.Capabilities`, so `put`/`get`/`list`/
  `meta`/`stats`/`verify`/`gc`/`prune`/`clean` work over either backend
  (backend-architecture §5). Without the flag the CLI behaves exactly as before.
  The open/refuse matrix is therefore **either backend for every store operation,
  `fs` only for the viewer**: `web` is the single refusal, and its message names
  the operation, the backend and the remedy instead of reporting a bare
  unsupported operation (§2, viewer-design §1).
- The hash algorithm is a **client** constant: `cmd/cask` digests and validates with `cas/hash/sha256` (`sha256.Format` renders the printable `sha256:hexdigest` form; `sha256.Parse` accepts it or bare hex). No **store operation** takes an algorithm flag — the core names no algorithm (cas-core §4.2). Both viewer subcommands take `-hash-algo` (`sha256`, `sha512`, `sha512_256`): a reader must know which algorithm to validate and decode with — `web` for the viewer, `seed-preview` for the preview graph it seeds — and the two MUST agree or the viewer finds no graph (§2, §4).
- `web` is the **viewer shape**: starts the embedded viewer (backend-architecture §3) with the store from `-store` and role=token pairs from `-tokens` (viewer-security). The startup admin token is generated and shown once on **stdout** — an interactive stdout, or any run asking for it with `-show-token` — **only for a loopback bind**, or supplied by the operator with `-token-file`/`CASK_VIEWER_TOKEN`; never logged at any level (§4, viewer-security §5.1, §9, §11). A non-loopback bind prints no login link and displays no token: the session cookie is always `Secure` (viewer-security §7), so the notice names the bind and the `https://` expectation instead. Config file deferred.

## 2. Subcommands

| Command | Behavior |
|---|---|
| `put <file>\|- [-json]` | store bytes (or stdin); prints the hash (`sha256:hexdigest`) |
| `get <hash> [-o <file>]` | retrieve to a file or stdout (no `-o` → stdout) |
| `list [-limit <n>] [-offset <n>] [-type <type[@major]>] [-codec <tag>] [-json]` | list objects (`{total, objects}` shape, `total` being the matches when a filter is set); `-type`/`-codec` filter on the envelope header — a bare type name reads as `@1`, `-codec unspecified` selects the frames that carry no identity — and a value no object carries is an empty result at exit 0; a digest-named file that is not a readable object (a stray file in the store directory) is skipped with a stderr warning instead of failing the command (cas-core §4.4) |
| `meta <hash> [-json]` | metadata of one object (size, type, frame version, codec, algorithm) |
| `stats [-json]` | storage statistics (`N objects, M bytes`) plus a header census: per type, per frame version and per codec (see §3) |
| `verify [-hash-algo <name>] <hash>\|--all [-checksums [-checksum <algo>]]` | integrity check (single object or full scan; `-hash-algo` selects the algorithm the addresses are expressed in; `-checksums` checks the per-object checksum recorded beside each object instead of its address) |
| `gc --min-age <dur> <roots...>` | reclaim objects absent from `<roots...>` AND older than `--min-age` (grace default 1h; `--min-age 0` = immediate, dangerous); `<roots...>` must already be the complete reachable set, not just entry points |
| `prune --min-age <dur> <roots...> [--dry-run]` | age-based retention (dry-run default); same reachable-set contract as `gc` |
| `clean [--min-age <dur>]` | remove orphan `*.tmp` files older than `--min-age` (default 24 h) |
| `seed-preview [-count <n>] [-hash-algo <name>]` | add 500 deterministic, valid envelope objects for local viewer preview; `-count` accepts 1–10000 |
| `web [-store <dir>] [-backend <name>] [-bind <addr>] [-hash-algo <name>] [-tokens r=t,...] [-token-file <path>] [-trusted-proxy <ip\|cidr,...>] [-allow-insecure-bind] [-show-token] [-no-open]` | start the embedded viewer (backend-architecture §3) — what the browser then shows is explained in [`cmd/cask/README.md`](../../cmd/cask/README.md); the startup admin token is **never logged** at any level; a generated token is shown once on **stdout** for a **loopback bind only**, on an interactive stdout or in any run that passes `-show-token`, while `-show-token=false` suppresses the display and an absent flag keeps the terminal heuristic, and an unattended deployment supplies its own with `-token-file <path>` or `CASK_VIEWER_TOKEN` (`-token-file` wins) without it ever being echoed; then opens the default browser unless `-no-open`, the bind is not loopback, or the display is suppressed (the launch hands the token deep link to another process, viewer-security §11); `-backend` accepts `fs` only (the viewer needs the filesystem backend); `-hash-algo` selects `sha256`, `sha512`, or `sha512_256` for digest parsing and verification and is shown in object Metadata → Identity → Algorithm; `-trusted-proxy` lists the reverse proxies whose forwarded client address the login throttle may believe (viewer-security §5.2) — empty, the default, believes none and keys the throttle on the direct peer, an entry is an IP, an `ip:port`, or a CIDR block, and a malformed entry fails startup (exit 1); a loopback `-bind` spelling (`127.0.0.1`, `localhost`, `[::1]`) is pinned to an explicit numeric loopback address before listening (`localhost` → `127.0.0.1`), so the listener, the printed origin and the host firewall's behaviour never depend on the machine's resolver, while `-bind localhost:<port>` remains accepted (viewer-security §4); refuses a non-loopback bind unless `-allow-insecure-bind` — that covers a bare `:port`, `0.0.0.0:port`, and `[::]:port` — and logs a prominent warning when the override is used (viewer-security §4), which also names the host firewall prompt a bind beyond loopback triggers; session cookies are always `Secure` (§7), so such a bind must be reached through a TLS-terminating proxy or no session will hold, and the notice prints the bind and that `https://` expectation instead of a login link or a token that could not log anyone in; config-file support (`-config`) deferred — flags only |
| `version` | print library + Go version |

- Hash arguments parsed with the selected algorithm's parser — the printable `<name>:hexdigest` form or bare hex, `sha256` unless `-hash-algo` names another (below); malformed, or another algorithm's prefix, → usage error (exit 2).
- `gc`/`prune` are destructive, **grace-gated**: they reclaim only objects absent from `<roots...>` AND older than `--min-age` (default 1h), so a concurrent writer's fresh objects survive (cas-core §6). `<roots...>` = the complete reachable set at the byte layer, not entry points: the CLI cannot interpret references (no typed object model), so it never expands a root into what it points to. Pass every digest that must survive, or the sweep deletes anything a root references (cas-core §4.11; `cas.Reachable` for library callers with a typed graph). `prune` defaults to `--dry-run`; `gc` prints the deleted count (consistency §4–§5). A forced sweep (`--min-age 0`) warns, safe only when nothing else writes the store.
- **Store lock:** maintenance sweeps (`gc`/`prune`/`clean`) take the store's exclusive cross-process lock (`.cask.lock` at the store root, holding the PID), so two sweeps never overlap. A second holder → exit 1 naming its PID, and telling the operator to remove a stale lock file when no such process runs. Writers (`put`) and the viewer (`web`) never lock: object writes are cross-process safe by construction, and the grace period protects fresh objects (cas-core §6). Read-only commands (`get`/`list`/`meta`/`stats`/`verify`) never lock. The library has no inter-process locking; this lock only stops sweeps racing.
- Every operation calls the library in-process.
- **Backend-agnostic maintenance.** `verify` runs through `cas.Verify`/
  `cas.VerifyAll`, so a backend with no backend-native `Verify` is checked
  identically; `gc`/`prune` use the backend's native `Prune` when it has one
  (`fs`), the portable `cas.Sweep` otherwise (`packfs`); `clean` uses the
  backend's `cas.Cleaner`. An unsupported operation fails with an error naming
  the operation and the backend and wrapping `cas.ErrUnsupported` (exit 1) —
  never a silent success or a partial sweep.
- The store is opened and closed per command, including on the write path:
  `packfs` keeps its active pack file open for appends, and the CLI releases it
  (and reports a close failure) before the command returns. Nothing buffers a
  pack index in memory waiting for a separate flush.
- `web` requires the `fs` backend: the viewer reads per-object physical
  metadata through the concrete filesystem backend, so `web -backend packfs` is
  refused with the same `cas.ErrUnsupported` error (exit 1) rather than reading
  a directory other than the one `-store` named. The refusal names the operation,
  the backend **and the remedy** — open a loose store (`-backend fs`, or `-store`
  with a loose store's directory) — because creating a packed store with the CLI
  and then being unable to inspect it is the likely support question; a packed
  object has no file of its own to stat (its loose copy and its pack record are
  two different answers to "how large is it, and how old"). The viewer's own
  `-backend` defaults to the global flag (cli.md §1, viewer-design §1).
- `seed-preview` creates valid, deterministically addressed TLV envelopes with
  representative type names, payload sizes, deterministic graph edges and
  alternating root-reachable segments. The frames are the current format
  (`cas.EnvelopeVersion`), carrying the codec tag `preview` — the payload is a
  synthetic byte pattern no shipped codec produced — and they are built by
  `cas.EncodeEnvelope`, the writer `Store.Put` frames through, so a seeded digest
  is the digest the store would have computed for the same bytes (a local copy of
  the layout silently froze at version 1 before go-cask#187, and the addresses of
  seeded objects change with the frame). Each eight-object block holds a Root
  (reachable, no inbound edges), orphans with inbound edges, and a Detached
  orphan entry (unreachable, no inbound edges), so the viewer can demonstrate
  all four reference states. Consecutive objects cycle through zero, one, two
  and three outgoing references, producing varied inbound counts too. It is
  idempotent for a given `-count`: a rerun reports deduplicated objects instead
  of writing copies. `-hash-algo` selects the digest algorithm the graph is
  addressed with and **must match** `cask web -hash-algo`: the viewer recognizes
  the preview graph by re-deriving every ordinal's digest with its own hasher,
  so seeding with one algorithm and reading with another finds no graph at all
  (the viewer shows no references). A missing object inside a block does not
  truncate the graph — a sweep reclaiming a Detached entry drops only that
  object's own edges — and `web` recognizes only this known deterministic
  preview graph and supplies it to the viewer; ordinary stores stay
  reference-free unless their host provides a viewer source.
- Every eighth preview object is written with deliberately tampered bytes that
  do not hash to their own address, so `verify` genuinely fails for them. Those
  ordinals sit inside root-reachable segments, giving the viewer corrupt objects
  that are not orphaned and keeping the two status axes visibly independent. The
  tampering flips one payload byte only, so the envelope type still reads
  correctly.
- Preview objects are deliberately local store data, never repository fixtures or fabricated viewer metadata.
- `web` is the only non-terminating subcommand: runs until signalled (graceful shutdown per backend-architecture §6).

## 3. Output and exit codes

- Default output is plain text: one hash per line for `put`/`list`; human-readable summaries for `stats`/`meta`/`verify`/`gc`/`prune`.
- `-json` switches to machine-readable JSON: `put` → `{"hash": "sha256:hexdigest", "deduplicated": bool}`; `list` → `{"total": n, "objects": [{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n, "type": "…", "version": 2, "codec": "…"}, …]}`; `meta` → `{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n, "type": "…", "version": 2, "codec": "…"}`; `stats` → `{"objects": n, "bytes": m, "unreadable": u, "headerless": h, "types": {"blob@1": n, …}, "versions": {"2": n, …}, "codecs": {"json": n, …}}`. `"algorithm"` is the client's constant, not something the core reports.
- **The header census is one convention across the surfaces.** `type` is the versioned envelope type (`""` when the bytes carry none), `version` is the frame's leading byte (`0` when there is no walkable header — raw bytes written by `put`), and `codec` is the writing codec's identity tag with the literal `unspecified` for a frame that carries none (a version 1 frame, or a version 2 frame whose codec declared no tag): never a blank value, never a guess. In `stats -json` each axis counts only objects whose header was read, so `sum(types) == sum(versions) == sum(codecs) == objects - unreadable - headerless`; a store of enveloped objects therefore sums to its object count exactly. The viewer renders the same three values from the same read (viewer-design §3), so `list -json`, `meta -json`, `stats -json`, the object table and the inspector agree digest by digest.
- Errors go to stderr, never stdout. The viewer's one-time login notice (§1, §2) is deliberate command output, so it goes to stdout.

| Exit | Meaning |
|---|---|
| 0 | success |
| 1 | runtime error (store/IO) — message on stderr |
| 2 | usage error (unknown command, bad flags, invalid hash) |

## 4. Conventions

- Flags: single-dash long names (`-store`, `-backend`, `-json`, `-o`, `-min-age`, `-dry-run`, `-limit`, `-offset`, `-count`, `-bind`, `-hash-algo`, `-checksums`, `-checksum`, `-tokens`, `-token-file`, `-trusted-proxy`, `-allow-insecure-bind`, `-show-token` (viewer: display the generated token's one-time login hint even without a terminal; `-show-token=false` never shows it and never opens the browser; a loopback `-bind` is required either way), `-no-open` (viewer: skip opening the browser), `-config` (deferred)).
- `-hash-algo` names the digest algorithm the **addresses** are expressed in and accepts the shipped hashers — `sha256` (the default), `sha512`, `sha512_256`; an unknown name is a usage error (exit 2). `verify` takes it for the single-object check, `--all`, and `--checksums` alike, so a store addressed by another algorithm is still verifiable: without the flag the CLI's sha256 hasher refuses a wider digest on width before reading a byte, and `--all` aborts rather than collecting. The viewer path (`web`, `seed-preview`) takes the same flag for the same reason, and must agree with how the store was seeded. Every other store operation (`put`/`get`/`list`/`meta`/`stats`/`gc`/`prune`/`clean`) speaks the client constant `sha256` — this flag is the deliberate exception, not a CLI-wide algorithm switch.
- `-checksums` is `verify`'s recorded-checksum mode (operations §6): read the per-object record beside each object rather than recomputing its address — a cheap check over a strongly-addressed store, not an identity check. `-checksum <algo>` names the record to read and accepts the shipped checksums (`crc32` — the default, `adler32`, `crc64`), requires `-checksums` (exit 2 otherwise), and rejects an unknown name as a usage error. A record written by another algorithm is a runtime error naming the mismatch, never corruption. An object with **no** record reports unchecked and does **not** fail the command: only a mismatch exits 1. The mismatch line is `CHECKSUM MISMATCH <hash>: …` on stderr, deliberately distinct from the address check's `CORRUPT <hash>: …`. A mismatch is reported, never repaired, and this mode writes no record: records come from the library decorator (`cas/verify/sidecar`).
- `gc` and a non-dry `prune` reconcile records after their sweep (dropping the record of every object the sweep deleted) and print a `checksum records: …` summary when the store has any; a store without records is unaffected.
- `-backend` accepts `fs` (default) or `packfs`; anything else is a usage error (exit 2). An absent flag is not an unknown one: it selects the documented default, unvalidated.
- **Bind determinism and the host firewall** (viewer-security §4). `-bind` accepts the loopback spellings `127.0.0.1`, `localhost`, and `[::1]`. A hostname is never handed to the listener: the resolver would then choose the address family, and the printed origin could name an address the operator's browser does not resolve. The viewer pins a loopback bind to an explicit numeric address before listening (`localhost` → `127.0.0.1`) and passes every other value through unchanged, so the listener, the printed origin, the browser URL, and the firewall behaviour are the same on every machine. A bare `:port`, `0.0.0.0:port`, `[::]:port`, or any other non-loopback address is refused without `-allow-insecure-bind`: binding beyond loopback is what makes the host firewall (Windows Defender Firewall, for one) prompt to allow network access, and the loopback default never prompts. A viewer started inside WSL and opened from the Windows browser goes through the WSL localhost relay, and any prompt there names the relay (`wslrelay.exe` or `vmmem`), not `cask`; no `-bind` value changes that, so the remedy is the address WSL reports, not a flag.
- `put`/`get` stream bytes; the CLI never buffers large objects (performance P-05).
- No secrets in output: the startup token is never logged at any level and never echoed; the one-time login hint on **stdout** — an interactive stdout, or any run that passes `-show-token` — is its only display and only for a loopback bind, `-token-file`/`CASK_VIEWER_TOKEN` supply it unattended, and the browser launch that carries the token deep link follows the same rule so the token cannot reach another process's argument vector. Errors name the flag or the file, never the token (viewer-security §5.1, §9, §11).
- The viewer's token URL is an acceptance contract, not a display one: it signs in
  only from the viewer's own origin — the URL the browser opens, or a same-origin
  form or link — and a cross-site request bearing it is refused with 403 and an
  empty body (viewer-security §5.1). It prints only for a loopback bind: the
  session cookie is always `Secure` (viewer-security §7), so a non-loopback bind
  prints the bind and the `https://` expectation instead.
- Std-lib only (`flag` package); documented per coding-guidelines §7.

## 5. Checklist

- [x] Local-only: `-store` mode; no `-algo` flag on store operations (the CLI digests with the client's sha256); viewer subcommands take `-hash-algo` (§1, §2); no remote flags
- [x] `web` starts the embedded viewer per backend-architecture §3; no separate server binary; `-no-open` skips the browser launch
- [x] `seed-preview` creates idempotent local viewer data with valid envelopes
- [x] Maintenance sweeps (`gc`/`prune`/`clean`) hold the store lock; a second sweep refused with the holder's PID (exit 1); writers (`put`) and reads never lock
- [x] `gc`/`prune` grace-gated by `--min-age` (default 1h); forced `--min-age 0` warns
- [x] All subcommands map to core operations or the viewer server composition; no new CLI logic
- [x] Hash arguments parsed with `sha256.Parse` (exit 2 on malformed), or with the `-hash-algo` the command accepts (`verify`, `web`, `seed-preview`)
- [x] `verify -hash-algo` checks a store addressed by `sha512`/`sha512_256`; every other store operation speaks the client constant `sha256`
- [x] `-backend fs|packfs` selects the storage backend; every store operation runs over either
- [x] `verify`/`gc`/`prune`/`clean` work over a packed store, or fail with `cas.ErrUnsupported` naming the operation and the backend
- [x] `verify -checksums` reads recorded per-object checksums (`operations §6`) and exits non-zero only on a mismatch; record-less objects report unchecked, and the mismatch line differs from the address `CORRUPT` line
- [x] `gc`/`prune` reconcile the records of the objects they sweep; a store with no records is unaffected
- [x] The store is closed on the write path; `web` requires the `fs` backend
- [x] Plain text by default, `-json` on request; errors on stderr; login notice on stdout
- [x] Exit codes 0/1/2 per §3
- [x] Streaming for large objects; no token leakage
- [x] The startup token is never logged; shown once on stdout for a loopback bind only — an interactive terminal, or any run passing `-show-token`, with `-show-token=false` suppressing it — or supplied via `-token-file`/`CASK_VIEWER_TOKEN` (viewer-security §5.1, §9, §11)
- [x] A non-loopback bind prints the bind and the `https://` expectation, not a login link that could not hold a session, and displays no token (viewer-security §7, §11)
- [x] The browser launch is skipped for a non-loopback bind and when `-show-token=false` suppresses the display, so the token never reaches an argument vector (viewer-security §11)
