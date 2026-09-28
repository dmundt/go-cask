---
type: Specification
title: Coordination — go-cask
description: The coordinator role for many landings at once — the board it reads, waves and their serialization points, the advisory slot that serializes gate runs inside one clone, the evidence that proves a landing, and the stale claims a finished lane leaves behind.
version: v3
---

# Coordination — go-cask

- [`landing.md`](landing.md) owns one landing; this file owns **many at once**: what a coordinator reads, how it orders work so lanes do not collide, how one clone's gate runs avoid starving each other, and how a landing is proven.
- Normative for any session acting as coordinator, human or agent; restates no rule of `landing.md`.
- Related: `branch-naming.md` §2, `versioning.md` §4, `.github/AGENT.md` (gate receipts), `docs/index.md` (path routing), `docs/specs/AGENT.md` §8 (precedence).

## 1. The role

- A coordinator owns the *set* of landings in flight, not the code inside them.
- MUST NOT write into a worktree another session is mid-operation in (two writers turn a resolvable conflict into a real one).
- MUST NOT edit the primary checkout while any session may be using it (`landing.md` §2).
- MUST NOT close an issue without merged evidence.
- MUST NOT dispatch work whose issue premise no longer matches the tree: correct the stale premise in the issue before spending a lane.
- Adopted by a session (§9); nothing in this repository grants it or spawns a session by itself.

## 2. The board

| Source | Question it answers |
|---|---|
| `go run ./cmd/buildtool pr-lane status` | every claimed lane, its age, and the pull request behind it |
| `git worktree list` | which task trees exist, on which branch, at which HEAD |
| `gh pr list --state open --json number,headRefName,mergeable,mergeStateStatus,isDraft,autoMergeRequest` | which pull requests can land, which conflict, which are drafts that cannot |
| `gh issue list --state open` | what is actually outstanding |
| the clone ledger and receipts (§3) | which commits are gated green |

- A board that omits `draft` and `autoMergeRequest` is not a board: a green draft and a green
  armed pull request are indistinguishable, and only one can land.

## 3. Evidence, not claims

- **A gate is green for a commit when both hold:** the clone ledger names that commit, and a receipt for it exists.
- Both live in the clone's **common** git directory (`git rev-parse --git-common-dir`): `.git/verify.ok` and `.git/gate-receipts/<full-sha>.receipt` in the primary checkout, and the same two paths under the common directory from a linked worktree, where `.git` is a file rather than a directory.
- The ledger's name in the policy table is `policy.Gate().Ledger`.
- A lane reporting "gated green" is a claim; only the ledger is evidence.
- `refs/gate/<sha>` on `origin` is the published form of that fact, and the one CI reads.
- **`git worktree list` is the authority for a removal**, not the removal's own message: `buildtool worktree remove` computes its path from policy, so a worktree created outside that parent is unregistered while its directory survives (go-cask#461).
- **`gh pr view` is the authority for whether a pull request can land.** A claim with a pull request is a lease; a *draft* with no auto-merge is a lease that cannot be exercised, because GitHub refuses to arm auto-merge on a draft. Mark it ready and arm it, or do not count it as progress.

## 4. Waves

- A **wave** is a set of lanes allowed to run at once. Two lanes share a wave only when their file sets are disjoint.
- **Serialization points** — at most one lane per wave touches each: `CHANGELOG.md`, `docs/index.md`, `docs/design/package-graph.md`, `docs/specs/defaults.md`, `docs/specs/cas-core.md`, `docs/specs/landing.md`, and go-cask's coverage table (`internal/build/policy/policy.go`, read by the gate's coverage steps).
- **Disjoint build files are not enough.** Most user-visible changes carry a `CHANGELOG.md` bullet and any spec change carries a frontmatter `version:` bump, so two lanes editing different packages still meet in those files. Two lanes that bump one spec to the same number conflict by construction.
- **Apply a frontmatter bump at push time**, as the merged tree's current value plus one — never at commit time, where every lane in the wave picks the same number.
- **Append, don't rewrite** (`landing.md` §3). One changelog bullet per change, even when `main` already carries one under the same heading.
- A pull request reported `CONFLICTING` is the **rebuild signal**: merge `main` locally, resolve, re-gate the merged tree, push `--force-with-lease`. The server-side update-branch button stays forbidden (`landing.md` §5).

## 5. Gate throughput — the advisory slot is required when lanes share a clone

- **When more than one lane in one clone needs a gate run, each run MUST hold the local advisory slot:** `go run ./cmd/buildtool land-lane acquire`, `renew` while it runs, `release` when it ends.
- `status` exits 0 yours, 1 free, 2 someone else.
- The slot stays advisory to the push and never a condition for it (`landing.md` §3). It serializes *runs*, and only inside one clone.
- **Why it is a rule:** measured on this repository on 2026-09-27, about ten lanes in one clone ran **thirteen concurrent `verify.sh` invocations** against one WSL VM; every run slowed and the contention surfaced as failures with nothing wrong in the tree — `go list ... ./...: error obtaining VCS status: exit status 128` in the package-graph step (go-cask#462), and a receipt fuzz target failing on a latent defect no lane had touched (go-cask#452).
- A run that cannot take the slot waits, or says that it is waiting. It MUST NOT gate alongside another run in the same clone and report the contention failure as a red gate: that spends a whole run and misdirects the lane that owns the commit.
- The slot's staleness rule is idle time, not age: a holder that stops renewing expires, so a dead holder cannot block the queue.

## 6. Verifying a landing

Verified when every line holds, each read from its own authority rather than the lane's report:

- [x] the pull request merged as a **squash**, and its body cites `Closes #NNN`
- [x] the issue is `CLOSED`
- [x] the merged file list stays within the issue's scope — no drive-by file
- [x] a user-visible change carried a `CHANGELOG.md` bullet and the frontmatter bump it needed; a change that is not user-visible carried neither
- [x] the branch head was signed (`git verify-commit`) and its gate receipt `refs/gate/<sha>` exists on `origin`
- [x] the lane is released and `git worktree list` no longer names the worktree

## 7. Liveness and stale claims

- A claim with no pull request is protected only by the 90-minute window (`landing.md` §3). After it the next claimer takes the lane over.
- **A lane whose pull request merged is finished.** The coordinator MAY release it, and SHOULD when the holder has not: an unreleased merged lane is indistinguishable from an abandoned one on the next board.
- The abandoned shape is a claim whose worktree holds **no commits beyond `origin/main`** and which has run nothing inside its window. Report it, and release it only once its window has lapsed — until then the window is the holder's.
- Keep the board short: ten unbacked claims is a board nobody can read.

## 8. Reporting

- Report the board as it is, naming the authority for each line: lane, pull request, state, blocker, next action.
- Never restate a lane's claim as fact (§3), and never report a lane as blocked when it is queued behind other gates: both re-dispatch work already done and lose a real blocker among imagined ones.
- Name the binding constraint — "twelve lanes, one gate queue" and "twelve lanes, no collisions" are different reports, and only one tells the operator what to change.

## 9. Adopting the role

- A session adopts the role by loading `.agents/skills/coordinate/SKILL.md`, which carries the pass order: read the board, find collisions, verify what landed, sequence the next wave, serialize the gate, report.
- The role ends when the operator says so, or when the board is empty: a session that stops coordinating holds no lock and blocks no lane.
