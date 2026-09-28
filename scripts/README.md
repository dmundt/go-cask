---
type: Guide
title: Scripts — go-cask
description: The repo's entry points — the buildtool launcher, the toolchain resolution it shares with the hooks, and the gate — with every rule they run living in Go under internal/build and cmd/buildtool.
version: v19
---

# Scripts — go-cask

Entry points for *landing* work: rules in [`internal/build/`](../internal/build/README.md)
(engine), commands in [`cmd/buildtool`](../cmd/buildtool/README.md); each script = a shim over a
command.

## Name

- `scripts/`, deliberately not `build/`: the build engine *is*
  [`internal/build/`](../internal/build/README.md) — engine checks, go-cask's tables, the gate's
  command — so a second tree named `build/` would be read as the same thing.
- Entry points here: `buildtool.sh` (toolchain + tool), `verify.sh` (the gate's name for one
  subcommand), `go run ./cmd/buildtool pr-lane` (landing lane), `go run ./cmd/buildtool worktree`
  (task workspaces) — not build logic, so `build/` would misname them.

## Script inventory

| Script | Purpose |
|---|---|
| [`buildtool.sh`](./buildtool.sh) | One entry point: resolve the toolchain (Go may be absent from a Git Bash or WSL shell), run `go run ./cmd/buildtool` from the checkout the script belongs to, pass the arguments through. Every purpose is a subcommand (`verify` = the gate), so this file holds no rule of its own. |
| [`toolchain.sh`](./toolchain.sh) | The resolution `buildtool.sh`, `verify.sh` and the pre-push hook share, *sourced* rather than executed (PATH changes only in the caller's shell), POSIX `sh` (a git hook runs under `sh`). Sets `PATH` and `CGO_ENABLED`, nothing else. |
| [`verify.sh`](./verify.sh) | The verification gate, nothing else: starts `buildtool.sh verify`, passes the arguments through. Its former bash step list is `go run ./cmd/buildtool verify` — formatting, module drift, build, vet, the static analyzer, the layer matrix, the codec guards, the security scan, the coverage tiers, the race suite, the fuzz smoke and the documentation steps. Keeps the name: CI, `.githooks/pre-push` and the specification set call the gate by it. |

## Parity with Go

- Every remaining `.sh`: referenced, none orphaned.
- Each is a launcher that *cannot* be Go — a shell must find `go` before Go can run, `PATH`
  changes only in the caller's shell, the gate's name must stay where CI, `.githooks/pre-push` and
  the specification set call it.
- No script here owns a rule; the last one that did, the gate receipt, is
  `go run ./cmd/buildtool gate-receipt` now.

| Script | Referenced by | Parity | Remaining |
|---|---|---|---|
| `toolchain.sh` | `buildtool.sh`, `verify.sh` | n/a — exemption | Nothing: a shell is required to find `go` before Go can run. 49 lines, sourced, POSIX `sh`. |
| `buildtool.sh` | `.githooks/pre-push`, docs | n/a — exemption | Nothing: it exists to resolve the toolchain and pass through. 20 lines. |
| `verify.sh` | CI, the pre-push message, the specification set | 100% | Nothing: it resolves the toolchain and starts `buildtool.sh verify`. The step list it held is `go run ./cmd/buildtool verify`; the decisions behind it are `internal/build/verify`'s, and go-cask's answer to each is `internal/build/policy`'s gate table. |

- Replaced scripts are deleted, not archived: rule = a package under
  [`internal/build`](../internal/build/README.md), cases = tests beside it, header = the command
  that took over. Git history keeps them.
- Task worktrees = `go run ./cmd/buildtool worktree {add,remove,lock,list}`: writes the worktree's
  `.git` in the relative form both toolchains resolve, locks the registration against
  `git worktree prune`, refuses `prune` outright. Rules: `internal/build/worktree`.

Landing layer, two layers, both Go:

| Layer | Command | Owner |
|---|---|---|
| LANE — one open pull request is one lane, claimed with a server-side compare-and-swap on `refs/lane/<NNN>` | `go run ./cmd/buildtool pr-lane` | `internal/build/claim` |
| Local ADVISORY slot — one slot in the shared git dir, keeping two gate runs in one clone from overlapping | `go run ./cmd/buildtool land-lane` | `internal/build/lane` |
| Gate stamp read by the pre-push hook (`.githooks/pre-push` = a shim over `go run ./cmd/buildtool pre-push`) | `go run ./cmd/buildtool pre-push` | `internal/build/gate` |

`verify.sh` also calls the non-shell build decisions — each a package with its own tests, reached
through `go run ./cmd/buildtool`, one subcommand per decision:

| Package | Owns |
|---|---|
| `internal/build/changes` | change-set classification, over `internal/build/policy`'s rules |
| `internal/build/layers` | dependency-layer matrix |
| `internal/build/coverage` | coverage tiers and thresholds |
| `internal/build/docs` | Markdown integrity rules |
| `internal/build/website` | the site's Go fences, inventory tables and footer |
| `internal/build/bench` | benchmark capture decisions |
| `internal/build/examples` | example table |
| `internal/build/toolchain` | pinned toolchain |
| `internal/build/claim` | landing lane |
| `internal/build/deps` | codec guards plus module-graph check |

| Subcommand | Decides |
|---|---|
| `verify` | the gate: covered steps, their order, recordable or not. Scope, concurrency, escape hatches: `internal/build/verify`; entry points, variables, smoke-fuzz set: `internal/build/policy`; record: `internal/build/gate` |
| `scope` | which of CI's jobs a change set can affect; whether the gate may run the documentation scope |
| `layer-matrix` | every package's imports against AGENTS.md's layer table |
| `coverage-tier` | every `cas/` package carries a tier or a written exemption; `--list` prints the gate's measurement table |
| `coverage-check` | the thresholds, from stdin: one threshold/package/measurement line per gated package — the gate collects the measurements, this decides |
| `markdown-integrity` | every tracked `.md`: raw HTML, forbidden fences, dead links, the CHANGELOG structure, mermaid balance |
| `website-examples` | each Go fence on the site is a complete unit; the inventory tables match the tree; the materialized set builds and vets |
| `website-footer` | the published footer is the pinned one-line contract; the site hook's self-test still renders it |
| `security` | the vulnerability scan runs with the pinned `govulncheck`, installed when the one on PATH is not that release |
| `bench-baseline` | one deliberate run owns the committed benchmark reference dump, archiving the previous one first |
| `bench-compare` | a benchmark comparison chooses its baseline before capturing and never writes the reference |
| `run-examples` | which example programs a runner executes, with which arguments and store; which one it must never run |
| `land-lane` | the local advisory slot: `status`/`whoami`/`acquire`/`renew`/`release`, its idle-time staleness rule, the takeover record an eviction leaves |
| `pr-lane` | the server-side lane: `claim`/`check`/`status`/`release`/`whoami`, the ref that is the compare-and-swap, the pull request that is the lease, the claim window |
| `pre-push` | the mechanical landing rule a push must satisfy; the advisory-slot note |
| `codec-guards` | `gitlike` and `cas/pack` do not reach the codec layer transitively |
| `module-graph` | `go list -m` names this module as the main one |

The gate executes them; it does not restate them.

## Rules

- Local scripts and CI: one helper logic, called by both; never a duplicated command.
- **`verify.sh` = a permanent thin shim:** resolve the toolchain, start `buildtool.sh verify`. Add
  no pattern list, threshold table, `case` matrix or step — the step list is
  `cmd/buildtool verify`'s `gateSteps`; a decision behind a step → `internal/build` with a test.
  Owner: [`AGENT.md`](./AGENT.md) "`verify.sh` stays forever".
- **Fast-turnaround options:** `VERIFY_SKIP_TESTS`, `VERIFY_SKIP_COVERAGE`, `VERIFY_SKIP_FUZZ`,
  `VERIFY_SKIP_SECURITY` and `VERIFY_SKIP_LINT` drop one step each; `VERIFY_FAST=true` drops all
  five. A run using one is not a verified run.
- A skipped run prints what it skipped and writes **no** gate stamp → `.githooks/pre-push` still
  refuses to push that commit: time on a working tree, never a landing.
- Only the test step is worth dropping, and only both hatches drop it: coverage measurement and
  race suite are one run, so `VERIFY_SKIP_TESTS` alone still spends it.
- No shell here owns a rule; no helper-behaviour step is left to drop. Receipt = the last one:
  `internal/build/receipt`, `cmd/buildtool/gatereceipt_test.go`.
- Test pins, reached through the race suite: `internal/build/depgraph` (committed graph,
  byte-for-byte), `internal/build/versioning` (version-field decision), `cmd/buildtool/bench_test.go`
  (benchmark helpers' file ownership), `cmd/buildtool/prlane_test.go` (pull-request lane against a
  fake remote), `cmd/buildtool/landlane_test.go` with `internal/build/gate` (advisory slot, gate
  stamp).
- **One-command delegation is no violation;** the footer is the reference case:
  `website/macros.py --selftest` = one call whose whole rule (pinned rendered line, zone label,
  guessed date) lives in `website/macros.py`, reached by the gate through
  `go run ./cmd/buildtool website-footer`. Must NOT be re-implemented in Go
  (`internal/build/policy`'s guards fail when the call is removed; `internal/build/website` pins
  the contract: declared base line, composed shape, deleted machinery). Extract a rule written out
  here; leave a call with an owner.
- Never duplicate a classification or decision list across sibling helper scripts; never
  re-implement in shell a rule a Go package owns. Gate and CI scope job both call
  `go run ./cmd/buildtool scope` (documentation paths); the gate asks `internal/build/layers` and
  `internal/build/coverage` through the same command. Shell keeps only the toolchain resolution;
  everything else, coverage measurement and fan-out included, is the command's.
- Run `./scripts/verify.sh` before every commit or release prep pass.
- `CHANGELOG.md` and GitHub release notes stay synchronized.
- `go run ./cmd/buildtool release --publish` resolves `gh` or `gh.exe` (POSIX shells on Windows,
  native Linux and macOS) and pipes release notes instead of a shell-specific temporary-file path;
  it checks for `gh` before the publish guards, so a missing CLI is reported as itself.
- Publish only from a clean `main` checkout: the tag must exist, point at `HEAD`, be reachable from
  `main`. Guards and note rendering: `internal/build/release`; `cmd/buildtool` runs them.
- Package-scoped fuzz corpora reviewed and checked in when a fuzz target changes; random output is
  never the only seed set.
- Benchmark history: dated archive files under `benchmarks/data/archive/`;
  `benchmarks/data/baseline.txt` = the latest canonical comparison point.
- `go run ./cmd/buildtool bench-baseline` owns `benchmarks/data/baseline.txt`: only a deliberate
  run (without `--capture-only`) rewrites it, archiving the previous dump first.
  `go run ./cmd/buildtool bench-compare` captures for itself → comparing never replaces the
  reference; `cmd/buildtool/bench_test.go` (race suite) enforces the split.
- `internal/build/depgraph` owns `docs/design/package-graph.md`, sole writer: the checking form
  renders in memory and compares, so a stale graph is reported with no way to overwrite it;
  `--write` is the only mode that touches the file. Its test pins the render against the committed
  document.
- Bash helpers: `set -euo pipefail`. Scripts assume the repository root unless a script documents
  otherwise.
- `go run ./cmd/buildtool run-examples` runs `artifacts`, `bloom`, `files`, `notes`, `pack` — each
  with the subcommand that completes it; `--list` names them plus the manual `api` example. Table:
  `internal/build/policy`; selection rule: `internal/build/examples`.
- `api` = a manual two-process pair, so no runner runs it: start the blocking server with
  `go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080 -tokens "viewer=v_tok,operator=o_tok,admin=a_tok"`,
  then the demo client with
  `go run ./examples/api/demo -api http://127.0.0.1:8080 -token o_tok -file ./README.md` in a
  second terminal ([`examples/api/README.md`](../examples/api/README.md)).
- `go run ./cmd/buildtool security` installs the pinned `govulncheck`. Pin:
  `internal/build/policy`; override: `GOVULNCHECK_VERSION`; update the table deliberately.
- CI sets `VERIFY_SKIP_SECURITY=true` (its required `security` job runs the same pinned scan);
  local `verify.sh` runs it by default.
- Gate receipt = one contract, one owner: every step in `cmd/buildtool verify` records its check
  name from `internal/build/policy`'s gate table; `policy.VerifySuite()` = the list CI requires
  (`--require-suite full`). A step renamed or added on one side only → CI runs the whole gate,
  slower but never weaker.
- Publishing a receipt = signing it: `go run ./cmd/buildtool gate-receipt publish` uses this
  toolchain's git signing configuration → run it where `gpg.format` and `user.signingkey` are set
  (this host: the Windows toolchain, also the one that pushes). A clone without signing cannot
  publish; CI falls back to the full gate.
- Race/coverage gate requires CGO: on Windows use a Go-supported MinGW-w64 or LLVM compiler; some
  Go/MSVC combinations reject race-build flags.
- Documentation CI installs [requirements-docs.lock](../requirements-docs.lock) with hash
  verification; regenerate it with the command recorded in
  [requirements-docs.txt](../requirements-docs.txt) after updating a direct documentation
  dependency.

## Typical commands

```bash
./scripts/verify.sh
go run ./cmd/buildtool verify                   # the same gate, without the shim
VERIFY_FAST=true ./scripts/verify.sh            # quick pass: skips tests/coverage/fuzz/security/lint, never stamps
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
go run ./cmd/buildtool gate-receipt show     # the receipt for HEAD, if this clone has one
go run ./cmd/buildtool gate-receipt publish  # sign it and push refs/gate/<sha> for CI
```

When a script's behavior changes, update this README, the matching workflow, and any affected docs in the same change.
