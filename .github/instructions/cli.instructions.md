---
title: CLI — go-cask
description: The contract for cmd/cask — the single entry point: a thin command-line client over the cas library, plus the embedded viewer via the web subcommand; subcommands, flags, output format, auth, and exit codes.
version: v5
---

# CLI — go-cask

> The contract for `cmd/cask`: the single binary of the project — a thin
> command-line client over the cas library and, via the `web` subcommand,
> the embedded viewer. It is a wrapper, not a second implementation — every
> operation maps to a core maintenance operation (cas-core §4) or the
> viewer server composition (backend-architecture §3). The product ships
> no network JSON API (backend-architecture §1).
>
> Related: `.github/instructions/cas-core.instructions.md` (operations),
> `.github/instructions/backend-architecture.instructions.md` (the `web`
> viewer server), `.github/instructions/viewer-design.instructions.md` and
> `.github/instructions/viewer-security.instructions.md` (the viewer),
> `.github/instructions/consistency.instructions.md` (GC/prune),
> `.github/instructions/versioning.instructions.md` (version output).

---

## 1. Purpose & Modes

`cmd/cask` is the only entry point — there is no separate server binary.
Store operations speak to the store in-process over the library:

| Mode  | Flag            | What it talks to                              | Auth               |
| ----- | --------------- | --------------------------------------------- | ------------------ |
| local | `-store <path>` | the library in-process (`FSRawStore`)         | none (filesystem trust) |

- `-store` is required for store operations. There is no remote mode — the
  product ships no network JSON API (backend-architecture §1).
- `-algo <name>` selects the write algorithm (default `sha256`); reads accept
  any registered algorithm (cas-core §4.2).
- The `web` subcommand is the **viewer shape**: it starts the embedded
  viewer (backend-architecture §3) with the store from `-store` and
  role=token pairs from `-tokens` (viewer-security); a config file is
  deferred.

---

## 2. Subcommands

| Command                        | Behavior                                                       |
| ------------------------------ | -------------------------------------------------------------- |
| `put <file>\|-`               | store bytes (or stdin); prints the hash                         |
| `get <hash> [-o <file>]`      | retrieve bytes to a file or stdout                              |
| `cat <hash>`                  | alias of `get` to stdout                                        |
| `list [--algo] [--limit] [--offset]` | list objects (`{total, objects}` shape)                 |
| `meta <hash>`                 | metadata + references of one object                             |
| `stats`                       | storage statistics (per-algorithm counts, total size)           |
| `verify <hash>\|--all`        | integrity check (single object or full scan)                    |
| `gc <roots...>`               | mark-and-sweep from the given root hashes                       |
| `prune --min-age <dur> <roots...> [--dry-run]` | age-based retention (dry-run default)             |
| `web [-store <dir>] [-bind <addr>] [-tokens r=t,...] [-allow-insecure-bind]` | start the embedded viewer (backend-architecture §3): prints a one-time startup admin token and refuses a non-loopback bind unless `-allow-insecure-bind` (viewer-security §4); config-file support (`-config`) is deferred — flags only |
| `version`                     | print the library version and Go version                        |

- Hash arguments are validated with `ParseHash` before use; malformed → usage
  error (exit 2).
- `gc` and `prune` are destructive: `prune` defaults to `--dry-run` and
  `gc` prints the count of deleted objects (consistency §4–§5).
- Remote mode is gone with the network surface; every operation calls the
  library directly in-process.
- `web` is the only subcommand that does not terminate: it runs the viewer
  until signalled (graceful shutdown per backend-architecture §6).

---

## 3. Output & Exit Codes

- **Default output** is plain text: one hash per line for `put`/`list`;
  human-readable summaries for `stats`/`meta`/`verify`/`gc`/`prune`.
- `-json` switches to machine-readable JSON
  (`{"hash": "…"}`, `{"total": n, "objects": […]}`).
- Errors go to stderr, never stdout.

| Exit code | Meaning                                      |
| --------- | -------------------------------------------- |
| 0         | success                                      |
| 1         | runtime error (store/IO) — message on stderr |
| 2         | usage error (unknown command, bad flags, invalid hash) |

---

## 4. Conventions

- Flags: single-dash long names (`-store`, `-algo`, `-json`, `-o`,
  `-min-age`, `-dry-run`, `-limit`, `-offset`, `-bind`, `-tokens`,
  `-allow-insecure-bind`, `-config` (deferred)).
- Streams: `put`/`get` stream bytes; the CLI never buffers large objects
  (performance P-05).
- No secrets in output: tokens are never echoed; errors never include the
  token (viewer-security secret handling).
- The CLI is std-lib only (`flag` package) and documented per
  coding-guidelines §7.

---

## 5. Checklist

- [ ] Local-only: `-store` mode; `-algo` honored for writes; no remote flags
- [ ] `web` starts the embedded viewer per backend-architecture §3; no
      separate server binary exists
- [ ] All subcommands map to core operations or the viewer server
      composition — no new logic in the CLI
- [ ] Hash arguments validated with `ParseHash` (exit 2 on malformed)
- [ ] Output plain text by default, `-json` on request; errors on stderr
- [ ] Exit codes 0/1/2 per §3
- [ ] Streaming for large objects; no token leakage
