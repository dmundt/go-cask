---
type: Guide
title: Scripts — go-cask
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v7
---

# Agent instructions — `scripts/`

This subtree contains the repo's operational command wrappers. Treat the scripts here as the canonical automation layer for verification, release notes, examples, and benchmarks.

## Purpose

- Keep local developer workflows and CI in sync.
- Centralize quiescent checks in `verify.sh` instead of spreading duplicate logic across workflows and docs.
- Keep release automation deterministic and driven from `CHANGELOG.md`.
- Keep example and benchmark runs simple enough to execute reliably from the repo root.

## Rules

- Write shell scripts in bash with `set -euo pipefail`.
- Prefer a single command path for a workflow; do not duplicate the same logic in multiple scripts when one wrapper can call the shared logic.
- Treat `./scripts/verify.sh` as the repo preflight gate for design changes, release prep, and CI parity checks.
- Keep GitHub release notes synchronized with `CHANGELOG.md`; include the standard `Full Changelog:` compare link for each release.
- Keep docs and workflow references in sync when script behavior changes.
- Do not add noisy background jobs or non-deterministic automation to the helper layer.
- Prefer explicit, easy-to-read output over hidden side effects.
- A script that owns a committed artifact must own it exclusively: when two
  helpers can write the same file, one of them gains a mode that never touches
  it, and a test in `verify.sh` pins the split (`test-bench-scripts.sh` is the
  reference for `bench-baseline.sh` versus `bench-compare.sh`).
- The landing helpers are part of this layer: `land-lane.sh` serializes who may
  push (one slot, in the shared git dir), `.githooks/pre-push` refuses a push
  without the lane and a green stamp for the exact commit, and `verify.sh` writes
  that stamp only on success. Keep them dependency-free, POSIX-sh safe for the
  hook, and never make the hook re-run work the stamp already covers — the point
  is that an unchanged commit costs nothing.
- Three invariants of those helpers are load-bearing. First, on a host where the
  gate and the push run in different toolchains (Windows: the race gate needs
  WSL, the push needs the Windows git client), the lane's holder identity must
  stay a portable token with no path in it — `land-lane.sh whoami` prints it for
  debugging — because `D:/x/repo` and `/mnt/d/x/repo` never compare equal, which
  would make a lane taken in one toolchain invisible to the other's hook. Second,
  `$common/verify.ok` is a ledger with one line per verified commit, never a
  single slot: overwriting it let a gate run in any other worktree invalidate a
  verified branch and refuse its push. Third, the lane is claimed with an
  exclusive create (`set -C`), never with a check followed by a write: two
  waiters retrying on the same cadence both found the slot absent and both claimed
  it, so two landings gated and pushed at once. `test-land-lane.sh` pins all
  three.

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

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Before every
PR creation or update, run `./scripts/verify.sh` and confirm all configured
coverage thresholds pass. Enable auto-merge or merge only after signature
verification, required checks, and coverage checks pass.
