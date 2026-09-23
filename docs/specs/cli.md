---
type: Specification
title: CLI — go-cask
description: The contract for cmd/cask — the single entry point: a thin command-line client over the cas library, plus the embedded viewer via the web subcommand; subcommands, flags, output format, auth, and exit codes.
version: v25
---

# CLI — go-cask

The contract for `cmd/cask`, the single binary: a thin CLI over the cas library and, via `web`, the embedded viewer. It is a wrapper, not a second implementation — every operation maps to a core operation (cas-core §4) or the viewer server composition (backend-architecture §3). The product ships no network JSON API (backend-architecture §1). Related: cas-core, backend-architecture, viewer-design, viewer-security, consistency (GC/prune), versioning (version output).

## 1. Purpose and modes

`cmd/cask` is the only entry point — no separate server binary. Store operations talk to the store in-process over the library.

| Mode | Flag | Talks to | Auth |
|---|---|---|---|
| local | `-store <path>` `-backend fs\|packfs` | library in-process over the selected storage backend (`fs` by default) | none (filesystem trust) |

- `-store` is required for store operations. No remote mode. The path must name the store's own directory: both backends reject an empty value, `.`, a filesystem or volume root and a parent-traversal path before creating anything (`fs.ValidateBase`, cas-core §4.4); a store path below the working directory (`-store root/name`) is fine.
- `-backend` selects the storage backend every store operation runs against:
  `fs` (the default, Git-like fan-out loose objects) or `packfs` (a loose tree
  plus append-only pack files). One shared internal constructor opens the
  selected backend and reports its `cas.Capabilities`, so `put`/`get`/`list`/
  `meta`/`stats`/`verify`/`gc`/`prune`/`clean` work over either backend
  (backend-architecture §5). Without the flag the CLI behaves exactly as before.
- The hash algorithm is a **client** constant: `cmd/cask` digests and validates with `cas/hash/sha256` (`sha256.Format` renders the printable `sha256:hexdigest` form; `sha256.Parse` accepts it or bare hex). There is no `-algo` flag — the core names no algorithm (cas-core §4.2).
- `web` is the **viewer shape**: starts the embedded viewer (backend-architecture §3) with the store from `-store` and role=token pairs from `-tokens` (viewer-security). The startup admin token is generated and shown once on stderr **only when stderr is an interactive terminal**, or supplied by the operator with `-token-file`/`CASK_VIEWER_TOKEN`; it is never logged at any level (§4, viewer-security §5.1, §9, §11). A config file is deferred.

## 2. Subcommands

| Command | Behavior |
|---|---|
| `put <file>\|- [-json]` | store bytes (or stdin); prints the hash (`sha256:hexdigest`) |
| `get <hash> [-o <file>]` | retrieve to a file or stdout (no `-o` → stdout) |
| `list [-limit <n>] [-offset <n>] [-json]` | list objects (`{total, objects}` shape); a digest-named file that is not a readable object (a stray file in the store directory) is skipped with a stderr warning instead of failing the command (cas-core §4.4) |
| `meta <hash> [-json]` | metadata of one object (size, type, algorithm) |
| `stats` | storage statistics (`N objects, M bytes`) |
| `verify <hash>\|--all` | integrity check (single object or full scan) |
| `gc --min-age <dur> <roots...>` | reclaim objects absent from `<roots...>` AND older than `--min-age` (grace default 1h; `--min-age 0` = immediate, dangerous); `<roots...>` must already be the complete reachable set, not just entry points |
| `prune --min-age <dur> <roots...> [--dry-run]` | age-based retention (dry-run default); same reachable-set contract as `gc` |
| `clean [--min-age <dur>]` | remove orphan `*.tmp` files older than `--min-age` (default 24 h) |
| `seed-preview [-count <n>] [-hash-algo <name>]` | add 500 deterministic, valid envelope objects for local viewer preview; `-count` accepts 1–10000 |
| `web [-store <dir>] [-backend <name>] [-bind <addr>] [-hash-algo <name>] [-tokens r=t,...] [-token-file <path>] [-trusted-proxy <ip\|cidr,...>] [-allow-insecure-bind] [-no-open]` | start the embedded viewer (backend-architecture §3): the startup admin token is **never logged** at any level; a generated token is shown once on stderr only when stderr is an interactive terminal, and an unattended deployment supplies its own with `-token-file <path>` or `CASK_VIEWER_TOKEN` (`-token-file` wins) without it ever being echoed; then opens the default browser unless `-no-open`; `-backend` accepts `fs` only (the viewer needs the filesystem backend); `-hash-algo` selects `sha256`, `sha512`, or `sha512_256` for digest parsing and verification and is shown in object Metadata → Identity → Algorithm; `-trusted-proxy` lists the reverse proxies whose forwarded client address the login throttle may believe (viewer-security §5.2) — empty, the default, believes none and keys the throttle on the direct peer, an entry is an IP, an `ip:port`, or a CIDR block, and a malformed entry fails startup (exit 1); refuses a non-loopback bind unless `-allow-insecure-bind`, and logs a prominent warning when the override is used (viewer-security §4) — session cookies are always `Secure` (§7), so such a bind must be reached through a TLS-terminating proxy or no session will hold; config-file support (`-config`) deferred — flags only |
| `version` | print library + Go version |

- Hash arguments are parsed with `sha256.Parse` (printable `sha256:hexdigest` or bare hex) before use; malformed → usage error (exit 2).
- `gc`/`prune` are destructive and **grace-gated**: they reclaim only objects absent from `<roots...>` AND older than `--min-age` (default 1h), so a concurrent writer's fresh objects survive (cas-core §6). `<roots...>` is the complete reachable set at the byte layer, not just entry points — the CLI cannot interpret references (it has no typed object model), so it never expands a root into what it points to; pass every digest that must survive, or the sweep deletes anything a root references (cas-core §4.11, `cas.Reachable` for library callers that do have a typed graph to expand first). `prune` defaults to `--dry-run`; `gc` prints the count deleted (consistency §4–§5). A forced sweep (`--min-age 0`) prints a warning and is safe only when no other process writes the store.
- **Store lock:** maintenance sweeps (`gc`/`prune`/`clean`) take the store's exclusive cross-process lock (a `.cask.lock` file at the store root holding the PID) so two sweeps never overlap. A second holder → exit 1 naming the holder's PID (and telling the operator to remove a stale lock file when no such process runs). Writers (`put`) and the viewer (`web`) never lock — object writes are cross-process safe by construction and the grace period protects fresh objects (cas-core §6). Read-only commands (`get`/`list`/`meta`/`stats`/`verify`) never lock. The library has no inter-process locking; this lock only keeps maintenance sweeps from racing.
- Every operation calls the library in-process.
- **Backend-agnostic maintenance.** `verify` runs through `cas.Verify`/
  `cas.VerifyAll`, so a backend with no backend-native `Verify` is checked
  identically; `gc`/`prune` use the backend's native `Prune` when it has one
  (`fs`) and the portable `cas.Sweep` otherwise (`packfs`); `clean` uses the
  backend's `cas.Cleaner`. An operation the selected backend cannot perform
  fails with an error naming the operation and the backend and wrapping
  `cas.ErrUnsupported` (exit 1) — never a silent success or a partial sweep.
- The store is opened and closed per command, including on the path that
  writes: `packfs` keeps its active pack file open for appends, and the CLI
  releases it (and reports a close failure) before the command returns. Nothing
  buffers a pack index in memory waiting for a separate flush.
- `web` requires the `fs` backend: the viewer reads per-object physical
  metadata through the concrete filesystem backend, so `web -backend packfs` is
  refused with the same `cas.ErrUnsupported` error (exit 1) instead of reading
  a different directory than `-store` named. The viewer's own `-backend`
  defaults to the global flag (cli.md §1).
- `seed-preview` creates valid, deterministically addressed TLV envelopes with
  representative type names, payload sizes, deterministic graph edges, and
  alternating root-reachable graph segments. Each eight-object graph block
  includes a Root (reachable, no inbound edges), orphans with inbound
  edges, and a Detached orphan entry (unreachable, no inbound edges), so the
  viewer can demonstrate all four reference states. Consecutive objects cycle
  through zero, one, two, and three outgoing references, producing varied
  inbound counts too. It is idempotent for a given `-count`: rerunning
  reports deduplicated objects instead of writing copies. `-hash-algo` selects
  the digest algorithm the graph is addressed with and **must match**
  `cask web -hash-algo`: the viewer recognizes the preview graph by re-deriving
  every ordinal's digest with its own hasher, so seeding with one algorithm and
  reading with another finds no graph at all (the viewer shows no references).
  A missing object inside a block does not truncate the graph — a sweep that
  reclaims a Detached entry only drops that object's own edges — and `web`
  recognizes
  only this known deterministic preview graph and supplies it to the viewer;
  ordinary stores remain reference-free unless their host provides a viewer
  source.
- Every eighth preview object is written with deliberately tampered bytes that
  do not hash to their own address, so `verify` genuinely fails for them. Those
  ordinals sit inside root-reachable segments, giving the viewer corrupt
  objects that are not orphaned and keeping the two status axes visibly
  independent. The tampering flips a payload byte only, so the envelope type
  still reads correctly.
- Preview objects are deliberately local store data, never repository fixtures
  or fabricated viewer metadata.
- `web` is the only non-terminating subcommand: it runs until signalled (graceful shutdown per backend-architecture §6).

## 3. Output and exit codes

- Default output is plain text: one hash per line for `put`/`list`; human-readable summaries for `stats`/`meta`/`verify`/`gc`/`prune`.
- `-json` switches to machine-readable JSON: `put` → `{"hash": "sha256:hexdigest", "deduplicated": bool}`; `list` → `{"total": n, "objects": [{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n}, …]}`; `meta` → `{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n, "type": "…"}`. `"algorithm"` is the client's constant, not something the core reports.
- Errors go to stderr, never stdout.

| Exit | Meaning |
|---|---|
| 0 | success |
| 1 | runtime error (store/IO) — message on stderr |
| 2 | usage error (unknown command, bad flags, invalid hash) |

## 4. Conventions

- Flags: single-dash long names (`-store`, `-backend`, `-json`, `-o`, `-min-age`, `-dry-run`, `-limit`, `-offset`, `-count`, `-bind`, `-hash-algo`, `-tokens`, `-token-file`, `-trusted-proxy`, `-allow-insecure-bind`, `-no-open` (viewer: skip opening the browser), `-config` (deferred)).
- `-backend` accepts `fs` (default) or `packfs`; anything else is a usage error (exit 2). An absent flag is not the same as an unknown one: it selects the documented default without passing through validation.
- `put`/`get` stream bytes; the CLI never buffers large objects (performance P-05).
- No secrets in output: the startup token is never logged at any level and never echoed; the one place it is displayed is the one-time interactive-terminal login hint, and `-token-file`/`CASK_VIEWER_TOKEN` supply it unattended. Errors name the flag or the file, never the token (viewer-security §5.1, §9, §11).
- Std-lib only (`flag` package); documented per coding-guidelines §7.

## 5. Checklist

- [x] Local-only: `-store` mode; no `-algo` flag (the CLI digests with the client's sha256); no remote flags
- [x] `web` starts the embedded viewer per backend-architecture §3; no separate server binary; `-no-open` skips the browser launch
- [x] `seed-preview` creates idempotent local viewer data with valid envelopes
- [x] Maintenance sweeps (`gc`/`prune`/`clean`) hold the store lock; a second sweep refused with the holder's PID (exit 1); writers (`put`) and reads never lock
- [x] `gc`/`prune` grace-gated by `--min-age` (default 1h); forced `--min-age 0` warns
- [x] All subcommands map to core operations or the viewer server composition — no new CLI logic
- [x] Hash arguments parsed with `sha256.Parse` (exit 2 on malformed)
- [x] `-backend fs|packfs` selects the storage backend; every store operation runs over either
- [x] `verify`/`gc`/`prune`/`clean` work over a packed store, or fail with `cas.ErrUnsupported` naming the operation and the backend
- [x] The store is closed on the write path; `web` requires the `fs` backend
- [x] Plain text by default, `-json` on request; errors on stderr
- [x] Exit codes 0/1/2 per §3
- [x] Streaming for large objects; no token leakage
- [x] The startup token is never logged at any level; shown once on an interactive terminal, or supplied via `-token-file`/`CASK_VIEWER_TOKEN` (viewer-security §5.1, §9, §11)
