---
type: Agent Instructions
title: Agent instructions — `scripts/`
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v14
---

# Agent instructions — `scripts/`

Subtree contains the repo's operational command wrappers. Treat the scripts here as the canonical automation layer for verification, release notes, examples, and benchmarks.

## Purpose

- Keep local developer workflows and CI in sync.
- Centralize quiescent checks in `verify.sh` instead of spreading duplicate logic across workflows and docs.
- Keep release automation deterministic and driven from `CHANGELOG.md`.
- Keep example and benchmark runs simple enough to execute reliably from the repo root.

## Rules

- Write shell scripts in bash with `set -euo pipefail`.
- Every new helper script MUST be executable in the index, not only on disk. On a
  checkout with `core.fileMode=false` (the Windows toolchain here) `chmod +x`
  changes nothing git records, so stage it with `git add --chmod=+x <path>` or
  `git update-index --chmod=+x <path>`. A `100644` script passes a local gate run
  and fails CI with `Permission denied`.
- Prefer a single command path for a workflow; never duplicate the same logic in multiple scripts when one wrapper can call the shared logic.
- Treat `./scripts/verify.sh` as the repo preflight gate for design changes, release prep, and CI parity checks.
- Keep GitHub release notes synchronized with `CHANGELOG.md`; include the standard `Full Changelog:` compare link for each release.
- Keep docs and workflow references in sync when script behavior changes.
- Never add noisy background jobs or non-deterministic automation to the helper layer.
- Prefer explicit, easy-to-read output over hidden side effects.
- A script that owns a committed artifact must own it exclusively: when two
  helpers can write the same file, one of them gains a mode that never touches
  it, and a test in `verify.sh` pins the split (`test-bench-scripts.sh` is the
  reference for `bench-baseline.sh` versus `bench-compare.sh`).
- Landing helpers are part of this layer, in two layers that must not be
  confused. `pr-lane.sh` owns the LANE: one open pull request is one lane, the
  claim is a server-side compare-and-swap on the coordination ref
  `refs/lane/<issue>`, and the lane is freed by merging or closing its PR and
  running `release`. `land-lane.sh` owns the local ADVISORY slot: one slot in the
  shared git dir that keeps two gate runs in one clone from overlapping, and it is
  not a condition for pushing. `verify.sh` writes the gate stamp and
  `.githooks/pre-push` refuses a push whose HEAD holds no stamp for that exact
  commit — the one hard local rule, because it is what makes re-pushing an
  unchanged commit free. Keep the helpers dependency-free, POSIX-sh safe for the
  hook, and never make the hook re-run work the stamp already covers.
- `gate-receipt.sh` is the stamp made portable, and it is the one helper whose
  failure mode is deliberate: `create` writes the receipt for a green run (commit,
  tree, merge base, hash of the changed path list, scope, the checks that ran),
  `publish` signs it with this toolchain's git key as a receipt commit (parent: the
  gated commit, tree: the gated tree, message: the receipt) and pushes it to
  `refs/gate/<sha>`, and `verify` is the CI side, which accepts a receipt only when
  the signature is in `.github/gate-signers` and the parent, the tree, the ancestor
  base, the recomputed diff hash, the recomputed scope and the check list all
  agree. Every refusal means CI runs the whole gate: that is the intended
  outcome for a fork, an unsigned gate, a branch behind `main`, or a malformed
  receipt, so never turn one into a skipped check.
- Three places hold that contract and they move together: a section in
  `verify.sh` marks itself with `mark_check <name>`, `suite_full` in
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
- Four invariants of those helpers are load-bearing. First, the lane is claimed
  with an atomic create on the REMOTE (`POST /git/refs` answers 422 when the ref
  exists), never with a check followed by a write: two sessions that retry on the
  same cadence would both find the lane free and both claim it — #299 is the
  local-slot version of that bug. Second, the claim record is the annotated tag
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
  push. `test-pr-lane.sh` pins the first two, `test-land-lane.sh` the last two.
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
  `test-land-lane.sh` pins the idle deadline, the refusal of a second acquirer in
  one worktree, and both diagnostics.
- `scripts/worktree.sh add` bases every task worktree on the freshly fetched
  `origin/main` (`-b <branch> origin/main`). A local `main` is not a substitute:
  in the primary checkout it can be behind the remote or hold another session's
  uncommitted work. The rule and its one exception — a `hotfix` based on
  `release/vX.Y` — live in
  [`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md) §3 and the
  repo-root [`AGENTS.md`](../AGENTS.md).

## Dependencies and scope

- Scripts may invoke `go`, `gofmt`, `git`, `benchstat`, and package-local helpers only when the repo already depends on them.
- Keep scripts repo-root aware and avoid assumptions about the caller's current directory.
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

WSL needs a Linux Go toolchain, a C compiler, and `python3` for the
documentation-integrity step. `gcc` is usually present; the other two install
without `sudo`:

```bash
# Linux Go into $HOME (the go.exe under /mnt/c cannot drive cgo builds in WSL)
mkdir -p "$HOME/opt" && cd "$HOME/opt"
curl -fsSL -o go.tgz "https://go.dev/dl/go1.27.1.linux-amd64.tar.gz"
tar xzf go.tgz

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
worktree's `.git` file (`gitdir: /mnt/d/.../.git/worktrees/<name>`). The gate's
final doc-integrity step runs the Windows CPython shim, so the `git ls-files`
it starts is the *Windows* git, which cannot resolve `/mnt/d/...`: the step
fails with `fatal: not a git repository` and the gate exits non-zero without
ever printing `verification passed`, even though the Go, coverage, race and
fuzz sections all passed. Either create the worktree with the Windows toolchain
instead, or rewrite `<worktree>/.git` to the relative form, which both
toolchains resolve:

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
