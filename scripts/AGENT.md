---
type: Guide
title: Scripts — go-cask
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v3
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
