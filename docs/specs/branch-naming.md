---
type: Specification
title: Branch Naming — go-cask
description: The simple, effective Git branch concept for go-cask — one permanent branch (main), short-lived type-prefixed branches, optional release branches; naming patterns, examples, and lifecycle rules.
version: v4
---

# Branch Naming — go-cask

Git branch rules: **one permanent branch, short-lived typed branches, optional release branches**. No long-lived integration/per-developer branches. Related: `versioning.md` §3 (lifecycle) and §5 (release process), `AGENT.md` (folder conventions).

## 1. Concept

- `main` is the ONLY permanent branch; always releasable; all version tags land on it (versioning §3). Never force-pushed, never deleted.
- Everything else is short-lived: branched from `main`, merged via PR, deleted after merge.
- Forbidden concepts: `develop`, `trunk`, per-developer branches, long-running integration branches. A branch living longer than a few days is too big — split it.
- A branch name's type prefix is the contract: it states what the branch is for.

## 2. Naming pattern

Grammar: `<type>/<description>`.

- `<type>` ∈ {`feat` (new feature/additive), `fix` (bug fix, pre-release), `hotfix` (urgent fix for a shipped release, PATCH), `refactor` (no behavior change), `perf` (performance), `docs`, `chore` (tooling/CI/deps/maintenance), `release` (`release/vX.Y`), `experiment` (throwaway spike/prototype)}.
- `<description>`: kebab-case, lowercase, ASCII-only, hyphens between words, no trailing punctuation. MAY prefix an optional numeric ticket reference (`feat/1234-memory-backend`).
- Whole branch ≤ 50 characters.
- Forbidden: names `master`, `trunk`, `develop`, `dev`, `staging`, `prod`; uppercase letters; underscores; slashes inside the description; reserved Git names (`HEAD`, `-`). Missing type prefix or description is invalid (`new-branch`, `fix/1234`). `release` requires the `v` (`release/vX.Y`, never `release/1.2`).

## 3. Lifecycle

| Branch type | Branch from | Merged into | After merge |
|---|---|---|---|
| `feat`/`fix`/`refactor`/`perf`/`docs`/`chore` | `main` | `main` (PR) | delete |
| `hotfix` | `main` or `release/vX.Y` | `main` AND the open release branch | delete |
| `release/vX.Y` | `main` (at the vX.Y.0 tag) | merged back to `main` when unmaintained | delete |
| `experiment` | `main` | never | delete |

- `release/vX.Y` exists only while that minor receives PATCH releases; created on demand, never preemptively (versioning §3/§5).
- Squash-merge or merge commits both acceptable — keep history readable (Conventional Commits, versioning §4); do not rebase `main`.

## 4. Interaction with versioning

- Version tags (`vX.Y.Z`) land on `main`; the release process (versioning §5) starts from `main`.
- `release/vX.Y` carries only PATCH versions (`vX.Y.1`, …); MAJOR/MINOR work happens on `main`.
- A `hotfix/` branch becomes a PATCH release through the same process.

## 5. Checklist

- [x] `main` is the only permanent branch; everything else short-lived
- [x] Branch names match `<type>/<kebab-description>` (type from §2)
- [x] ≤ 50 chars, lowercase/ASCII/hyphens; no forbidden names
- [x] Feature/fix/hotfix branches merged via PR and deleted
- [x] `release/vX.Y` created on demand, PATCH-only, deleted when unmaintained
- [x] No `develop`/per-developer/long-lived branches
