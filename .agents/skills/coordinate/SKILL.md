---
name: coordinate
description: >
  Coordination playbook for the go-cask repository when more than one landing is
  in flight: read the board, find file collisions between lanes before they cost
  a rebuild, verify that landed lanes really landed, sequence the next wave, and
  keep one clone's gate runs from starving each other. Use when running or
  joining multi-lane work. Triggers: "coordinate sessions", "which PRs are
  stuck", "sequence these issues", "wave plan", "gate queue".
---

# go-cask coordination playbook

Passes in order; each names the artifact it produces. Contract: [`docs/specs/coordination.md`](/docs/specs/coordination.md); one landing: [`docs/specs/landing.md`](/docs/specs/landing.md).

## Pass 1 — Read the board

1. `go run ./cmd/buildtool pr-lane status` — every claim, its age, its PR.
2. `git worktree list` — task trees, branch, HEAD.
3. `gh pr list --state open --json number,headRefName,mergeable,mergeStateStatus,isDraft,autoMergeRequest`.
4. `gh issue list --state open`.

Green is evidence, never a report: the commit in the clone ledger `.git/verify.ok` **and** `.git/gate-receipts/<full-sha>.receipt`; `refs/gate/<sha>` on `origin` is the published form. A draft with `autoMergeRequest: null` cannot land, however green.

Artifact: the board, each lane with the authority for its state.

## Pass 2 — Find collisions before they cost a rebuild

Disjoint code files are not disjoint lanes. Check every lane against the serialization points `docs/specs/coordination.md` §4 names — `CHANGELOG.md`, `docs/index.md`, `docs/design/package-graph.md`, `docs/specs/defaults.md`, `docs/specs/cas-core.md`, `docs/specs/landing.md`, `scripts/verify.sh`'s coverage entries — and against each other's packages. Two lanes bumping one spec's frontmatter version conflict by construction.

Artifact: the file set each lane owns, and the points no two lanes may share.

## Pass 3 — Verify what already landed

Run `docs/specs/coordination.md` §6 against each merged lane, reading each line from its own authority: `gh pr view`, `gh issue view`, the changed-file list, `git verify-commit`, `refs/gate/<sha>`, `git worktree list`.

Artifact: each merged lane verified, or the one line that failed.

## Pass 4 — Sequence the next wave

Three lanes, disjoint file sets, at most one per serialization point. Re-check each issue's premise against the tree before spending a lane: a design proposal or an owner-side setting is not a lane. Tell every lane to bump frontmatter at push time, to the merged tree's value plus one.

Artifact: the wave, one lane per issue, with the files each lane owns.

## Pass 5 — Serialize the gate

More than one lane in this clone needing a gate run: hold the advisory slot — `go run ./cmd/buildtool land-lane acquire`, `renew` while running, `release` when done (`status`: 0 yours, 1 free, 2 someone else). Concurrent runs in one clone slow every run and manufacture failures that are not in any tree: `go list ...: error obtaining VCS status: exit status 128` is contention, not a regression — a run that could not take the slot says so, never reports contention as red.

Artifact: gate runs that do not overlap.

## Pass 6 — Report

Terse, with the authority for each line: lane, pull request, state, blocker, next action. Name the binding constraint — lanes, files, or the gate queue. Never restate a lane's claim as fact; never call a queued lane blocked.

Artifact: the report, and the operator's next decision.

## Stop conditions

- No writing into a worktree another session is mid-operation in.
- No closing an issue without merged evidence from `main` or a merged pull request.
- No dispatching an issue whose premise the tree no longer matches.
- No editing the primary checkout while a session may be using it.

## For deeper reading

- [`docs/specs/coordination.md`](/docs/specs/coordination.md) — the contract these passes run.
- [`docs/specs/landing.md`](/docs/specs/landing.md) — one worktree, one lane, one gate, one merge: §4 signed commits, WSL gate command, coverage thresholds; §5 auto-merge.
- [`.agents/skills/cask-change/SKILL.md`](/.agents/skills/cask-change/SKILL.md) — changing the code itself.
