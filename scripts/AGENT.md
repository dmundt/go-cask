---
type: Agent Instructions
title: Agent instructions — `scripts/`
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v16
---

# Agent instructions — `scripts/`

Subtree contains repo's operational command wrappers. Treat scripts here as canonical automation layer for verification, release notes, examples, benchmarks.

## Purpose

- Keep local developer workflows and CI in sync.
- Centralize quiescent checks in `verify.sh`, not spreading duplicate logic across workflows and docs.
- Keep release automation deterministic, driven from `CHANGELOG.md`.
- Keep example and benchmark runs simple enough to execute reliably from repo root.

## Rules

- Write shell scripts in bash with `set -euo pipefail`. `buildtool.sh` and the
  git hooks are the exception and are POSIX `sh`: a hook runs under `sh`, and the
  launcher has to work from the same shell a hook does.
- A new helper script MUST be executable in the index, not only on disk. On a
  checkout with `core.fileMode=false` (the Windows toolchain here) `chmod +x`
  changes nothing git records, so stage it with `git add --chmod=+x <path>` or
  `git update-index --chmod=+x <path>`. A `100644` script passes a local gate run
  and fails CI with `Permission denied`. A script that is only *sourced*
  (`toolchain.sh`) is not one of these: it is never executed.
- **One launcher, and no rule in any of the shells.** `buildtool.sh` finds Go —
  Git Bash and WSL may not inherit its path, and a hook cannot assume it — runs
  `go run ./cmd/buildtool` from the checkout the script belongs to, and passes the
  arguments through; `toolchain.sh` is that resolution, sourced, POSIX `sh`
  because `.githooks/pre-push` sources it too. Every purpose is a subcommand, so
  the shells hold no rule: a verb added to the build process is a `cmd/buildtool`
  subcommand, not a fourth script.
- Prefer a single command path per workflow; never duplicate the same logic in multiple scripts when one wrapper can call the shared logic.
- **`verify.sh` stays forever, and it is now a shim and nothing else.** It is the
  repository's one entry point for verification — `buildtool.sh verify` under the
  gate's own name, because that is the name every document, CI and the pre-push
  hook call — and it does exactly two things: resolve the toolchain, and exec the
  command. Every rule it runs is enforced somewhere
  else: a Go package with its own tests. The
  engine packages are `internal/build/core/{changes,gate,lane,claim,verify,worktree,toolchain,bench,examples,layers,coverage,docs,website,deps,depgraph,versioning,release}`,
  reached through `go run ./cmd/buildtool`; go-cask's answers to them are
  `internal/build/policy`'s, and the step list itself is `cmd/buildtool verify`'s.
  Neither the shim nor the step list MUST hold a rule as literal shell: a pattern list, a
  threshold table, a `case` matrix, a needle/haystack comparison with its
  reasoning in the test rather than the source. The reason is not tidiness. A
  rule written inline in shell is executed by CI and by developers but covered by no
  test, so the only way to know it still holds is to run the whole gate; a rule
  in a package is covered by `go test` and by a table test of its own detector.
  It has already paid off twice: moving the layer matrix and the coverage tiers out of
  this file is how an inverted module-local check (`isLocal` deriving the module
  path from the allowed prefixes, so a local import no layer allowed looked
  third-party and escaped the gate) was found and pinned; and moving the step list
  is what exposed a `gofmt -l .` that walked every linked worktree under
  `.gocache`, so an unformatted file in another session's worktree could fail this
  one's gate. When a step needs a decision rather than a command, extract it — do
  not grow the list.
- When two helpers must agree on a classification, one of them owns it and the
  other calls it. The change-set classification is the reference case: the rule is
  `internal/build/changes`, go-cask's patterns are `internal/build/policy`, and the
  gate's scope decision and CI's scope job both call
  `go run ./cmd/buildtool scope`, so a second copy of the pattern list could only
  drift. The shell script that owned it is archived in `internal/build/shell`.
- A rule that is data plus logic belongs in Go, not in this shell layer, and the
  gate calls it. The packages under `internal/build/core` own the gate's decisions:
  `changes` the change-set classification (which paths a change touches, and what
  that decides), `layers` the dependency-layer matrix (AGENTS.md, "Layers and
  citizen classes"), `coverage` the coverage tiers and thresholds
  (testing-strategy.md §5), `docs` the Markdown integrity rules
  (docs/specs/AGENT.md §9), `website` the site's Go fences, shipped-package
  inventory tables and one-line footer (website/AGENT.md), `deps` the codec guards
  and the module-graph check (cas-core §4.12, §7), `depgraph` the committed package
  dependency graph, `versioning` the frontmatter version rule (docs/AGENT.md §1),
  `verify` the run's own scope, concurrency and escape hatches, and `release` the
  changelog-to-release-note rendering with its publish guards.
  `verify.sh` reaches them through
  `go run ./cmd/buildtool`. Keep the split clean: the shell resolves the toolchain, and the
  command orchestrates — runs the steps in order, streams their output, writes the stamp —
  while the Go package decides, so the rule is covered by ordinary tests instead of only by
  a whole gate run.
- A count is not a rule, and it may stay in shell — but a count over tracked
  files, beside a step already reading them, is better off in the package that
  owns the rules. `internal/build/docs` walks every tracked `.md` for the
  Markdown integrity rules, so the mermaid-balance check reads the same files in
  the same pass instead of a second `git ls-files` and a `grep -c` per file in
  this script. The line is whether the step decides something: a rule belongs in
  a package, and a one-line instantiation of one (`/bin/true`, `git rev-parse`)
  belongs here.
- Treat `./scripts/verify.sh` as repo preflight gate: design changes, release prep, CI parity checks.
- The gate's expensive steps can be skipped for a fast inner loop, and a skipped
  run is never a verified one. `VERIFY_SKIP_TESTS`, `VERIFY_SKIP_COVERAGE`,
  `VERIFY_SKIP_FUZZ` and `VERIFY_SKIP_SECURITY` each drop
  one step; `VERIFY_FAST=true` drops all four. A run that skipped anything prints
  what it skipped and **does not write the gate stamp**, so `.githooks/pre-push`
  still refuses to push that commit: the options buy time on a working tree,
  never a landing. Measured, the two steps worth dropping are the race suite
  (~79s) and the un-raced suite (~33s); everything else is a few seconds and not
  worth a flag. `go test -short` saves nothing here — no test consults
  `testing.Short()` — so it is deliberately not offered.
- **Use the cores.** `VERIFY_JOBS` sets how many gated packages the coverage loop
  runs at once (default: the core count; `1` makes that step serial), and the same
  number becomes `go test -race -p N`. Two orders were leaving cores idle: the loop
  was serial, and Go's own `-p` limit is below the core count on a large machine.
  Measured on 20 cores for the whole gate: 1 job 163s, 16 jobs 113s. The step is
  order-safe — each package writes its own output file and the report is reassembled
  in sorted order — so a parallel run logs the same lines in the same order as a
  serial one, and the only difference between two runs is a test's own duration. Two
  rules when touching it: never splice a coverage target into a shell command line (a
  target contains `|`, so `xargs -I{}` hands the shell a pipeline — pass it as an
  argument instead), and a worker must exit zero, because `xargs` aborts the gate on a
  non-zero status and low coverage is the coverage check's decision, not the worker's.
- Keep GitHub release notes synchronized with `CHANGELOG.md`; include standard `Full Changelog:` compare link per release.
- Keep docs and workflow references in sync when script behavior changes.
- Never add noisy background jobs or non-deterministic automation to helper layer.
- Prefer explicit, easy-to-read output over hidden side effects.
- A helper that owns a committed artifact must own it exclusively: when two
  helpers can write the same file, one of them gains a mode that never touches
  it, and a test pins the split. The benchmark pair is the reference and it is Go
  now: `cmd/buildtool bench-baseline` versus `cmd/buildtool bench-compare`, with
  the rule in `internal/build/bench` and the file ownership pinned by
  `cmd/buildtool/bench_test.go`.
- The landing helpers are part of this layer, in two layers that must not be
  confused, and both are Go now. `go run ./cmd/buildtool pr-lane` owns the LANE:
  one open pull request is one lane, the claim is a server-side compare-and-swap
  on the coordination ref `refs/lane/<issue>`, and the lane is freed by merging or
  closing its PR and running `release`. What the ref means and what a claim may do
  with it are `internal/build/core/claim`'s; the `gh`, git and ref calls are the
  command's; the ref namespace, the window, the override variables and the record
  file are `internal/build/policy`'s `PRLane` table. The local ADVISORY slot — one
  slot in the shared git dir that
  keeps two gate runs in one clone from overlapping — is
  `go run ./cmd/buildtool land-lane`, with the records and the decisions in
  `internal/build/core/lane`; it is not a condition for pushing. The gate writes
  the stamp (`internal/build/core/gate`) and `.githooks/pre-push` — a shim over
  `go run ./cmd/buildtool pre-push` — refuses a push whose HEAD holds no stamp for
  that exact commit: the one hard local rule, because it is what makes re-pushing
  an unchanged commit free. Never make the hook re-run work the stamp already
  covers.
- `gate-receipt.sh` is the stamp made portable, and it is the one helper whose
  failure mode is deliberate: `create` writes the receipt for a green run (commit,
  tree, merge base, hash of the changed path list, scope, the checks that ran),
  `publish` signs it with this toolchain's git key as a receipt commit (parent: the
  gated commit, tree: the gated tree, message: the receipt) and pushes it to
  `refs/gate/<sha>`, and `verify` is the CI side, which accepts a receipt only when
  the signature is in `.github/gate-signers` and the parent, the tree, the ancestor
  base, the recomputed diff hash, the recomputed scope and the check list all
  agree, and it recomputes the change's scope with `go run ./cmd/buildtool scope`
  rather than trusting the receipt's own word for it.
- Every refusal means CI runs the whole gate: that is the intended
  outcome for a fork, an unsigned gate, a branch behind `main`, or a malformed
  receipt, so never turn one into a skipped check.
- Three places hold that contract and they move together: a step in
  `cmd/buildtool verify` marks itself with `r.mark(<name>)`, `suite_full` in
  `gate-receipt.sh` lists the marks CI requires, and `ci.yml` asks only for
  `--require-suite full`. Adding or renaming a gate section without the other two
  costs a full CI run, never a missed one. `verify.sh` writes a receipt only for a
  clean working tree and never on a runner: a receipt names the tree of a commit,
  and CI checks out a merge commit nobody pushes.
- Publishing is signing, so it happens in the toolchain that has `gpg.format` and
  `user.signingkey` — on this host the Windows git, which is also the one that
  pushes, and the reason `.githooks/pre-push` publishes best-effort after its stamp
  check rather than the WSL gate doing it. A toolchain that cannot sign cannot
  publish, and CI falls back; the hook says so and names the command.
- The receipt is the one rule in this directory that has not moved to Go: the gate`n  calls `create` and the hook calls `publish`, so nothing is unrouted, but the format`n  and the three verbs are still shell. Porting them retires `test-gate-receipt.sh``n  into Go tests beside them.
- Four invariants of those helpers are load-bearing. First, the lane is claimed
  with an atomic create on the REMOTE (`POST /git/refs` answers 422 when the ref
  exists), never with a check followed by a write: two sessions that retry on the
  same cadence would both find the lane free and both claim it — the local slot
  had the same bug, and `lane.Decide` plus the exclusive create fixed it there.
  Second, the claim record is the annotated tag
  object the ref points at, because a ref carries no timestamp: the record names
  the claiming branch and worktree and dates the claim, which is what lets another
  session tell a claim still inside its window from one that was abandoned.
  Third, the local slot's holder identity must stay a portable token with no path
  in it — on a host where the gate and the push run in different toolchains
  (Windows: the race gate needs WSL, the push needs the Windows git client)
  `D:/x/repo` and `/mnt/d/x/repo` never compare equal — because `whoami` prints
  it and the hook reports the slot with it. Fourth, `$common/verify.ok` is a
  ledger with one line per verified commit, never a single slot: overwriting it
  let a gate run in any other worktree invalidate a verified branch and refuse its
  push. `cmd/buildtool/prlane_test.go` pins the first two; `cmd/buildtool/landlane_test.go`,
  `cmd/buildtool/prepush_test.go` and `internal/build/core/gate` pin the last two.
- Neither lane is a branch, and neither may become one. `refs/lane/<issue>` is a
  coordination ref — outside the branch namespace
  [`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md) §2 governs —
  and nothing is ever committed to it: it is created, read and deleted through the
  refs API (`POST`/`DELETE /git/refs/lane/<issue>`), never pushed to or fetched
  as a branch.
- The local slot's staleness is IDLE time, never age since acquisition, and losing
  the slot is recorded. `acquire` writes the moment the slot was last refreshed
  and only `renew` — the holder's own verb — moves that moment forward, so a gate
  run or push that outlasts `LAND_LANE_STALE_MINUTES` keeps the lane instead of
  being evicted by the next session's `acquire` (#325). Renewing is never a side
  effect of asking for the slot, or a second session in one worktree would collect
  a lane it does not hold. A takeover drops the slot only after recording the
  holder it evicts: the evicted holder has no other way to learn what happened.
  `cmd/buildtool/landlane_test.go` pins the idle deadline, the refusal of a second
  acquirer in one worktree, and both diagnostics.
- `go run ./cmd/buildtool worktree add` bases every task worktree on the freshly
  fetched `origin/main` (`-b <branch> origin/main`), writes the worktree's `.git`
  link in the relative form both toolchains resolve, and locks the registration.
  A local `main` is not a substitute:
  in the primary checkout it can be behind the remote or hold another session's
  uncommitted work. The rule and its one exception — a `hotfix` based on
  `release/vX.Y` — live in
  [`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md) §3 and the
  repo-root [`AGENTS.md`](../AGENTS.md); the link, the admin directory it must
  resolve to and the lock are `internal/build/core/worktree`'s, and
  `cmd/buildtool/worktree_test.go` pins them against a real repository.

## The build engine is its own module

`internal/build/core` is a **separate Go module** with its own `go.mod`, its own
tests, and no dependency beyond the standard library. It holds the reusable half of
the gate's decisions; go-cask's own tables live in `internal/build/policy`, and
`cmd/buildtool` is the entry point that wires the two together. The root module
reaches the engine through a `require` plus a local-path `replace`.

The split exists because the engine is meant to serve a second repository. Keep it
that way:

- **The engine ships no policy.** A layer table, a coverage tier, an inventory
  table, a document's prose — each is one repository's answer, so it belongs in
  `internal/build/policy` and reaches the engine as a parameter. An engine package
  that starts naming `cas/` or `gitlike/` has lost the property that makes it
  reusable.
- **The engine imports only the standard library.** It has no `go.sum`; adding a
  dependency to it is a decision, not an accident.
- **Every gate step that covers the engine names it explicitly.** This is the trap:
  `go build ./...`, `go vet ./...`, `go test ./...` and `gofmt -l .` **skip a nested
  module**, so the engine could rot untested while the gate stayed green. `verify.sh`
  therefore runs its own `build engine module` step and a second `go mod tidy` in
  `internal/build/core`.
- **The layer matrix does not see the engine**, and that is correct: the engine is
  outside the root module, so `go list ./...` never returns its packages.

## Releasing

The release pair is no longer here: `internal/build/release` owns the notes and
`cmd/buildtool` exposes them, with `CHANGELOG.md` as the single source they read.
`release-notes` extracts the `## [<tag>]` section, rewrites its `###` groups to
`##`, and appends the standard `Full Changelog:` compare link. `release` is the
entry point — it resolves the previous tag, renders the same body, and with
`--publish` hands it to `gh release create` instead of freeing the notes for a
manual paste:

```bash
go run ./cmd/buildtool release --tag v1.3.0 --dry-run           # notes only; publishes nothing
go run ./cmd/buildtool release --tag v1.3.0 --from v1.2.0 --dry-run
go run ./cmd/buildtool release --tag v1.3.0 --publish           # create the GitHub release
go run ./cmd/buildtool release-notes --tag v1.3.0 --from v1.2.0 # just the body
```

The notes come from the versioned changelog section, so the section must exist
before this runs, and the `--publish` guards are what make the release record
match the tree: it refuses a dirty working tree, a tag that does not exist, a tag
that does not point at `HEAD`, and a tag that is not reachable from `main`. Those
guards are why publishing goes through the command rather than a hand-typed
`gh release create`; it also resolves `gh` or `gh.exe`, so one command works from
the Windows and POSIX shells alike, and it checks for `gh` before the guards so a
missing CLI is reported as itself. The process steps themselves — choosing the
bump, updating `CHANGELOG.md`, tagging — are
[`docs/specs/versioning.md`](../docs/specs/versioning.md) §5, which owns them;
nothing here restates that, and that section does not cover notes or publishing.

## Dependencies and scope

- Scripts may invoke `go`, `gofmt`, `git`, `benchstat`, and package-local helpers only when the repo already depends on them.
- Keep scripts repo-root aware, avoid assumptions about the caller's current directory.
- If a script changes user-visible behavior, update [`README.md`](./README.md) and any relevant workflow or spec references in the same change.

## Running the scripts on Windows

Every wrapper here is a bash script. On Windows they MUST run under WSL
(`bash`), never under the Windows Go toolchain:

- `verify.sh` builds and tests with `-race`, which needs cgo and a C compiler.
  The Windows toolchain cannot use WSL's `gcc` (a Linux binary), so the race and
  coverage gate either fails or does not run at all.
- Coverage and platform-gated branches differ between Windows and Linux builds,
  so a number measured with the Windows toolchain does not predict the gate.
  Never re-implement the gate's steps by hand in PowerShell; run the script.

WSL needs a Linux Go toolchain and a C compiler. `gcc` is usually present; Go
installs without `sudo`:

```bash
# Linux Go into $HOME (the go.exe under /mnt/c cannot drive cgo builds in WSL)
mkdir -p "$HOME/opt" && cd "$HOME/opt"
curl -fsSL -o go.tgz "https://go.dev/dl/go1.27.1.linux-amd64.tar.gz"
tar xzf go.tgz
```

`python3` is needed only for the website footer self-test (`website/macros.py`,
run by the gate's `== website footer ==` step through
`go run ./cmd/buildtool website-footer`, and by `internal/build/policy`'s footer
tests). The documentation-integrity rules no longer embed a Python program — they are
`internal/build/docs` — so the path-translating shim that step required is gone.
If `python3` is absent the gate's footer step fails where a shell's `command -v`
finds nothing, and the footer tests skip that check locally with a note; the
mirror below is still the way to make it run under WSL:

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

A run is green only when it ends with `verification passed`. A run that stops
earlier failed: the gate reports each unmet coverage threshold and every other
failure as it proceeds, then exits non-zero after the race suite. Judge the
result by the script's own output and exit status, not by a wrapper's echoed
status.

A green WSL run also writes the gate receipt that lets CI reuse it instead of
repeating the suite. Publishing that receipt is signing, and signing belongs to
the toolchain that holds the key: this host signs with the Windows git
(`gpg.format=ssh` plus `user.signingkey` under `%USERPROFILE%\.ssh`), while the
WSL git carries its own, separate config and no key. `.githooks/pre-push` runs in
the toolchain that pushes, so it publishes the receipt after its stamp check; a
push made from WSL git warns instead that CI will run the whole gate. To publish
from WSL as well, configure signing there — signing needs no `allowed_signers`
file, only verification does:

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519_github_signing
```

### Worktrees created from WSL

`git worktree add` run from WSL records an absolute WSL path in the new
worktree's `.git` file (`gitdir: /mnt/d/.../.git/worktrees/<name>`). The gate
reads that link with whichever toolchain runs it — `verify.sh` refuses outright
when `git rev-parse` resolves a different tree than the one the script lives in,
because the gate would silently test the primary checkout instead — and the
steps it starts must resolve it too (`go build`, `go test`, and the
`go run ./cmd/buildtool website-footer` footer step, whose self-test runs under the
Windows interpreter). Either create the worktree with the Windows toolchain instead,
or rewrite `<worktree>/.git` to the relative form, which both toolchains resolve:

```text
gitdir: ../../.git/worktrees/<name>
```

## Validation

Before finishing a change in this subtree, run the smallest relevant validation from the repo root:

```bash
./scripts/verify.sh
```

If the change affects release automation or changelog sync, also verify the generated release notes are aligned with the current release entry.

## Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Before every PR
creation or update, run `./scripts/verify.sh` and confirm all configured
coverage thresholds pass. Enable auto-merge or merge only after signature
verification, required checks, and coverage checks pass.
