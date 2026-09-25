---
type: Guide
title: Scripts — go-cask
description: Local automation for verification, releases, examples, and benchmarks; treated as the canonical repo helper layer for human operators and CI.
version: v9
---

# Scripts — go-cask

This directory holds the repo's operational helper scripts. They are the single place for local preflight checks, release-note generation, example execution, and benchmark baseline capture.

## Script inventory

| Script | Purpose |
|---|---|
| [`verify.sh`](./verify.sh) | Central repo verification gate: formatting, module drift, build, vet, the layer matrix, cross-platform build/vet for windows/amd64 and linux/arm64, security scanning, race/coverage, fuzz smoke, helper-script behaviour, doc integrity, and the website example build plus shipped-package inventory check. Auto-detects a documentation-only change and runs the documentation gate instead (the scope CI applies; `VERIFY_SCOPE=full|docs` overrides). A green run stamps the commit in the shared git dir for the pre-push hook and writes a gate receipt for the commit, which CI can reuse instead of repeating the same suite. |
| [`gate-receipt.sh`](./gate-receipt.sh) | The gate's evidence, made portable. `create` writes the receipt for a green run (commit, tree, merge base, hash of the changed path list, scope, the checks that ran) into the shared git dir; `publish` signs it with the developer's git key as a receipt commit — parent: the gated commit, tree: the gated tree, message: the receipt — and pushes it to `refs/gate/<sha>`, a coordination ref like `refs/lane/<issue>` and never a branch; `verify` is the CI side, and accepts only a receipt signed by a key in [`.github/gate-signers`](../.github/gate-signers) whose parent, tree, ancestor base, recomputed diff hash, recomputed scope and check list all agree; `show` prints one; `suite` prints the checks a full receipt must list. Every refusal is a fallback to the whole gate, never a skipped check. |
| [`test-gate-receipt.sh`](./test-gate-receipt.sh) | Behaviour test for that helper in throwaway repositories with throwaway ssh keys: the receipt's fields, publish's signing and its idempotent second run, the accept path, and every refusal — an untrusted signer, a rewritten payload, a receipt built on another commit, a different tree, a base that is not an ancestor, a diff hash that no longer describes the change, a docs receipt for a Go change, and a missing required check. `verify.sh` runs it. |
| [`pr-lane.sh`](./pr-lane.sh) | The landing lane: one open pull request is one lane. `claim <issue>` takes it with a server-side compare-and-swap on the coordination ref `refs/lane/<issue>` (an atomic create — it succeeds exactly once, so two sessions cannot both claim a lane), `check` reports whether a claim would succeed without claiming, `status [<issue>] [--json]` lists every lane on the remote with the pull request behind it, and `release <issue> [--force]` frees a lane whose PR merged or was closed. The ref points at a tag object naming the claiming branch and worktree and dating the claim; an open PR holds the lane, and a claim with no PR is honoured for `PR_LANE_STALE_MINUTES` (90) before the next claimer takes it over — no heartbeat to renew and no `--force` takeover. |
| [`test-pr-lane.sh`](./test-pr-lane.sh) | Behaviour test for that lane against a stub `gh`: the atomic claim under four simultaneous claimers, the refusal while a PR is open, the claim window, the takeover of an abandoned claim, an unreadable record, and release. `verify.sh` runs it. |
| [`land-lane.sh`](./land-lane.sh) | The local ADVISORY slot: one slot in the shared git dir that keeps two gate runs in ONE clone from overlapping, so a clone does not pay twice for the same tree. It no longer gates a push — the lane is the pull request (`pr-lane.sh`) and `.githooks/pre-push` only reports this slot. `status` / `acquire [--force] <label>` / `renew` / `release`. Staleness is idle time and only `renew` — the holder's own call — moves the deadline; a takeover records the holder it evicted. |
| [`worktree.sh`](./worktree.sh) | Creates/removes a task worktree with its `.git` in the relative form, so both the Windows and the WSL git resolve it (an absolute path makes WSL git walk up to the primary checkout). `add <name> [<branch>]` / `remove <name>` / `lock [<name>...]` / `prune` (refuses — see below) / `list`. Every worktree it creates carries git's `locked` file, which is what stops `git worktree prune` from deleting a registration whose admin `gitdir` is in the other toolchain's path form. |
| [`security.sh`](./security.sh) | Installs the pinned `govulncheck` version and runs the repository security scan. |
| [`docs-only.sh`](./docs-only.sh) | Classifies a Git diff as documentation-only for CI scope selection. |
| [`dep-graph.sh`](./dep-graph.sh) | Generates `docs/design/package-graph.md`, the local package dependency graph as a Mermaid flowchart, from `go list`. It owns that document: the no-argument run rewrites it and moves its frontmatter `version` only when the graph actually changed, and `--check` reports a stale document without writing it. `verify.sh` runs `--check` in both scopes. |
| [`test-dep-graph.sh`](./test-dep-graph.sh) | Regression test for that ownership split: a fresh document at `v1`, an unchanged regeneration as a byte-for-byte no-op, a version bump when the graph changes, and `--check` writing nothing in every case (stubbed `go` in throwaway git repositories). |
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
- `dep-graph.sh` owns `docs/design/package-graph.md` and is its only writer: `--check` regenerates into a scratch directory and compares, so the gate can report a stale graph without any way to overwrite it; `scripts/test-dep-graph.sh` (run by `verify.sh`) enforces that split.
- Keep scripts fail-fast and explicit: `set -euo pipefail` is the default for bash helpers in this repo.
- Prefer repo-root execution. Scripts assume they are launched from the repository root unless a script explicitly documents otherwise.
- `run-examples.sh` runs the examples that terminate on their own (`artifacts`, `bloom`, `files`, `notes`, `pack`), each with the subcommand that completes, and `--list` names them together with the manual `api` example.
- The `api` example is a manual two-process pair, so no script runs it: start the blocking server with `go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080`, then the demo client with `go run ./examples/api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md` in a second terminal (see [`examples/api/README.md`](../examples/api/README.md)).
- `security.sh` installs the pinned `govulncheck` version so local and CI vulnerability scans are reproducible. Update `GOVULNCHECK_VERSION` deliberately.
- CI sets `VERIFY_SKIP_SECURITY=true` because its required `security` job runs
  the same pinned scan separately; local `verify.sh` runs it by default.
- The gate receipt is one contract in three places and they move together: every
  `verify.sh` section marks itself with `mark_check`, `gate-receipt.sh`'s
  `suite_full` list names the marks CI requires, and `ci.yml` asks only for
  `--require-suite full`. A section that is renamed or added without the other two
  makes the receipt unusable and CI runs the whole gate — slower, never weaker.
- Publishing a receipt is signing it: `gate-receipt.sh publish` uses this
  toolchain's git signing configuration, so run it where `gpg.format` and
  `user.signingkey` are set (on this host the Windows toolchain, which is also the
  one that pushes). A clone without signing simply cannot publish, and CI falls
  back to the full gate.
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
./scripts/dep-graph.sh                      # regenerate the package dependency graph
./scripts/dep-graph.sh --check              # report a stale graph; writes nothing
./scripts/pr-lane.sh claim 389              # take the lane for an issue before starting
./scripts/pr-lane.sh status                 # every lane on the remote and its pull request
./scripts/pr-lane.sh release 389            # free the lane once its PR merged
./scripts/gate-receipt.sh show              # the receipt for HEAD, if this clone has one
./scripts/gate-receipt.sh publish           # sign it and push refs/gate/<sha> for CI
```

When a script's behavior changes, update this README, the matching workflow, and any affected docs in the same change.
