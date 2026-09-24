---
type: Guide
title: Scripts — go-cask
description: Local automation for verification, releases, examples, and benchmarks; treated as the canonical repo helper layer for human operators and CI.
version: v5
---

# Scripts — go-cask

This directory holds the repo's operational helper scripts. They are the single place for local preflight checks, release-note generation, example execution, and benchmark baseline capture.

## Script inventory

| Script | Purpose |
|---|---|
| [`verify.sh`](./verify.sh) | Central repo verification gate: formatting, module drift, vet, import checks, security scanning, race/coverage, fuzz smoke, helper-script behaviour, doc integrity, and the website example build plus shipped-package inventory check. Auto-detects a documentation-only change and runs the documentation gate instead (the scope CI applies; `VERIFY_SCOPE=full|docs` overrides). A green run stamps the commit in the shared git dir for the pre-push hook. |
| [`land-lane.sh`](./land-lane.sh) | The single-slot landing lock that serializes who may push, so parallel sessions cannot invalidate each other's branch. `status` / `acquire <label>` / `release`; the slot lives in the shared git dir. |
| [`worktree.sh`](./worktree.sh) | Creates/removes a task worktree with its `.git` in the relative form, so both the Windows and the WSL git resolve it (an absolute path makes WSL git walk up to the primary checkout). `add <name> [<branch>]` / `remove <name>` / `lock [<name>...]` / `prune` (refuses — see below) / `list`. Every worktree it creates carries git's `locked` file, which is what stops `git worktree prune` from deleting a registration whose admin `gitdir` is in the other toolchain's path form. |
| [`security.sh`](./security.sh) | Installs the pinned `govulncheck` version and runs the repository security scan. |
| [`docs-only.sh`](./docs-only.sh) | Classifies a Git diff as documentation-only for CI scope selection. |
| [`release.sh`](./release.sh) | Release wrapper that coordinates the consistent release flow from the repo root. |
| [`release-notes.sh`](./release-notes.sh) | Generates GitHub release notes from `CHANGELOG.md` and ensures the standard `Full Changelog:` compare URL is present. |
| [`bench-baseline.sh`](./bench-baseline.sh) | Captures benchmark output and is the only writer of the canonical `benchmarks/data/baseline.txt` reference dump. |
| [`bench-compare.sh`](./bench-compare.sh) | Captures a fresh run and diffs it against a reference with `benchstat`; never writes the canonical baseline. |
| [`test-bench-scripts.sh`](./test-bench-scripts.sh) | Regression test for the benchmark helpers' baseline ownership (stubbed `go`/`benchstat` in throwaway git repositories). |
| [`check-version-fields.sh`](./check-version-fields.sh) | Reports versioned files (frontmatter `version:`) whose change did not move that field. Called by `verify.sh` in both scopes, so a documentation-only branch is covered too. |
| [`test-version-fields.sh`](./test-version-fields.sh) | Behaviour test for `check-version-fields.sh` against a throwaway repository: unbumped, bumped, unversioned, mixed, new and missing paths. |
| [`run-examples.sh`](./run-examples.sh) | Runs the example programs that terminate on their own in one command from the repo root; `--list` also names the manual two-process `api` example. |

## Rules

- Keep local scripts and CI behavior aligned. The workflow should call the same helper logic instead of duplicating commands.
- Run `./scripts/verify.sh` before every commit or release prep pass.
- Keep `CHANGELOG.md` and GitHub release notes synchronized.
- `release.sh --publish` resolves either `gh` or `gh.exe`, so it works in
  POSIX shells on Windows as well as native Linux and macOS shells, and pipes
  release notes instead of passing a shell-specific temporary-file path.
- Publish only from a clean `main` checkout: the release tag must exist, point
  at `HEAD`, and be reachable from `main`.
- Keep package-scoped fuzz corpora reviewed and checked in when a fuzz target changes, rather than letting random output become the only seed set.
- Keep benchmark history in dated archive files under `benchmarks/data/archive/` and keep `benchmarks/data/baseline.txt` as the latest canonical comparison point.
- `bench-baseline.sh` owns `benchmarks/data/baseline.txt`: only a deliberate run (without `--capture-only`) rewrites it, and it archives the previous dump first. `bench-compare.sh` captures through `bench-baseline.sh --capture-only`, so comparing can never replace the reference it compares against; `scripts/test-bench-scripts.sh` (run by `verify.sh`) enforces that split.
- Keep scripts fail-fast and explicit: `set -euo pipefail` is the default for bash helpers in this repo.
- Prefer repo-root execution. Scripts assume they are launched from the repository root unless a script explicitly documents otherwise.
- `run-examples.sh` runs the examples that terminate on their own (`artifacts`, `bloom`, `files`, `notes`, `pack`), each with the subcommand that completes, and `--list` names them together with the manual `api` example.
- The `api` example is a manual two-process pair, so no script runs it: start the blocking server with `go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080`, then the demo client with `go run ./examples/api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md` in a second terminal (see [`examples/api/README.md`](../examples/api/README.md)).
- `security.sh` installs the pinned `govulncheck` version so local and CI vulnerability scans are reproducible. Update `GOVULNCHECK_VERSION` deliberately.
- CI sets `VERIFY_SKIP_SECURITY=true` because its required `security` job runs
  the same pinned scan separately; local `verify.sh` runs it by default.
- The race/coverage gate requires CGO. On Windows, use a Go-supported MinGW-w64 or LLVM compiler; some Go/MSVC combinations reject race-build flags.
- Documentation CI installs [requirements-docs.lock](../requirements-docs.lock) with
  hash verification. Regenerate it with the command recorded in
  [requirements-docs.txt](../requirements-docs.txt) after updating a direct
  documentation dependency.

## Typical commands

```bash
./scripts/verify.sh
./scripts/release-notes.sh v1.4.5 v1.4.4
./scripts/run-examples.sh
./scripts/run-examples.sh --list            # automated examples plus the manual api pair
./scripts/bench-baseline.sh                 # refresh the canonical reference dump (archives the old one)
./scripts/bench-compare.sh                  # diff a fresh run against the committed reference
./scripts/test-bench-scripts.sh             # regression test for the two helpers above
```

When a script's behavior changes, update this README, the matching workflow, and any affected docs in the same change.
