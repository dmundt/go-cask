---
type: Guide
title: Scripts — go-cask
description: The repo's entry points — the buildtool launcher, the toolchain resolution it shares with the hooks, and the gate — with every rule they run living in Go under internal/build and cmd/buildtool.
version: v10
---

# Scripts — go-cask

This directory holds the repo's entry points and the tooling that is about *landing* work
rather than building it. The decisions those entry points run live in Go — the rules in
[`internal/build/`](../internal/build/README.md), the commands in
[`cmd/buildtool`](../cmd/buildtool/README.md) — so what is left here is small, and each
script is a shim over a command wherever it can be.

## Name

This directory keeps the name `scripts/`; it is deliberately not called `build/`:

- the build *is* [`internal/build/`](../internal/build/README.md) — the engine module,
  go-cask's tables for it, and the archived shells each Go port replaced. A second tree
  called `build/` beside it would name something else and be read as the same thing;
- what lives here is entry points, not build logic: `buildtool.sh` finds the toolchain and
  starts the tool, `verify.sh` is the gate's name for one of its subcommands, the landing
  lane is `go run ./cmd/buildtool pr-lane`, and the task workspaces are
  `go run ./cmd/buildtool worktree`. A directory named `build/` would misname most of that,
  and the build it would claim to be is already `internal/build/`.

## Script inventory

| Script | Purpose |
|---|---|
| [`buildtool.sh`](./buildtool.sh) | The build tool's one entry point: resolve the toolchain (Go may be absent from a Git Bash or WSL shell), run `go run ./cmd/buildtool` from the checkout the script belongs to, and pass the arguments through. Every purpose is a subcommand — the gate (`verify`), the task worktrees, the landing lane, the security scan, the examples, the benchmarks, the release notes — so this file holds no rule of its own. |
| [`toolchain.sh`](./toolchain.sh) | The resolution `buildtool.sh`, `verify.sh` and the pre-push hook share, *sourced* rather than executed because PATH can only change in the caller's shell, and POSIX `sh` on purpose because a git hook runs under `sh`. It sets `PATH` and `CGO_ENABLED` and nothing else. |
| [`verify.sh`](./verify.sh) | The repository's verification gate, and nothing else: it starts `buildtool.sh verify` and passes the arguments through. Every step it used to hold in bash is now `go run ./cmd/buildtool verify` — formatting, module drift, build, vet, the engine module's own suite, the layer matrix, the codec guards, the security scan, the coverage tiers, the race suite, the fuzz smoke and the documentation steps — because a step list written in shell is executed everywhere and covered by no test, while `internal/build/core/verify` and the engine's other packages are covered by ordinary ones. It keeps the name because CI, `.githooks/pre-push` and the specification set call the gate by it. |
| [`gate-receipt.sh`](./gate-receipt.sh) | The gate's evidence made portable, so CI can reuse a green local run instead of repeating the same suite. `create` writes the receipt for a green run — commit, tree, merge base, a hash of the changed path list, the scope, and the checks that ran — into the shared git dir; `publish` signs it as a receipt commit (parent: the gated commit, tree: the gated tree, message: the receipt) and pushes it to `refs/gate/<sha>`, a coordination ref like `refs/lane/<issue>` and never a branch; `verify` is the CI side, and accepts only a receipt signed by a key in [`.github/gate-signers`](../.github/gate-signers) whose parent, tree, ancestor base, recomputed diff hash, recomputed scope and check list all agree; `show` prints one; `suite` prints the checks a full receipt must list. Every refusal is a fallback to the whole gate, never a skipped check. The gate calls `create`, and `.githooks/pre-push` publishes it best effort. `verify` recomputes the change's scope rather than trusting the receipt, and asks `go run ./cmd/buildtool scope` for it — the same command the gate and CI use — so the classification keeps one owner. |
| [`test-gate-receipt.sh`](./test-gate-receipt.sh) | Behaviour test for that helper in throwaway repositories with throwaway signing keys: the receipt's fields, publish's signing and its idempotent second run, the accept path, and every refusal. The gate runs it as its `helper script behaviour` step. |

## Parity with Go

Audit: every remaining `.sh` is referenced (none orphaned). Three are launchers that
*cannot* be Go — a shell has to find `go` before Go can run, and `PATH` can only change in
the caller's shell. One owns a rule that has not moved yet, the gate receipt, and the last
is that helper's behaviour test. Nothing here is a second owner of a Go rule.

| Script | Referenced by | Parity | Remaining |
|---|---|---|---|
| `toolchain.sh` | `buildtool.sh`, `verify.sh` | n/a — exemption | Nothing: a shell is required to find `go` before Go can run. 49 lines, sourced, POSIX `sh`. |
| `buildtool.sh` | `.githooks/pre-push`, docs | n/a — exemption | Nothing: it exists to resolve the toolchain and pass through. 20 lines. |
| `verify.sh` | CI, the pre-push message, the specification set | 100% | Nothing: it resolves the toolchain and starts `buildtool.sh verify`. The step list it held is `go run ./cmd/buildtool verify`; the decisions behind it are `internal/build/core/verify`'s, and go-cask's answer to each is `internal/build/policy`'s gate table. |
| `gate-receipt.sh` | CI's `verify` job, `.githooks/pre-push`, the gate | 0% | All of it: the receipt's format and its `create`/`publish`/`verify`/`show`/`suite` verbs → an engine package plus a `gate-receipt` subcommand of `cmd/buildtool`. Nothing is unrouted — the gate and the hook call the script — but the rule has not moved yet. |
| `test-gate-receipt.sh` | the gate's `helper script behaviour` step | 0% | Its cases become Go tests beside that port, so the throwaway-key harness retires with it. |

Archived scripts (`internal/build/shell/`, 11 files): checked — every copy carries an
`ARCHIVED —` header naming its replacement, every replacement exists, and each has a row in
[`shell/README.md`](../internal/build/shell/README.md). No copy is orphaned, so nothing was
deleted; an unreferenced script with no replacement would be the only deletion candidate.

Task worktrees are `go run ./cmd/buildtool worktree {add,remove,lock,list}`: the command
writes the worktree's `.git` in the relative form both toolchains resolve, locks the
registration against `git worktree prune`, and refuses `prune` outright. The rules are
`internal/build/core/worktree`'s.

The landing layer is two layers that must not be confused, and both are Go now.
The LANE is `go run ./cmd/buildtool pr-lane`: one open pull request is one lane,
claimed with a server-side compare-and-swap on `refs/lane/<NNN>`, with what the ref
means and what a claim may do with it in `internal/build/core/claim`. The local
ADVISORY slot — one
slot in the shared git dir that keeps two gate runs in one clone from overlapping
— is `go run ./cmd/buildtool land-lane`, with the records and the decisions in
`internal/build/core/lane`; the gate stamp the pre-push hook reads is
`internal/build/core/gate`, and `.githooks/pre-push` is a shim over
`go run ./cmd/buildtool pre-push`.

`verify.sh` also calls the build decisions that are not shell: the change-set
classification (`internal/build/changes` over `internal/build/policy`'s rules), the
dependency-layer matrix (`internal/build/layers`), the coverage tiers and thresholds
(`internal/build/coverage`), the Markdown integrity rules (`internal/build/docs`), the
site's Go fences, inventory tables and footer (`internal/build/website`), the benchmark
capture decisions (`internal/build/bench`), the example table (`internal/build/examples`),
the pinned toolchain (`internal/build/toolchain`), the landing lane
(`internal/build/claim`) and the codec
guards plus module-graph check (`internal/build/deps`). Each is a package with its own
tests, reached through `go run ./cmd/buildtool`, which exposes one subcommand per
decision:

| Subcommand | Decides |
|---|---|
| `verify` | the gate itself: which steps a run covers, in what order, and whether it may be recorded. The scope, the concurrency and the escape hatches are `internal/build/core/verify`'s; the entry points, the variables and the smoke-fuzz set are `internal/build/policy`'s; the record is `internal/build/core/gate`'s |
| `scope` | which of CI's jobs a change set can affect, and whether the gate may run the documentation scope |
| `layer-matrix` | every package's imports against AGENTS.md's layer table |
| `coverage-tier` | that every `cas/` package carries a tier or a written exemption; `--list` prints the gate's measurement table |
| `coverage-check` | the thresholds, reading one threshold/package/measurement line per gated package from stdin — the gate collects the measurements, this decides |
| `markdown-integrity` | every tracked `.md`: raw HTML, forbidden fences, dead links, the CHANGELOG structure, mermaid balance |
| `website-examples` | that each Go fence on the site is a complete unit, that the inventory tables match the tree, and that the materialized set builds and vets |
| `website-footer` | that the published footer is the pinned one-line contract and that the site hook's self-test still renders it |
| `security` | that the vulnerability scan runs with the pinned `govulncheck`, installing it when the one on PATH is not that release |
| `bench-baseline` | that one deliberate run owns the committed benchmark reference dump, archiving the previous one first |
| `bench-compare` | that a benchmark comparison chooses its baseline before capturing and never writes the reference |
| `run-examples` | which example programs a runner executes, with which arguments and store, and which one it must never run |
| `land-lane` | the local advisory slot: `status`/`whoami`/`acquire`/`renew`/`release`, its idle-time staleness rule, and the takeover record an eviction leaves |
| `pr-lane` | the server-side lane: `claim`/`check`/`status`/`release`/`whoami`, the ref that is the compare-and-swap, the pull request that is the lease, and the claim window |
| `pre-push` | the mechanical landing rule a push must satisfy, and the advisory-slot note |
| `codec-guards` | that `gitlike` and `cas/pack` do not reach the codec layer transitively |
| `module-graph` | that `go list -m` names this module as the main one |

The gate executes them; it does not restate them.

## Rules

- Keep local scripts and CI behavior aligned. The workflow should call the same helper logic instead of duplicating commands.
- `verify.sh` is a permanent thin shim, and it is now literally one: it resolves the toolchain and starts `buildtool.sh verify`. Do not add a pattern list, a threshold table, a `case` matrix or a step to it — the step list is `cmd/buildtool verify`'s `gateSteps`, and a decision behind a step belongs in `internal/build/core` with a test. The rule and its reasoning are in [`AGENT.md`](./AGENT.md) ("`verify.sh` stays forever").
- `verify.sh` takes fast-turnaround options, and a run that uses one is not a verified run. `VERIFY_SKIP_TESTS`, `VERIFY_SKIP_COVERAGE`, `VERIFY_SKIP_FUZZ` and `VERIFY_SKIP_SECURITY` drop one step each; `VERIFY_FAST=true` drops all four. The run prints what it skipped and writes **no** gate stamp, so `.githooks/pre-push` still refuses to push that commit — the options save time on a working tree, never on a landing. Only the race suite (~79s) and the un-raced suite (~33s) are worth dropping; the rest are seconds.
- No shell here owns a rule any more, so there is no helper-behaviour step left to skip: every helper's rule carries an ordinary Go test instead, and the gate reaches those through the race suite. `internal/build/core/depgraph` pins the committed graph byte-for-byte, `internal/build/core/versioning` pins the version-field decision, `cmd/buildtool/bench_test.go` pins the benchmark helpers' file ownership, `cmd/buildtool/prlane_test.go` pins the pull-request lane against a fake remote, and `cmd/buildtool/landlane_test.go` with `internal/build/core/gate` pin the advisory slot and the gate stamp.
- A one-command delegation is not a violation of that rule, and the footer is the reference case. `website/macros.py --selftest` is one call whose whole rule — the pinned rendered line, the zone label, the guessed date — lives in `website/macros.py`, and the gate reaches it through `go run ./cmd/buildtool website-footer`. So it must NOT be re-implemented in Go: the command *calls* the hook and fails when the call is removed (`internal/build/policy`'s guards), while `internal/build/website` pins the contract around it — the declared base line, the composed shape, the deleted machinery. Extract a rule that is written out here; leave a call that already has an owner.
- Never duplicate a classification or decision list across sibling helper scripts, and never re-implement in shell a rule a Go package already owns. Where the gate and CI must agree on one, the owning package is called by both: the gate and CI's scope job ask `go run ./cmd/buildtool scope` which paths count as documentation, and the gate asks `internal/build/layers` and `internal/build/coverage` — through the same command — whether the import matrix and the coverage tiers hold. The one thing that stays in shell is the toolchain resolution, because PATH can only be changed in the caller's shell; everything else, including the coverage loop and its fan-out, is the command's.
- Run `./scripts/verify.sh` before every commit or release prep pass.
- Keep `CHANGELOG.md` and GitHub release notes synchronized.
- `go run ./cmd/buildtool release --publish` resolves either `gh` or `gh.exe`, so it works in
  POSIX shells on Windows as well as native Linux and macOS shells, and pipes
  release notes instead of passing a shell-specific temporary-file path. It checks
  for `gh` before running the publish guards, so a missing CLI is reported as
  itself rather than behind an unrelated failure.
- Publish only from a clean `main` checkout: the release tag must exist, point
  at `HEAD`, and be reachable from `main`. `internal/build/release` owns those
  guards and the note rendering; `cmd/buildtool` runs them.
- Keep package-scoped fuzz corpora reviewed and checked in when a fuzz target changes, rather than letting random output become the only seed set.
- Keep benchmark history in dated archive files under `benchmarks/data/archive/` and keep `benchmarks/data/baseline.txt` as the latest canonical comparison point.
- `go run ./cmd/buildtool bench-baseline` owns `benchmarks/data/baseline.txt`: only a deliberate run (without `--capture-only`) rewrites it, and it archives the previous dump first. `go run ./cmd/buildtool bench-compare` captures for itself, so comparing can never replace the reference it compares against; `cmd/buildtool/bench_test.go` (run by the gate's race suite) enforces that split.
- `internal/build/depgraph` owns `docs/design/package-graph.md` and is its only writer: the checking form renders the document in memory and compares, so the gate can report a stale graph without any way to overwrite it, and `--write` is the only mode that touches the file. `internal/build/depgraph`'s own test pins the render against the committed document.
- Keep scripts fail-fast and explicit: `set -euo pipefail` is the default for bash helpers in this repo.
- Prefer repo-root execution. Scripts assume they are launched from the repository root unless a script explicitly documents otherwise.
- `go run ./cmd/buildtool run-examples` runs the examples that terminate on their own (`artifacts`, `bloom`, `files`, `notes`, `pack`), each with the subcommand that completes it, and `--list` names them together with the manual `api` example. The table is `internal/build/policy`, the selection rule `internal/build/examples`.
- The `api` example is a manual two-process pair, so no runner runs it: start the blocking server with `go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080`, then the demo client with `go run ./examples/api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md` in a second terminal (see [`examples/api/README.md`](../examples/api/README.md)).
- `go run ./cmd/buildtool security` installs the pinned `govulncheck` version so local and CI vulnerability scans are reproducible. The pin is `internal/build/policy`'s, the override is `GOVULNCHECK_VERSION`; update the table deliberately.
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
go run ./cmd/buildtool verify                   # the same gate, without the shim
VERIFY_FAST=true ./scripts/verify.sh            # quick pass: skips tests/coverage/fuzz/security, never stamps
VERIFY_SKIP_TESTS=true ./scripts/verify.sh      # skip only the race suite (~79s) while iterating
go run ./cmd/buildtool layer-matrix             # the gate's decisions, run on their own
go run ./cmd/buildtool coverage-tier
go run ./cmd/buildtool markdown-integrity
go run ./cmd/buildtool website-examples
go run ./cmd/buildtool codec-guards
go run ./cmd/buildtool module-graph
go run ./cmd/buildtool release --tag v1.4.5 --dry-run        # preview the release notes for a tag
go run ./cmd/buildtool release --tag v1.4.5 --publish        # create the GitHub release from them
go run ./cmd/buildtool release-notes --tag v1.4.5 --from v1.4.4   # just the note body
go run ./cmd/buildtool run-examples          # the examples that terminate on their own
go run ./cmd/buildtool run-examples --list   # them, plus the manual api pair
go run ./cmd/buildtool bench-baseline       # refresh the canonical reference dump (archives the old one)
go run ./cmd/buildtool bench-compare        # diff a fresh run against the committed reference
go run ./cmd/buildtool dep-graph          # report a stale package graph; writes nothing
go run ./cmd/buildtool dep-graph --write  # regenerate docs/design/package-graph.md
go run ./cmd/buildtool land-lane status     # the local advisory slot; 0 yours, 1 free, 2 someone else
go run ./cmd/buildtool land-lane acquire 389  # take it before a gate run
go run ./cmd/buildtool security             # the pinned vulnerability scan
go run ./cmd/buildtool pr-lane claim 389    # take the lane for an issue before starting
go run ./cmd/buildtool pr-lane status       # every lane on the remote and its pull request
go run ./cmd/buildtool pr-lane release 389  # free the lane once its PR merged
./scripts/gate-receipt.sh show              # the receipt for HEAD, if this clone has one
./scripts/gate-receipt.sh publish           # sign it and push refs/gate/<sha> for CI
```

When a script's behavior changes, update this README, the matching workflow, and any affected docs in the same change.
