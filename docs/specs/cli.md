---
type: Specification
title: CLI — go-cask
description: The contract for cmd/cask — the single entry point: a thin command-line client over the cas library, plus the embedded viewer via the web subcommand; subcommands, flags, output format, auth, and exit codes.
version: v14
---

# CLI — go-cask

The contract for `cmd/cask`, the single binary: a thin CLI over the cas library and, via `web`, the embedded viewer. It is a wrapper, not a second implementation — every operation maps to a core operation (cas-core §4) or the viewer server composition (backend-architecture §3). The product ships no network JSON API (backend-architecture §1). Related: cas-core, backend-architecture, viewer-design, viewer-security, consistency (GC/prune), versioning (version output).

## 1. Purpose & modes

`cmd/cask` is the only entry point — no separate server binary. Store operations talk to the store in-process over the library.

| Mode | Flag | Talks to | Auth |
|---|---|---|---|
| local | `-store <path>` | library in-process over the filesystem backend (`fs`) | none (filesystem trust) |

- `-store` is required for store operations. No remote mode.
- The hash algorithm is a **client** constant: `cmd/cask` digests and validates with `cas/hash/sha256` (`sha256.Format` renders the printable `sha256:hexdigest` form; `sha256.Parse` accepts it or bare hex). There is no `-algo` flag — the core names no algorithm (cas-core §4.2).
- `web` is the **viewer shape**: starts the embedded viewer (backend-architecture §3) with the store from `-store` and role=token pairs from `-tokens` (viewer-security). A config file is deferred.

## 2. Subcommands

| Command | Behavior |
|---|---|
| `put <file>\|- [-json]` | store bytes (or stdin); prints the hash (`sha256:hexdigest`) |
| `get <hash> [-o <file>]` | retrieve to a file or stdout (no `-o` → stdout) |
| `list [-limit <n>] [-offset <n>] [-json]` | list objects (`{total, objects}` shape) |
| `meta <hash> [-json]` | metadata of one object (size, type, algorithm) |
| `stats` | storage statistics (`N objects, M bytes`) |
| `verify <hash>\|--all` | integrity check (single object or full scan) |
| `gc --min-age <dur> <roots...>` | reclaim objects not reachable from roots AND older than `--min-age` (grace default 1h; `--min-age 0` = immediate, dangerous) |
| `prune --min-age <dur> <roots...> [--dry-run]` | age-based retention (dry-run default) |
| `clean [--min-age <dur>]` | remove orphan `*.tmp` files older than `--min-age` (default 24 h) |
| `web [-store <dir>] [-bind <addr>] [-tokens r=t,...] [-allow-insecure-bind]` | start the embedded viewer (backend-architecture §3): prints a one-time startup admin token; refuses a non-loopback bind unless `-allow-insecure-bind` (viewer-security §4); config-file support (`-config`) deferred — flags only |
| `version` | print library + Go version |

- Hash arguments are parsed with `sha256.Parse` (printable `sha256:hexdigest` or bare hex) before use; malformed → usage error (exit 2).
- `gc`/`prune` are destructive and **grace-gated**: they reclaim only objects unreachable from roots AND older than `--min-age` (default 1h), so a concurrent writer's fresh objects survive (cas-core §6). `prune` defaults to `--dry-run`; `gc` prints the count deleted (consistency §4–§5). A forced sweep (`--min-age 0`) prints a warning and is safe only when no other process writes the store.
- **Store lock:** maintenance sweeps (`gc`/`prune`/`clean`) take the store's exclusive cross-process lock (a `.cask.lock` file at the store root holding the PID) so two sweeps never overlap. A second holder → exit 1 naming the holder's PID (and telling the operator to remove a stale lock file when no such process runs). Writers (`put`) and the viewer (`web`) never lock — object writes are cross-process safe by construction and the grace period protects fresh objects (cas-core §6). Read-only commands (`get`/`list`/`meta`/`stats`/`verify`) never lock. The library has no inter-process locking; this lock only keeps maintenance sweeps from racing.
- Every operation calls the library in-process.
- `web` is the only non-terminating subcommand: it runs until signalled (graceful shutdown per backend-architecture §6).

## 3. Output & exit codes

- Default output is plain text: one hash per line for `put`/`list`; human-readable summaries for `stats`/`meta`/`verify`/`gc`/`prune`.
- `-json` switches to machine-readable JSON: `put` → `{"hash": "sha256:hexdigest", "deduplicated": bool}`; `list` → `{"total": n, "objects": [{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n}, …]}`; `meta` → `{"hash": "sha256:hexdigest", "algorithm": "sha256", "size": n, "type": "…"}`. `"algorithm"` is the client's constant, not something the core reports.
- Errors go to stderr, never stdout.

| Exit | Meaning |
|---|---|
| 0 | success |
| 1 | runtime error (store/IO) — message on stderr |
| 2 | usage error (unknown command, bad flags, invalid hash) |

## 4. Conventions

- Flags: single-dash long names (`-store`, `-json`, `-o`, `-min-age`, `-dry-run`, `-limit`, `-offset`, `-bind`, `-tokens`, `-allow-insecure-bind`, `-config` (deferred)).
- `put`/`get` stream bytes; the CLI never buffers large objects (performance P-05).
- No secrets in output: tokens are never echoed; errors never include the token.
- Std-lib only (`flag` package); documented per coding-guidelines §7.

## 5. Checklist

- [x] Local-only: `-store` mode; no `-algo` flag (the CLI digests with the client's sha256); no remote flags
- [x] `web` starts the embedded viewer per backend-architecture §3; no separate server binary
- [x] Maintenance sweeps (`gc`/`prune`/`clean`) hold the store lock; a second sweep refused with the holder's PID (exit 1); writers (`put`) and reads never lock
- [x] `gc`/`prune` grace-gated by `--min-age` (default 1h); forced `--min-age 0` warns
- [x] All subcommands map to core operations or the viewer server composition — no new CLI logic
- [x] Hash arguments parsed with `sha256.Parse` (exit 2 on malformed)
- [x] Plain text by default, `-json` on request; errors on stderr
- [x] Exit codes 0/1/2 per §3
- [x] Streaming for large objects; no token leakage
