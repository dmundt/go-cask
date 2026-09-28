---
type: Specification
title: Landing — go-cask
description: The landing procedure for one task — the task worktree, the server-side lane whose record is the open pull request, the gate run that authorises a push, and the merge that lands it; the coordination rules every session MUST obey.
version: v9
---

# Landing — go-cask

- One task = one worktree, one lane, one gated branch, one merged pull request.
- Normative for every session, human or agent, that changes this repository.
- Related: `branch-naming.md` §2 (branch name), `versioning.md` §4 (commit and changelog conventions), `docs/specs/AGENT.md` §11 (signed-commit workflow), `scripts/AGENT.md` ("Running the scripts on Windows", "Releasing"), `.github/AGENT.md` (branch protection, required checks, gate receipts).

## 1. Why the lane exists

- Parallel sessions invalidate each other's branches: every merge leaves the others "behind"; each rebuild costs a gate run.
- The lane is the server-side record on the open pull request, not a file in one clone; an abandoned lane is reclaimed by the next session.

## 2. Task worktree

| Action | Command |
| --- | --- |
| Create one worktree per task | `go run ./cmd/buildtool worktree add <task> <type>/<NNN>-<kebab>` |
| Remove it after the PR merges | `go run ./cmd/buildtool worktree remove <task>` |

- `add` writes the worktree's `.git` in the relative form; an absolute path (the other toolchain) makes `git` walk up to the primary checkout and `verify.sh` refuses to gate [wrong tree].
- The wrapper locks the worktree: the shared git dir's reverse link holds one toolchain's path form.
- **Never run `git worktree prune`** — the other toolchain sees a live worktree as prunable and the prune deletes its registration with its index.
- `verify.sh` locks an unprotected registration before it gates; `buildtool worktree prune` refuses: git has no pre-command hook and no alias shadows a built-in, so git's own `locked` file is the whole protection.
- **Never edit the primary checkout while another session may be using it.**
- **Every task worktree is based on the freshly fetched `origin/main`**: `buildtool worktree add` fetches, then branches (`-b <branch> origin/main`).
- That remote-tracking ref is the only base: a local `main` can be behind the remote or carry another session's uncommitted work.
- Exception: the `hotfix` base `branch-naming.md` §3 defines (`release/vX.Y`).
- **Never `git add -A`**, **never `git commit -a`** — stage the paths you touched, or a shared tree sweeps another session's untracked files into your commit.
- **A scratch tree is not storage.** `.gocache/` is the build cache, `.worktrees/` holds task worktrees; both are untracked and cleaned. A durable record goes in a pull request.

## 3. The lane

- **Claim the lane before you start: `go run ./cmd/buildtool pr-lane claim <issue>`.** The record is the pull request.
- The claim is a server-side compare-and-swap on `refs/lane/<NNN>`, so creating it succeeds exactly once: two sessions cannot both claim one lane, and every clone, machine and toolchain reads the same answer.

| `pr-lane claim` exit | Meaning |
| --- | --- |
| 0 | Yours |
| 1 | Refused |
| 2 | Usage |

- `go run ./cmd/buildtool pr-lane status` lists every lane with the PR behind it, and `release <issue>` frees it after the merge; a bare `status` lists the advisory slot.
- The claim refuses a closed issue, an issue with an open PR, and a claim still inside its window — check `gh issue view NNN --json state` and `gh pr list --state all --limit 15` before starting.
- **Push early and open the pull request as a draft: the PR is the lease.** A claim with no PR holds only for the 90-minute claim window (`PR_LANE_STALE_MINUTES`); after it, the next claimer takes the lane over, so nobody runs a `--force` takeover.
- PR open → lane held; PR closed or merged → lane finished, and `release` clears it.
- **Re-read the owning spec immediately before asking a question** — parallel PRs make premises stale within minutes.
- **The local advisory slot is not the lane.** `go run ./cmd/buildtool land-lane` keeps two gate runs in ONE clone from overlapping.
- Slot commands `acquire` / `renew` / `release`; `wait [--watch] [<label>] [<seconds>]` queues for it rather than losing the race — default `LAND_LANE_WAIT_SECONDS`.

| `land-lane status` exit | Meaning |
| --- | --- |
| 0 | Yours |
| 1 | Free |
| 2 | Someone else; `status` is 2 s into a stale record |
| 3 | Evictable |

- `land-lane wait` exits 1 for a watched slot that is free, 3 when the wait ends with someone else still holding it.
- `acquire --takeover-dead` takes an evictable slot without waiting the window out: the holder's process is provably gone on this host.
- A holder is judged gone only when this host can say so: another host, no host recorded, or a toolchain that cannot read process start times is Unknown, and an unknown holder is never taken over.
- Holding it is not a condition for pushing — it only saves this clone from gating the same tree twice.
- **One decision or area per PR; append, don't rewrite.** Prefer a new spec row or bullet to rewriting an existing line; additions auto-merge, rewrites conflict and cost a rebuild. Keep a documentation PR to one file where possible.

## 4. The gate

- **Gate once per commit, at the right scope.** `./scripts/verify.sh` detects a documentation-only change and runs the documentation gate, the scope CI applies (`VERIFY_SCOPE=full` forces the whole gate, `VERIFY_SCOPE=docs` asserts the documentation scope).
- A green run stamps the commit in the shared git dir and writes the gate receipt, which `go run ./cmd/buildtool gate-receipt publish` (best-effort from `.githooks/pre-push`) signs and pushes as `refs/gate/<sha>`; CI verifies that receipt instead of repeating the suite it covers (`.github/AGENT.md`, "Local gate receipts").
- The gate cross-builds and vets `windows/amd64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64` locally, so platform-matrix failures surface before the push.
- Those targets are compile-gated, never executed: a cross-build cannot run what it produces, and the `verify` job is the only job that runs tests (testing-strategy §5).
- **A run is green only when it ends with `verification passed`**; a run that stops earlier failed even if nothing was echoed.
- **Install the hook once per clone:** `git config core.hooksPath .githooks`.
- `.githooks/pre-push` refuses a push whose HEAD holds no green stamp for that exact commit — the one hard local rule; re-pushing an unchanged commit costs no compute. The advisory slot is reported, never required.
- **Signed commits, local rebuild only** (§5): `git cherry-pick -S`, `git verify-commit` every head commit, `git push --force-with-lease`.
- **Before every PR creation or update**, run `./scripts/verify.sh` and confirm every configured coverage threshold passes. The gate re-runs after each change to the tree, not once per commit. A required operational step for all follow-up work, not optional cleanup.
- **At push time the workspace is clean and the branch is current** — the base rule `branch-naming.md` §1 states for the day a branch is cut, held for every later push: commit or stash in-flight work, then bring the branch up to date with a freshly fetched `origin/main` merged **locally**, and re-gate the merged tree. A branch behind `main` is rebuilt locally, re-verified and re-stamped (§5). Never GitHub's server-side "Update branch"/rebase: it rewrites the branch outside the local signature and gate chain.
- **On Windows, run the gate under WSL with a Linux Go toolchain**: the race and coverage gate needs cgo and a C compiler, which the Windows toolchain cannot take from WSL's `gcc`, and coverage measured on Windows does not predict the gate.
- Never duplicate the gate's steps in PowerShell; never look for a faster path. `./scripts/verify.sh` on `/mnt/d` is the one supported route.
- Rootless WSL setup and the exact command: `scripts/AGENT.md`, "Running the scripts on Windows".

## 5. Landing

- **The merge queue serializes the landing; land with `gh pr merge`.** Once the owner enables GitHub's merge queue on `main`, it rebases and re-tests each queued PR once, at merge time, after its required checks — `gh pr merge` enters the queue, and `gh pr merge --auto --squash` is the interim mechanism until it is on.
- Merge only after signature verification, the required checks and the coverage checks pass. Linear history is required: GitHub signs the landed squash commit, and the branch head's key is verified locally with `git verify-commit` (§4).
- GitHub's "Update branch" and server-side rebase operations stay forbidden: a branch that fell behind is rebuilt locally with `git cherry-pick -S`, re-verified and re-stamped.
- CI runs on `merge_group` too (`.github/workflows/ci.yml`, `.github/workflows/codeql.yml`): the queue's temporary branch carries no required check otherwise, and a queued PR without one waits forever.
- **Website changes take the lane too.** A branch that touches `website/**` is a landing like any other: claim the lane for the whole landing, and finish one website decision before starting the next.
- The site footer was redesigned five times in three hours (`#203` → `#220` → `#224` → `#236` → `#239`, six PRs) by sessions that could not see each other's merge, and every step cost an issue, a branch, a PR and a review.
- Nothing about the gate changes: a `website/**`-only change is still documentation scope, so this orders the work.

## 6. Checklist

- [x] Task worktree created by `buildtool worktree add`, based on the freshly fetched `origin/main`
- [x] Lane claimed with `pr-lane claim <issue>`; no `git add -A`, no `git commit -a`
- [x] Draft PR opened early; the PR is the lease; `pr-lane release <issue>` after the merge
- [x] Gate run at the right scope, green only at `verification passed`, under WSL on Windows
- [x] At push time: clean workspace, branch merged locally with the freshly fetched `origin/main`, gate re-run on the merged tree — never a server-side update-branch
- [x] `.githooks` installed; every head commit signed and verified with `git verify-commit`
- [x] Landing through `gh pr merge` (the merge queue) or `gh pr merge --auto --squash` while the queue is off; no server-side update-branch
