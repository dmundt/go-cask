---
title: Branch Naming — go-cask
description: The simple, effective Git branch concept for go-cask — one permanent branch (main), short-lived type-prefixed branches, optional release branches; naming patterns, examples, and lifecycle rules.
version: v1
---

# Branch Naming — go-cask

> How Git branches are named and live in this project. The concept is
> deliberately simple: **one permanent branch, short-lived typed branches,
> optional release branches**. No long-lived integration branches, no
> per-developer branches, no ambiguity about what a branch is for.
>
> Related: `.github/instructions/versioning.instructions.md` §3 (branch
> lifecycle) and §5 (release process — what branches carry), `AGENT.md`
> (folder conventions).

---

## 1. The Branch Concept (simple, effective)

```text
main                  ← the ONLY permanent branch; always releasable; tagged
 │
 ├── feat/<desc>      short-lived, PR → main, deleted
 ├── fix/<desc>       short-lived, PR → main, deleted
 ├── hotfix/<desc>    short-lived, from main (or release/vX.Y), merged to both
 ├── refactor|perf|docs|chore/<desc>   short-lived, PR → main, deleted
 ├── release/vX.Y     created ONLY when a shipped minor needs maintenance
 └── experiment/<desc>  throwaway, never merged, deleted
```

Rules of the concept:

1. **`main` is the only permanent branch.** It must always be in a releasable
   state; all version tags land on it (versioning §3).
2. **Everything else is short-lived**: branched from `main`, merged via PR,
   deleted after merge.
3. **No `develop`, no `trunk`, no per-developer branches**, no long-running
   integration branches. If a branch lives longer than a few days, it is too
   big — split it.
4. **A branch name says what it is**: the type prefix is the contract.

---

## 2. Naming Pattern

Grammar:

```text
<type>/<description>
```

- **`<type>`** — one of:

| Type         | Purpose                                              |
| ------------ | ---------------------------------------------------- |
| `feat`       | new feature or additive capability                   |
| `fix`        | bug fix (pre-release of a version)                   |
| `hotfix`     | urgent fix for a shipped release (PATCH)             |
| `refactor`   | internal restructuring, no behavior change           |
| `perf`       | performance improvement                              |
| `docs`       | documentation only                                   |
| `chore`      | tooling, CI, dependencies, maintenance               |
| `release`    | release maintenance branch (`release/vX.Y`)          |
| `experiment` | throwaway spike/prototype                            |

- **`<description>`** — kebab-case, lowercase, ASCII only, hyphens between
  words, no trailing punctuation. An optional numeric ticket reference may
  prefix the description: `feat/1234-memory-backend`.
- **Length**: keep the whole branch ≤ 50 characters.

Forbidden in branch names: `master`, `trunk`, `develop`, `dev`, `staging`,
`prod`; uppercase letters; underscores; slashes inside the description;
reserved Git names (`HEAD`, `-`).

---

## 3. Examples

Good:

```text
feat/memory-backend
feat/1234-memory-backend
fix/hash-path-roundtrip
hotfix/corrupt-index-read
refactor/lock-free-reads
perf/one-pass-hashing
docs/branch-naming
chore/ci-benchstat-gate
release/v1.2
experiment/packfile-format
```

Bad (and why):

```text
master                  # forbidden name; use main
develop                 # no long-lived integration branch
feature/MemoryBackend   # wrong type (feat), wrong case
fix/1234               # description missing — what does it fix?
feat/my_new_backend     # underscores; use hyphens
new-branch              # missing the type prefix
release/1.2             # missing the v (versioning §3: release/vX.Y)
```

---

## 4. Lifecycle Rules

| Branch type       | Branch from | Merged into        | After merge        |
| ----------------- | ----------- | ------------------ | ------------------ |
| `feat`/`fix`/`refactor`/`perf`/`docs`/`chore` | `main` | `main` (PR) | delete |
| `hotfix`          | `main` or `release/vX.Y` | `main` AND the open release branch | delete |
| `release/vX.Y`    | `main` (at the vX.Y.0 tag) | merged back to `main` when unmaintained | delete |
| `experiment`      | `main` | never              | delete |

- `main` is never force-pushed and never deleted.
- `release/vX.Y` exists only while that minor receives PATCH releases
  (versioning §3/§5); it is created on demand, not preemptively.
- Squash-merge or merge commits are both fine — keep the history readable
  (Conventional Commits per versioning §4); do not rebase `main`.

---

## 5. Interaction with Versioning

- Version tags (`vX.Y.Z`) land on `main` (versioning §3); the release
  process (versioning §5) starts from `main`.
- `release/vX.Y` carries **only PATCH** versions (`vX.Y.1`, …); MAJOR/MINOR
  work happens on `main`.
- A `hotfix/` branch becomes a PATCH release through the same process.

---

## 6. Checklist

- [ ] `main` is the only permanent branch; everything else short-lived
- [ ] Branch names match `<type>/<kebab-description>` (type from §2)
- [ ] ≤ 50 chars, lowercase/ASCII/hyphens; no forbidden names
- [ ] Feature/fix/hotfix branches merged via PR and deleted
- [ ] `release/vX.Y` created on demand, PATCH-only, deleted when unmaintained
- [ ] No `develop`/per-developer/long-lived branches
