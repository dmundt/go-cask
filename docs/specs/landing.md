---
type: Specification
title: Landing — go-cask
description: The landing procedure for one task — the task worktree, the server-side lane whose record is the open pull request, the gate run that authorises a push, and the merge that lands it; the coordination rules every session MUST obey.
version: v4
---

# Landing — go-cask

One task = one worktree, one lane, one gated branch, one merged pull request. Normative for every session, human or agent, that changes this repository. Related: `branch-naming.md` §2 (the branch name), `versioning.md` §4 (commit and changelog conventions), `docs/specs/AGENT.md` §11 (signed-commit workflow), `scripts/AGENT.md` ("Running the scripts on Windows", "Releasing"), `.github/AGENT.md` (branch protection, required checks, gate receipts).

## 1. Why the lane exists

Parallel sessions on one repository invalidate each other's branches:

- Every merge makes the others "behind"; each rebuild costs a gate run.
- The rebuild window is long enough for the next merge to arrive first.

Coordination therefore hangs off the one record every session, every clone and the operator can already see — the open pull request — and the server, not a file inside one clone, owns the serialization.

## 2. Task worktree

- **One worktree per task,** created by `go run ./cmd/buildtool worktree add <task> <type>/<NNN>-<kebab>` and removed with `go run ./cmd/buildtool worktree remove <task>` once the PR merges.
- The command writes the worktree's `.git` in the relative form. A worktree created by the other toolchain records an absolute path, which makes `git` walk up to the primary checkout, and `verify.sh` refuses to run when it detects that, because the gate would silently test the wrong tree.
- The wrapper also locks the worktree: the reverse link in the shared git dir holds one toolchain's path form. **Never run `git worktree prune`** — the other toolchain sees a live worktree as prunable and a prune deletes its registration together with its index. `verify.sh` locks any registration it finds unprotected before it gates, and `buildtool worktree prune` refuses outright: git has no pre-command hook and no alias can shadow a built-in, so git's own `locked` file is the whole protection.
- **Never edit the primary checkout while another session may be using it.**
- **Every task worktree is based on the freshly fetched `origin/main`.** `buildtool worktree add` fetches, then branches from `origin/main` (`-b <branch> origin/main`), and that remote-tracking ref is the only base a task worktree uses: a local `main` is never a substitute, because in the primary checkout it can be behind the remote or carry another session's uncommitted work. The one exception is the `hotfix` base `branch-naming.md` §3 defines (`release/vX.Y`).
- **Never `git add -A` and never `git commit -a`.** Stage the paths you touched: a shared tree otherwise sweeps another session's untracked files into your commit.

## 3. The lane

- **Claim the lane before you start: `go run ./cmd/buildtool pr-lane claim <issue>`.** The lane is the issue's landing and its record is the pull request. The claim is a server-side compare-and-swap on the coordination ref `refs/lane/<NNN>` — creating it succeeds exactly once — so two sessions cannot both claim one lane however their attempts interleave, and every clone, machine and toolchain reads the same answer.
- Exit 0 means yours, 1 refused, 2 usage; `status` lists every lane with the PR behind it, and `release <issue>` frees it after the merge. The claim refuses a closed issue, an issue with an open PR, and a claim still inside its window, so also check `gh issue view NNN --json state` and `gh pr list --state all --limit 15` before starting.
- **Push early and open the pull request as a draft: the PR is the lease.** A claim with no PR is protected only by the 90-minute claim window (`PR_LANE_STALE_MINUTES`), after which the next claimer takes the lane over. That is what keeps liveness objective — an abandoned lane is reclaimed by the next session, and nobody has to guess whether a holder is dead or run a `--force` takeover. A lane whose PR is open is held, full stop; a lane whose PR was closed or merged is finished, and `release` clears it.
- **Re-read the owning spec immediately before asking a question** — parallel PRs make premises stale within minutes.
- **The local advisory slot is not the lane.** `go run ./cmd/buildtool land-lane` keeps two gate runs in ONE clone from overlapping (`acquire` / `renew` / `release`; `status` exits 0 yours, 1 free, 2 someone else, 3 evictable — the holder's process is provably gone on this host, and `acquire --takeover-dead` takes it without waiting the window out). A holder is judged gone only when this host can say so: another host, no host recorded, or a toolchain that cannot read process start times is Unknown, and an unknown holder is never taken over. Holding it is not a condition for pushing — it only saves this clone from gating the same tree twice.
- **One decision or area per PR; append, don't rewrite.** Prefer adding a spec row or bullet over rewriting an existing line, and keep a documentation PR to one file where possible: additions auto-merge, rewrites conflict and cost a rebuild.

## 4. The gate

- **Gate once per commit, at the right scope.** `./scripts/verify.sh` detects a documentation-only change and runs the documentation gate — the scope CI applies (`VERIFY_SCOPE=full` forces the whole gate, `VERIFY_SCOPE=docs` asserts the documentation scope).
- A green run stamps the commit in the shared git dir and writes the gate receipt, which `go run ./cmd/buildtool gate-receipt publish` (called best-effort by `.githooks/pre-push`) signs and pushes as `refs/gate/<sha>`; CI verifies that receipt instead of repeating the suite it covers, and runs the whole gate whenever it cannot (`.github/AGENT.md`, "Local gate receipts").
- The gate also cross-builds and vets `windows/amd64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64` and `linux/arm64` locally, so the failures the platform matrix would find are found before the push. Those targets are compile-gated, never executed: a cross-build cannot run what it produces, and the `verify` job is the only job that runs tests (testing-strategy §5).
- **A run is green only when it ends with `verification passed`**; a run that stops earlier failed even if nothing was echoed about it.
- **Install the hook once per clone:** `git config core.hooksPath .githooks`. `.githooks/pre-push` refuses a push whose HEAD holds no green stamp for that exact commit — the one hard local rule, and re-pushing an unchanged commit costs no compute. The advisory slot is reported, never required.
- **Signed commits.** Never use GitHub's server-side rebase or update-branch operation. Rebuild each PR branch locally from current `main`, apply changes with `git cherry-pick -S`, verify every resulting head commit with `git verify-commit`, and push with `git push --force-with-lease`.
- **Before every PR creation or update**, run `./scripts/verify.sh` and confirm every configured coverage threshold passes. One green run covers every commit it contains, so a multi-commit branch needs the gate re-run after each change to the tree, not once per commit. The gate is a required operational step for all follow-up work, not an optional cleanup.
- **On Windows, run the gate under WSL with a Linux Go toolchain.** The race and coverage gate needs cgo and a C compiler, which the Windows toolchain cannot take from WSL's `gcc`, and coverage measured on Windows does not predict the gate. Never duplicate the gate's steps in PowerShell; never look for a faster path. `./scripts/verify.sh` on `/mnt/d` is the one supported route. The rootless WSL setup and the exact command are in `scripts/AGENT.md`, "Running the scripts on Windows".

## 5. Landing

- **The server serializes the landing; land with `gh pr merge --auto --squash`.** `main` requires its checks with `strict=false`, so a green, non-conflicting PR merges without a rebuild, and auto-merge is on GitHub's side — a clone that never held the local slot cannot corrupt anything. Enable auto-merge or merge only after signature verification, the required checks and the coverage checks pass. Linear history is required: GitHub signs the landed squash commit, while the key that signed the branch head is verified locally with `git verify-commit` (§4).
- GitHub's "Update branch" button stays forbidden: a branch that fell behind is rebuilt locally with `git cherry-pick -S`, re-verified, and re-stamped.
- GitHub's own merge queue is the intended replacement for auto-merge once it is enabled on `main` (the REST API does not accept the `merge_queue` rule for this repository today, so it is enabled by the owner in the UI, and CI must then also run on `merge_group`).
- **Website changes take the lane too.** A branch that touches `website/**` is a landing like any other: claim the lane for the whole landing, and finish one website decision before starting the next. The site footer was redesigned five times in three hours (`#203` → `#220` → `#224` → `#236` → `#239`, six PRs) by sessions that could not see each other's merge, and every step cost an issue, a branch, a PR and a review. Nothing about the gate changes: a `website/**`-only change is still documentation scope, so this orders the work without making it slower.

## 6. Checklist

- [x] Task worktree created by `buildtool worktree add`, based on the freshly fetched `origin/main`
- [x] Lane claimed with `pr-lane claim <issue>`; no `git add -A`, no `git commit -a`
- [x] Draft PR opened early; the PR is the lease; `pr-lane release <issue>` after the merge
- [x] Gate run at the right scope, green only at `verification passed`, under WSL on Windows
- [x] `.githooks` installed; every head commit signed and verified with `git verify-commit`
- [x] Landing through `gh pr merge --auto --squash`; no server-side update-branch
