---
title: CLI — go-cask
description: The contract for cmd/cask — a thin command-line client over the cas library (in-process) or the CAS API (remote); subcommands, flags, output format, auth, and exit codes.
version: v1
---

# CLI — go-cask

> The contract for `cmd/cask`: a thin command-line client. It is a wrapper,
> not a second implementation — every operation maps to a core maintenance
> operation (cas-core §4) or a CAS API call (cas-api), so behavior is defined
> once.
>
> Related: `.github/instructions/cas-core.instructions.md` (operations),
> `.github/instructions/cas-api.instructions.md` (remote mode),
> `.github/instructions/consistency.instructions.md` (GC/prune),
> `.github/instructions/versioning.instructions.md` (version output).

---

## 1. Purpose & Modes

`cmd/cask` speaks to the store in one of two modes:

| Mode      | Flag            | What it talks to                              | Auth               |
| --------- | --------------- | --------------------------------------------- | ------------------ |
| local     | `-store <path>` | the library in-process (`FSRawStore`)         | none (filesystem trust) |
| remote    | `-api <url>`    | a CAS API server via the client SDK           | `-token <bearer>`  |

- Exactly one mode is required; `-store` and `-api` are mutually exclusive.
- Remote mode goes through the documented CAS API contract (including rate
  limits and role checks); local mode is always available.
- `-algo <name>` selects the write algorithm (default `sha256`); reads accept
  any registered algorithm (cas-core §4.2).

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
| `version`                     | print the library version and Go version                        |

- Hash arguments are validated with `ParseHash` before use; malformed → usage
  error (exit 2).
- `gc` and `prune` are destructive: `prune` defaults to `--dry-run` and
  `gc` prints the count of deleted objects (consistency §4–§5).
- Remote mode maps directly to the CAS API endpoints (cas-api §5/§6); local
  mode calls the library.

---

## 3. Output & Exit Codes

- **Default output** is plain text: one hash per line for `put`/`list`;
  human-readable summaries for `stats`/`meta`/`verify`/`gc`/`prune`.
- `-json` switches to machine-readable JSON, matching the API shapes
  (`{"hash": "…"}`, `{"total": n, "objects": […]}`).
- Errors go to stderr, never stdout.

| Exit code | Meaning                                      |
| --------- | -------------------------------------------- |
| 0         | success                                      |
| 1         | runtime error (store/API/IO) — message on stderr |
| 2         | usage error (unknown command, bad flags, invalid hash) |

---

## 4. Conventions

- Flags: single-dash long names (`-store`, `-api`, `-token`, `-algo`,
  `-json`, `-o`, `-min-age`, `-dry-run`, `-limit`, `-offset`).
- Streams: `put`/`get` stream bytes; the CLI never buffers large objects
  (performance P-05).
- No secrets in output: tokens are never echoed; errors never include the
  token (viewer-security secret handling).
- The CLI is std-lib only (`flag` package) and documented per
  coding-guidelines §7.

---

## 5. Checklist

- [ ] `-store` and `-api` modes mutually exclusive; `-algo` honored for writes
- [ ] All subcommands map to core operations or CAS API endpoints — no new
      logic in the CLI
- [ ] Hash arguments validated with `ParseHash` (exit 2 on malformed)
- [ ] Output plain text by default, `-json` on request; errors on stderr
- [ ] Exit codes 0/1/2 per §3
- [ ] Streaming for large objects; no token leakage
