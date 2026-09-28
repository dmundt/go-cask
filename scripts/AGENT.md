---
type: Agent Instructions
title: Agent instructions — `scripts/`
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v26
---

# Agent instructions — `scripts/`

Automation wrappers only: verification, release notes, examples, benchmarks.

## Purpose

- Local workflows and CI in sync; quiescent checks centralized in `verify.sh`.
- Releases driven from `CHANGELOG.md`; examples and benchmarks runnable from the repo root.

## Rules

- Shell: bash + `set -euo pipefail`; POSIX `sh` for `buildtool.sh` and the git hooks (a hook runs
  under `sh`).
- New helper script MUST be executable in the index: `core.fileMode=false` (Windows toolchain
  here) records no `chmod +x` → `git add --chmod=+x <path>`, `git update-index --chmod=+x <path>`.
  A `100644` script passes a local gate run, fails CI with `Permission denied`; a *sourced* script
  (`toolchain.sh`) is never executed.
- **One launcher, no rule in any shell.** `buildtool.sh` = find Go (Git Bash, WSL may not inherit
  its path) → `go run ./cmd/buildtool` from the script's checkout, arguments passed through.
  `toolchain.sh` = that resolution, sourced, POSIX `sh` (`.githooks/pre-push` sources it too).
  Every purpose = a subcommand → a build verb is a `cmd/buildtool` subcommand, never a fourth
  script ([`README.md`](./README.md) "Script inventory").
- One command path per workflow; never duplicate logic across scripts when one wrapper can call
  the shared logic.
- **`verify.sh` stays forever: a shim, nothing else** — resolve the toolchain, exec
  `buildtool.sh verify`, the gate's name in every document, CI and the pre-push hook.
- **The shim holds no rule.** Rules = Go packages, each with tests: `internal/build/{changes,gate,lane,claim,verify,worktree,toolchain,bench,examples,layers,coverage,docs,website,deps,depgraph,versioning,release}`
  via `go run ./cmd/buildtool`; go-cask's answers `internal/build/policy`; step list
  `cmd/buildtool verify`. Neither shim nor step list MUST hold a rule as literal shell (pattern
  list, threshold table, `case` matrix, needle/haystack comparison) — reasoning in the test, not
  the source ([`../internal/build/AGENT.md`](../internal/build/AGENT.md)).
- Classification shared by two helpers: one owns it, the other calls it. Reference — change-set
  classification: rule `internal/build/changes`, patterns `internal/build/policy`; gate scope
  decision and CI scope job both call `go run ./cmd/buildtool scope`.
- Data plus logic → Go: `internal/build` owns the gate's decisions (`changes` classification,
  `layers` matrix, `coverage` tiers, `docs`, `website`, `deps`, `depgraph`, `versioning`, `verify`,
  `release`); `verify.sh` reaches them via `go run ./cmd/buildtool`. Shell resolves the toolchain;
  the command orchestrates (steps in order, output streamed, stamp written); the package decides
  ([`README.md`](./README.md) "Parity with Go").
- A count is not a rule and may stay in shell — but a count over tracked files, beside a step
  already reading them, belongs in the package owning the rules: `internal/build/docs` walks every
  tracked `.md`, so mermaid balance reads the same files in the same pass, not a second
  `git ls-files` + `grep -c`. Rule → package; one-line instantiation (`/bin/true`,
  `git rev-parse`) → here.
- **Build language = Go:** `internal/build` engine, `cmd/buildtool` entry point. No new `.sh`; no
  Python for build logic. Three launchers stay: a launcher must exist before Go can run;
  `verify.sh` is the gate's published name. A build change = a `cmd/buildtool` subcommand. One
  recorded exception: `website/macros.py` (MkDocs plugin hook, website toolchain not build
  tooling), run by `cmd/buildtool website-footer` (website/AGENT.md).
- `./scripts/verify.sh` = the repo preflight gate: design changes, release prep, CI parity checks.
- **Fast-loop skips:** `VERIFY_SKIP_TESTS`, `VERIFY_SKIP_COVERAGE`, `VERIFY_SKIP_FUZZ`,
  `VERIFY_SKIP_SECURITY`, `VERIFY_SKIP_LINT` drop one step each; `VERIFY_FAST=true` drops all
  five.
- A skipped run prints what it skipped and writes **no** gate stamp → `.githooks/pre-push` still
  refuses that commit: time on a working tree, never a landing.
- Worth dropping: the test step only, and only as a pair — coverage measurement and race suite are
  one run, so `VERIFY_SKIP_TESTS` alone keeps a red suite out of the verdict; `go test -short`
  saves nothing (no test consults `testing.Short()`).
- **Use the cores.** `VERIFY_JOBS` = machine share per step (default: core count; `1` = serial).
  Test pass: `go test -race -p N`; fan-out steps (cross-platform targets, fuzz smoke) divide it
  among concurrent items → the total stays the caller's number, logs printed in table order. A
  measured package's failure = the coverage check's decision, never the runner's (measurement and
  suite = one pass; its profile is the record).
- GitHub release notes stay synchronized with `CHANGELOG.md`; standard `Full Changelog:` compare
  link per release (versioning.md §4).
- No noisy background jobs; no non-deterministic automation in the helper layer.
- Explicit, readable output over hidden side effects.
- A committed artifact has one owning helper: two helpers that could write one file → one gains a
  never-touch mode, plus a test pinning the split (`cmd/buildtool bench-baseline` vs
  `cmd/buildtool bench-compare`, rule `internal/build/bench`, pin `cmd/buildtool/bench_test.go`).
- **LANE** = `go run ./cmd/buildtool pr-lane`: one open pull request = one lane; claim =
  server-side compare-and-swap on `refs/lane/<issue>`; freed by merging or closing the PR, then
  `release`. Owner `internal/build/claim`; ref namespace, window, override variables, record file:
  `internal/build/policy`'s `PRLane` table.
- **Local ADVISORY slot** = `go run ./cmd/buildtool land-lane`: one slot in the shared git dir
  keeping two gate runs in one clone from overlapping; records and decisions
  `internal/build/lane`; not a condition for pushing.
- **Stamp** = `internal/build/gate`. `.githooks/pre-push` (shim over
  `go run ./cmd/buildtool pre-push`) refuses a push whose HEAD holds no stamp for that exact
  commit — the one hard local rule; re-pushing an unchanged commit is free, and the hook never
  re-runs work the stamp covers. Owner:
  [`../docs/specs/landing.md`](../docs/specs/landing.md) §3, §4.
- **Receipt** = `go run ./cmd/buildtool gate-receipt`. `create` = a green run's receipt; `publish`
  = sign it as a receipt commit (parent: gated commit; tree: gated tree; message: the receipt),
  push `refs/gate/<sha>`; `verify` = CI side, accepting a receipt only if its signature is in
  `.github/gate-signers` and its parent, tree, ancestor base, recomputed diff hash, scope and check
  list agree — scope recomputed via `go run ./cmd/buildtool scope`. Owners:
  [`README.md`](./README.md) "Script inventory", [`../.github/AGENT.md`](../.github/AGENT.md)
  "Local gate receipts".
- Every refusal → CI runs the whole gate: fork, unsigned gate, branch behind `main`, malformed
  receipt; never a skipped check ([`../.github/AGENT.md`](../.github/AGENT.md) "Local gate
  receipts").
- Receipt contract: a step in `cmd/buildtool verify` records its check name from
  `internal/build/policy`'s gate table = the list `VerifySuite` hands the receipt's `suite` verb;
  `ci.yml` asks only `--require-suite full` → a one-sided rename or addition costs a full CI run,
  never a missed check. Receipt only for a clean working tree, never a runner. Pin:
  `cmd/buildtool/gatereceipt_test.go`.
- Publishing = signing → only the toolchain holding `gpg.format` and `user.signingkey` (this host:
  the Windows git, the one that pushes), so `.githooks/pre-push` publishes best-effort after its
  stamp check, not the WSL gate. No key → no publish, CI falls back.
- Receipt = Go; this directory holds no rule: the gate calls `create`, the hook `publish`; format
  and five verbs are `internal/build/receipt`'s; the shell test's cases are in
  `cmd/buildtool/gatereceipt_test.go`. `scripts/` keeps the launchers only.
- Four load-bearing invariants:

| # | Invariant | Pin |
|---|---|---|
| 1 | Lane claim = atomic create on the REMOTE (`POST /git/refs` answers 422 when the ref exists), never check-then-write | `cmd/buildtool/prlane_test.go` |
| 2 | Claim record = the annotated tag the ref points at (a ref carries no timestamp): names the claiming branch and worktree, dates the claim | `cmd/buildtool/prlane_test.go` |
| 3 | Slot holder identity = portable token with no path (`D:/x/repo` and `/mnt/d/x/repo` never compare equal); `whoami` prints it, the hook reports the slot with it | `cmd/buildtool/landlane_test.go`, `cmd/buildtool/prepush_test.go`, `internal/build/gate` |
| 4 | `$common/verify.ok` = a ledger, one line per verified commit, never a single slot | `cmd/buildtool/landlane_test.go`, `cmd/buildtool/prepush_test.go`, `internal/build/gate` |

- Neither lane is a branch, and neither may become one: `refs/lane/<issue>` = a coordination ref,
  outside the branch namespace ([`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md)
  §2); created, read, deleted through the refs API (`POST`/`DELETE /git/refs/lane/<issue>`); never
  committed to, pushed or fetched as a branch.
- Slot staleness = IDLE time, never age since acquisition; losing the slot is recorded. `acquire`
  writes the last-refresh moment, and only `renew` (the holder's own verb) moves it → a run
  outlasting `LAND_LANE_STALE_MINUTES` keeps the lane; renewing is never a side effect of asking
  for the slot. A takeover drops the slot only after recording the holder it evicts. Pins
  (`cmd/buildtool/landlane_test.go`): idle deadline, refusal of a second acquirer in one worktree,
  both diagnostics. Owner: `internal/build/lane`.
- `go run ./cmd/buildtool worktree add`: base every task worktree on the freshly fetched
  `origin/main` (`-b <branch> origin/main`), write the worktree's `.git` link in the relative form
  both toolchains resolve, lock the registration; a local `main` is no substitute. Exception:
  `hotfix` from `release/vX.Y` ([`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md)
  §3, [`docs/specs/landing.md`](../docs/specs/landing.md) §2). Link, admin directory, lock:
  `internal/build/worktree`, pinned by `cmd/buildtool/worktree_test.go`.
- **A destructive verb reports success only on evidence.**
  `go run ./cmd/buildtool worktree remove <task>` exits 3 when it removed nothing (unregistered
  name, or half a worktree left behind) and says which; `os.RemoveAll` returns nil for a missing
  path. A verb that removes, prunes or frees names what it removed; one that removed nothing
  carries that in its exit status, not only its message.

## One module

- Engine and policy share the root module → every root `./...` step reaches them: build, vet,
  test, `gofmt -l .`, `go mod tidy`, `layer-matrix`, `coverage-tier` ([`../internal/build/AGENT.md`](../internal/build/AGENT.md)).
- Pins live in `internal/build/policy`: the scanner, and the linter.

## Releasing

- Notes and guards: `internal/build/release`, exposed by `cmd/buildtool`; `CHANGELOG.md` = the
  single source they read.
- `release-notes`: extracts the `## [<tag>]` section, rewrites its `###` groups to `##`, appends
  the standard `Full Changelog:` compare link.
- `release`: resolves the previous tag, renders the same body; `--publish` hands it to
  `gh release create` instead of freeing the notes for a manual paste.

```bash
go run ./cmd/buildtool release --tag v1.3.0 --dry-run           # notes only; publishes nothing
go run ./cmd/buildtool release --tag v1.3.0 --from v1.2.0 --dry-run
go run ./cmd/buildtool release --tag v1.3.0 --publish           # create the GitHub release
go run ./cmd/buildtool release-notes --tag v1.3.0 --from v1.2.0 # just the body
```

- The versioned changelog section must exist first. `--publish` guards, each refused: dirty
  working tree; tag that does not exist; tag not at `HEAD`; tag not reachable from `main`.
- Resolves `gh` or `gh.exe` → one command from the Windows and POSIX shells alike; checks for `gh`
  before the guards, so a missing CLI is reported as itself.
- Process steps — bump, `CHANGELOG.md`, tagging:
  [`docs/specs/versioning.md`](../docs/specs/versioning.md) §5, which owns them and not the notes.

## Dependencies and scope

- Scripts may invoke `go`, `gofmt`, `git`, `benchstat`, and package-local helpers only when the
  repo already depends on them.
- Scripts repo-root aware: no assumptions about the caller's current directory.
- A script change to user-visible behavior updates [`README.md`](./README.md) and any relevant
  workflow or spec references in the same change.

## Running the scripts on Windows

- Every wrapper here MUST run under WSL (`bash`), never the Windows Go toolchain: `-race` needs
  cgo and a C compiler the Windows toolchain cannot take from WSL's `gcc` (a Linux binary) → the
  race/coverage gate fails or does not run, and a Windows coverage number does not predict the
  gate.
- Never re-implement the gate's steps by hand in PowerShell; run the script. Owner:
  [`../docs/specs/landing.md`](../docs/specs/landing.md) §4.
- WSL needs a Linux Go toolchain and a C compiler. `gcc` is usually present; Go installs without
  `sudo`:

```bash
# Linux Go into $HOME (the go.exe under /mnt/c cannot drive cgo builds in WSL)
mkdir -p "$HOME/opt" && cd "$HOME/opt"
curl -fsSL -o go.tgz "https://go.dev/dl/go1.27.1.linux-amd64.tar.gz"
tar xzf go.tgz
```

- `python3` is needed only for the website footer self-test (`website/macros.py`, run by the
  gate's `== website footer ==` step through `go run ./cmd/buildtool website-footer`, and by
  `internal/build/policy`'s footer tests); absent, the gate's footer step fails. Shim below:

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

- Run the gate from the repository root with both directories on `PATH`:

```bash
bash -c 'cd /mnt/d/Sources/Github/dmundt/go-cask && PATH="$HOME/opt/go/bin:$HOME/bin:$PATH" ./scripts/verify.sh'
```

- Green only when the run ends with `verification passed`; an earlier stop is a failure, even with
  nothing echoed. Judge by the script's own output and exit status, not a wrapper's echoed status.
- A green WSL run writes the gate receipt CI can reuse. Publishing it is signing → the toolchain
  holding the key: this host the Windows git (`gpg.format=ssh` plus `user.signingkey` under
  `%USERPROFILE%\.ssh`); the WSL git has no key.
- `.githooks/pre-push` runs in the toolchain that pushes → it publishes the receipt after its
  stamp check; a push from WSL git warns that CI will run the whole gate.
- To publish from WSL as well, configure signing there (signing needs no `allowed_signers` file;
  verification does):

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519_github_signing
```

### Worktrees created from WSL

- `git worktree add` from WSL writes an absolute WSL path into the new worktree's `.git` file
  (`gitdir: /mnt/d/.../.git/worktrees/<name>`).
- `verify.sh` refuses outright when `git rev-parse` resolves a different tree than the one the
  script lives in; the steps it starts must resolve that link too (`go build`, `go test`, the
  `go run ./cmd/buildtool website-footer` step).
- Fix: create the worktree with the Windows toolchain, or rewrite `<worktree>/.git` to the
  relative form both toolchains resolve:

```text
gitdir: ../../.git/worktrees/<name>
```

## Validation

- From the repo root:

```bash
./scripts/verify.sh
```

- A release-automation or changelog-sync change also verifies the generated release notes align
  with the current release entry (versioning.md §4).
- Signed-commit workflow (`git cherry-pick -S`, `git verify-commit`,
  `git push --force-with-lease`, no server-side rebase or update-branch operation): owner
  [`docs/specs/AGENT.md`](../docs/specs/AGENT.md) §11.
- Auto-merge after signature verification, required checks and coverage checks: owner
  [`docs/specs/landing.md`](../docs/specs/landing.md) §5.
