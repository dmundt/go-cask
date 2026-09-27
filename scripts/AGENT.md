---
type: Agent Instructions
title: Agent instructions — `scripts/`
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v24
---

# Agent instructions — `scripts/`

Repo's operational command wrappers. Canonical automation layer: verification, release notes, examples, benchmarks.

## Purpose

- Local developer workflows and CI in sync.
- Quiescent checks centralized in `verify.sh`, not duplicated across workflows and docs.
- Release automation deterministic, driven from `CHANGELOG.md`.
- Example and benchmark runs simple enough to execute reliably from repo root.

## Rules

- Shell scripts: bash with `set -euo pipefail`; POSIX `sh` for `buildtool.sh` and the git hooks, because a hook runs under `sh` and the launcher must work from the same shell a hook does.
- New helper script MUST be executable in the index, not only on disk. On `core.fileMode=false` (Windows toolchain here) `chmod +x` records nothing, so stage with `git add --chmod=+x <path>` or `git update-index --chmod=+x <path>`: a `100644` script passes a local gate run and fails CI with `Permission denied`. A script only *sourced* (`toolchain.sh`) is never executed.
- **One launcher, and no rule in any of the shells.** `buildtool.sh`: finds Go — Git Bash and WSL may not inherit its path — runs `go run ./cmd/buildtool` from the checkout the script belongs to, passes arguments through. `toolchain.sh` = that resolution, sourced, POSIX `sh` because `.githooks/pre-push` sources it too. Every purpose is a subcommand, so the shells hold no rule: a verb added to the build process is a `cmd/buildtool` subcommand, not a fourth script. Detail: [`README.md`](./README.md) "Script inventory".
- One command path per workflow; never duplicate the same logic in multiple scripts when one wrapper can call the shared logic.
- **`verify.sh` stays forever, and it is a shim and nothing else.** Repository's one entry point for verification — `buildtool.sh verify` under the gate's own name, the name every document, CI and the pre-push hook call — doing exactly two things: resolve the toolchain, exec the command.
- **The shim holds no rule.** Every rule it runs is enforced elsewhere, as a Go package with its own tests: engine packages `internal/build/core/{changes,gate,lane,claim,verify,worktree,toolchain,bench,examples,layers,coverage,docs,website,deps,depgraph,versioning,release}` reached through `go run ./cmd/buildtool`, go-cask's answers in `internal/build/policy`, the step list in `cmd/buildtool verify`. Neither the shim nor the step list MUST hold a rule as literal shell — pattern list, threshold table, `case` matrix, needle/haystack comparison with its reasoning in the test rather than the source. Details: [`../internal/build/AGENT.md`](../internal/build/AGENT.md).
- When two helpers must agree on a classification, one owns it, the other calls it. Reference case — change-set classification: rule `internal/build/core/changes`, go-cask's patterns `internal/build/policy`, and gate scope decision plus CI scope job both call `go run ./cmd/buildtool scope`, so a second copy of the pattern list could only drift.
- A rule that is data plus logic belongs in Go, and the gate calls it: `internal/build/core` owns the gate's decisions (`changes` classification, `layers` matrix, `coverage` tiers, `docs`, `website`, `deps`, `depgraph`, `versioning`, `verify`, `release` — each with its owning spec in [`README.md`](./README.md) "Parity with Go"). `verify.sh` reaches them through `go run ./cmd/buildtool`. Shell resolves the toolchain and the command orchestrates — steps in order, output streamed, stamp written — while the Go package decides.
- A count is not a rule, and it may stay in shell — but a count over tracked files, beside a step already reading them, belongs in the package that owns the rules. `internal/build/core/docs` walks every tracked `.md` for the Markdown integrity rules, so the mermaid-balance check reads the same files in the same pass instead of a second `git ls-files` and a `grep -c` per file here. The line: whether the step decides something. A rule → package; a one-line instantiation of one (`/bin/true`, `git rev-parse`) → here.
- **The build language is Go.** Build logic is Go — `internal/build/core` engine, `cmd/buildtool` entry point. No new `.sh`; Python not used for build logic: such a rule is covered by no test, must be re-run to be trusted, and drags whatever interpreter the operator has into the build. The three launchers stay — a launcher must exist before Go can run, and `verify.sh` is the gate's published name — exception, not a licence for a fourth. A build change is a `cmd/buildtool` subcommand; one that fits nowhere there is a design problem to fix, not a reason to reach for a script. `website/macros.py` = the one recorded exception: MkDocs plugin hook, the website toolchain rather than repo build tooling, driven by `cmd/buildtool website-footer` (website/AGENT.md).
- Treat `./scripts/verify.sh` as the repo preflight gate: design changes, release prep, CI parity checks.
- Gate's expensive steps skippable for a fast inner loop; a skipped run is never a verified one. `VERIFY_SKIP_TESTS`, `VERIFY_SKIP_COVERAGE`, `VERIFY_SKIP_FUZZ`, `VERIFY_SKIP_SECURITY` each drop one step; `VERIFY_FAST=true` drops all four. A run that skipped anything prints what it skipped and **does not write the gate stamp**, so `.githooks/pre-push` still refuses to push that commit: the options buy time on a working tree, never a landing. Worth dropping: the test step, and there only as a pair — the coverage measurement and the race suite are one run, so `VERIFY_SKIP_TESTS` alone keeps a red suite out of the verdict while the run still happens for the numbers. `go test -short` saves nothing — no test consults `testing.Short()` — so not offered.
- **Use the cores.** `VERIFY_JOBS` = how much of the machine a step may use at once (default: core count; `1` = serial). The test pass takes it as `go test -race -p N`; a step that fans out over independent items — the cross-platform targets, the fuzz smoke — divides it among the items running at once, so the total stays the number the caller gave rather than the number of items times that number. Each item's log is printed in the table's order afterwards, so a parallel run logs the same lines in the same order as a serial one. One rule when touching it: a measured package's failure is the coverage check's decision, never the runner's — the suite may come back red with every tier still reported, which is why the measurement and the suite are one pass and the profile that pass writes is the record, not a line scraped out of a log.
- Keep GitHub release notes synchronized with `CHANGELOG.md`; include the standard `Full Changelog:` compare link per release (versioning.md §4).
- Keep docs and workflow references in sync when script behavior changes.
- No noisy background jobs; no non-deterministic automation in the helper layer.
- Explicit, easy-to-read output over hidden side effects.
- A helper that owns a committed artifact owns it exclusively: when two helpers can write the same file, one gains a mode that never touches it, and a test pins the split — `cmd/buildtool bench-baseline` versus `cmd/buildtool bench-compare`, rule in `internal/build/core/bench`, pinned by `cmd/buildtool/bench_test.go`.
- Landing helpers are part of this layer, two layers both Go now. `go run ./cmd/buildtool pr-lane` owns the LANE: one open pull request is one lane; the claim is a server-side compare-and-swap on the coordination ref `refs/lane/<issue>`; the lane is freed by merging or closing its PR and running `release`. What the ref means and what a claim may do with it: `internal/build/core/claim`; the ref namespace, window, override variables and record file: `internal/build/policy`'s `PRLane` table. The local ADVISORY slot — one slot in the shared git dir, keeping two gate runs in one clone from overlapping — is `go run ./cmd/buildtool land-lane`, records and decisions in `internal/build/core/lane`; it is not a condition for pushing. The gate writes the stamp (`internal/build/core/gate`); `.githooks/pre-push` — a shim over `go run ./cmd/buildtool pre-push` — refuses a push whose HEAD holds no stamp for that exact commit: the one hard local rule, and what makes re-pushing an unchanged commit free. Never make the hook re-run work the stamp already covers. Owner: [`../docs/specs/landing.md`](../docs/specs/landing.md) §3, §4.
- `go run ./cmd/buildtool gate-receipt` is the stamp made portable, and its failure mode is deliberate. `create` writes the receipt for a green run; `publish` signs it as a receipt commit (parent: the gated commit, tree: the gated tree, message: the receipt) and pushes it to `refs/gate/<sha>`; `verify` is the CI side, accepting only a receipt whose signature is in `.github/gate-signers` and whose parent, tree, ancestor base, recomputed diff hash, scope and check list all agree, recomputing scope with `go run ./cmd/buildtool scope` rather than trusting the receipt's own word. Receipt fields, verbs and CI checks: [`README.md`](./README.md) "Script inventory", [`../.github/AGENT.md`](../.github/AGENT.md) "Local gate receipts".
- Every refusal means CI runs the whole gate: intended outcome for a fork, an unsigned gate, a branch behind `main`, or a malformed receipt — never turn one into a skipped check ([`../.github/AGENT.md`](../.github/AGENT.md) "Local gate receipts").
- Two places hold that contract and they move together: a step in `cmd/buildtool verify` records its check name from `internal/build/policy`'s gate table, which is also the list `VerifySuite` hands the receipt's `suite` verb, and `ci.yml` asks only for `--require-suite full` — a step renamed or added without the other costs a full CI run, never a missed one. The gate writes a receipt only for a clean working tree and never on a runner: a receipt names the tree of a commit, and CI checks out a merge commit nobody pushes. Pin: `cmd/buildtool/gatereceipt_test.go`.
- Publishing is signing, so it happens in the toolchain that has `gpg.format` and `user.signingkey` — on this host the Windows git, the one that pushes, and the reason `.githooks/pre-push` publishes best-effort after its stamp check rather than the WSL gate doing it. A toolchain that cannot sign cannot publish and CI falls back; the hook names the command.
- The receipt is Go, and this directory holds no rule any more: the gate calls the command's `create` and the hook calls its `publish`, the format and the five verbs are `internal/build/core/receipt`'s, and the cases the shell test asserted live in `cmd/buildtool/gatereceipt_test.go`. `scripts/` keeps only the launchers.
- Four invariants are load-bearing. First, the lane is claimed with an atomic create on the REMOTE (`POST /git/refs` answers 422 when the ref exists), never with a check followed by a write. Second, the claim record is the annotated tag the ref points at, because a ref carries no timestamp: it names the claiming branch and worktree and dates the claim, so another session can tell a claim inside its window from an abandoned one. Third, the local slot's holder identity must stay a portable token with no path in it — `D:/x/repo` and `/mnt/d/x/repo` never compare equal — because `whoami` prints it and the hook reports the slot with it. Fourth, `$common/verify.ok` is a ledger with one line per verified commit, never a single slot: overwriting it let a gate run in any other worktree invalidate a verified branch and refuse its push. `cmd/buildtool/prlane_test.go` pins the first two; `cmd/buildtool/landlane_test.go`, `cmd/buildtool/prepush_test.go` and `internal/build/core/gate` pin the last two.
- Neither lane is a branch, and neither may become one. `refs/lane/<issue>` is a coordination ref — outside the branch namespace [`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md) §2 governs — and nothing is ever committed to it: created, read and deleted through the refs API (`POST`/`DELETE /git/refs/lane/<issue>`), never pushed to or fetched as a branch.
- The local slot's staleness is IDLE time, never age since acquisition, and losing the slot is recorded: `acquire` writes the moment the slot was last refreshed, and only `renew` — the holder's own verb — moves that moment forward, so a gate run or push that outlasts `LAND_LANE_STALE_MINUTES` keeps the lane instead of being evicted by the next session's `acquire`. Renewing is never a side effect of asking for the slot, or a second session in one worktree would collect a lane it does not hold. A takeover drops the slot only after recording the holder it evicts. `cmd/buildtool/landlane_test.go` pins the idle deadline, the refusal of a second acquirer in one worktree, and both diagnostics. Owner: `internal/build/core/lane`.
- `go run ./cmd/buildtool worktree add` bases every task worktree on the freshly fetched `origin/main` (`-b <branch> origin/main`), writes the worktree's `.git` link in the relative form both toolchains resolve, and locks the registration; a local `main` is no substitute. Rule and its one exception — a `hotfix` based on `release/vX.Y`: [`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md) §3, [`docs/specs/landing.md`](../docs/specs/landing.md) §2. The link, the admin directory it must resolve to and the lock: `internal/build/core/worktree`, pinned against a real repository by `cmd/buildtool/worktree_test.go`.
- **A destructive verb reports success only on evidence.** `go run ./cmd/buildtool worktree remove <task>` exits 3 when it removed nothing — a name registered nowhere, or a half of the worktree the removal left behind — and says which, instead of printing the success line. Not tidiness: the fallback is `os.RemoveAll`, which returns nil for a path that is not there, so without it the verb reported a removal it had not performed. A verb that removes, prunes or frees something names what it removed, and a verb that removed nothing carries that in its exit status, not only in its message.

## The build engine is its own module

Separate Go module, own `go.mod`, own tests, nothing beyond the standard library — so none of the root module's `./...` steps see it, and the gate's own `build engine module` step plus a second `go mod tidy` cover it. Rules: [`../internal/build/AGENT.md`](../internal/build/AGENT.md).

## Releasing

Notes and guards: `internal/build/core/release`, exposed by `cmd/buildtool`, with `CHANGELOG.md` as the single source they read. `release-notes` extracts the `## [<tag>]` section, rewrites its `###` groups to `##`, and appends the standard `Full Changelog:` compare link. `release` resolves the previous tag, renders the same body, and with `--publish` hands it to `gh release create` instead of freeing the notes for a manual paste:

```bash
go run ./cmd/buildtool release --tag v1.3.0 --dry-run           # notes only; publishes nothing
go run ./cmd/buildtool release --tag v1.3.0 --from v1.2.0 --dry-run
go run ./cmd/buildtool release --tag v1.3.0 --publish           # create the GitHub release
go run ./cmd/buildtool release-notes --tag v1.3.0 --from v1.2.0 # just the body
```

The notes come from the versioned changelog section, so that section must exist first, and the `--publish` guards make the release record match the tree: a dirty working tree, a tag that does not exist, a tag that does not point at `HEAD`, a tag not reachable from `main` — each refused, which is why publishing goes through the command rather than a hand-typed `gh release create`. It resolves `gh` or `gh.exe`, so one command works from the Windows and POSIX shells alike, and it checks for `gh` before the guards so a missing CLI is reported as itself. Process steps — bump, `CHANGELOG.md`, tagging: [`docs/specs/versioning.md`](../docs/specs/versioning.md) §5, which owns them and not the notes.

## Dependencies and scope

- Scripts may invoke `go`, `gofmt`, `git`, `benchstat`, and package-local helpers only when the repo already depends on them.
- Scripts repo-root aware: no assumptions about the caller's current directory.
- A script change to user-visible behavior updates [`README.md`](./README.md) and any relevant workflow or spec references in the same change.

## Running the scripts on Windows

Every wrapper here MUST run under WSL (`bash`), never under the Windows Go toolchain: `verify.sh` builds and tests with `-race`, which needs cgo and a C compiler the Windows toolchain cannot take from WSL's `gcc` (a Linux binary), so the race and coverage gate either fails or does not run at all, and a coverage number measured on Windows does not predict the gate. Never re-implement the gate's steps by hand in PowerShell; run the script. Rule: [`../docs/specs/landing.md`](../docs/specs/landing.md) §4.

WSL needs a Linux Go toolchain and a C compiler. `gcc` is usually present; Go installs without `sudo`:

```bash
# Linux Go into $HOME (the go.exe under /mnt/c cannot drive cgo builds in WSL)
mkdir -p "$HOME/opt" && cd "$HOME/opt"
curl -fsSL -o go.tgz "https://go.dev/dl/go1.27.1.linux-amd64.tar.gz"
tar xzf go.tgz
```

`python3` is needed only for the website footer self-test (`website/macros.py`, run by the gate's `== website footer ==` step through `go run ./cmd/buildtool website-footer`, and by `internal/build/policy`'s footer tests). If `python3` is absent the gate's footer step fails; the mirror below runs it under WSL:

```bash
# python3 shim: forward to the Windows interpreter and translate the path
mkdir -p "$HOME/bin"
cat > "$HOME/bin/python3" <<'SH'
#!/bin/sh
if [ "$1" = "-" ] && [ -n "${2:-}" ]; then
  shift
  exec "/mnt/c/Users/<you>/AppData/Local/Programs/Python/Python312/python.exe" - "$(wslpath -w "$1")"
fi
exec "/mnt/c/Users/<you>/AppData/Local/Programs/Python/Python312/python.exe" "$@"
SH
chmod +x "$HOME/bin/python3"
```

Run the gate from the repository root with both directories on `PATH`:

```bash
bash -c 'cd /mnt/d/Sources/Github/dmundt/go-cask && PATH="$HOME/opt/go/bin:$HOME/bin:$PATH" ./scripts/verify.sh'
```

A run is green only when it ends with `verification passed`; a run that stops earlier failed, even if nothing was echoed about it. Judge the result by the script's own output and exit status, not by a wrapper's echoed status.

A green WSL run also writes the gate receipt that lets CI reuse it instead of repeating the suite. Publishing that receipt is signing, and signing belongs to the toolchain that holds the key: this host signs with the Windows git (`gpg.format=ssh` plus `user.signingkey` under `%USERPROFILE%\.ssh`), while the WSL git carries no key. `.githooks/pre-push` runs in the toolchain that pushes, so it publishes the receipt after its stamp check; a push made from WSL git warns instead that CI will run the whole gate. To publish from WSL as well, configure signing there — signing needs no `allowed_signers` file, only verification does:

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519_github_signing
```

### Worktrees created from WSL

`git worktree add` from WSL records an absolute WSL path in the new worktree's `.git` file (`gitdir: /mnt/d/.../.git/worktrees/<name>`). `verify.sh` refuses outright when `git rev-parse` resolves a different tree than the one the script lives in, because the gate would silently test the primary checkout instead, and the steps it starts must resolve that link too (`go build`, `go test`, the `go run ./cmd/buildtool website-footer` step). Either create the worktree with the Windows toolchain instead, or rewrite `<worktree>/.git` to the relative form, which both toolchains resolve:

```text
gitdir: ../../.git/worktrees/<name>
```

## Validation

From the repo root:

```bash
./scripts/verify.sh
```

A change to release automation or changelog sync also verifies the generated release notes are aligned with the current release entry (versioning.md §4). Signed-commit workflow — `git cherry-pick -S`, `git verify-commit`, `git push --force-with-lease`, no server-side rebase or update-branch operation — is [`docs/specs/AGENT.md`](../docs/specs/AGENT.md) §11's; auto-merge after signature verification, required checks and coverage checks is [`docs/specs/landing.md`](../docs/specs/landing.md) §5's.
