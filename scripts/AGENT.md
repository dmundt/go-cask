---
type: Guide
title: Scripts — go-cask
description: Operational guardrails for the repo automation layer; keep script behavior consistent with local checks, CI, and release docs.
version: v2
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
