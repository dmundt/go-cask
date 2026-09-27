---
type: Design
title: Build Parity Audit — go-cask
description: What an audit of scripts/ and internal/build/ found — the parity gaps, the orphaned work, the process friction, the landings that followed, and the errors it produced.
version: v1
---

# Build Parity Audit — go-cask

Non-normative. The rules it names live in their own files, reached through
[`docs/index.md`](../index.md); this document records what an audit found and what was done about it.

## 1. Scope and method

Audited `scripts/` and `internal/build/` — their contents, their parity with the Go ports that
replaced them, and the landing process they serve — over seven rounds in one session. Every finding
was checked against the tree rather than against its documentation, and every change was landed
through the lane, gate and signed-commit discipline the repository requires.

The audit's own record was first kept as an untracked note under `.gocache/`. It was deleted when
another session cleaned that scratch tree, which is why this document exists and why
[`landing.md`](../specs/landing.md) §2 now says a record goes in a pull request, not in a scratch
directory.

## 2. The headline: work finished but unlandable

The interesting exposure was not in `scripts/` at all. At the start of the audit:

- **An open pull request was blocked on a fix that existed only in one checkout.** The Windows
  platform job failed on an 8.3 short path (`RUNNER~1` against `runneradmin`) compared as text. The
  fix had been written, signed and amended into the branch head eighteen hours earlier and never
  pushed; nobody could see it, including the session that owned the pull request.
- **Seven branches carried commits no pull request tracked.** Four were live defects: a
  Windows command-injection vector in the viewer's browser launch, a record cap that let
  `cask verify --checksums --all` abort reporting nothing, a destructive verb that reported a
  removal it had not performed, and a lost-claim race in the land lane.
- **Two branches carried the same work under two issue numbers**, one of them misnamed: the branch
  `enhancement/366-land-lane-atomic-renew` patches the land lane, while issue #366 is about
  `cask list`.

Nothing in the repository could answer "which branches carry work no pull request tracks?" — it took
a hand-built join of `git worktree list`, `git rev-list --count`, `gh pr list` and `pr-lane status`.
That gap is now the `task-status` command (§6).

## 3. Parity: the shell layer against its Go ports

| Finding | Evidence at the time | Resolution |
|---|---|---|
| The gate receipt — 421 lines plus a 276-line behaviour test — was the last rule in shell, and the gate kept a step whose whole body was that test | `scripts/gate-receipt.sh`, `scripts/test-gate-receipt.sh`; the `helper script behaviour` step | Ported to `internal/build/core/receipt` and `buildtool gate-receipt` (#441) |
| The required-check list had two owners: the shell's `suite_full` and `policy.VerifySuite()`, pinned by a test that parsed the shell file | `internal/build/policy/verify_test.go` | One owner after the port |
| The port left its callers switched but both shell scripts unreferenced in the tree, while `scripts/README.md` already said the rule had moved | the tree at that commit | Archived at parity, then deleted with the archive (#480) |
| **46 references named `internal/build/<pkg>`** at the path the engine had moved away from; three were *code*, rendered into the gate-checked package graph | `AGENTS.md`, `docs/index.md`, `docs/specs/*`, `scripts/*`, `website/*`, `internal/build/policy/graphdoc.go` | Rewritten to `internal/build/core/<pkg>`; the renderer and the graph moved together (#432) |
| The archive's headers named replacements at those same stale paths, and a prose claim that mermaid balance "stays a count in `scripts/verify.sh`" outlived the shim it named | five archived headers; `docs/specs/AGENT.md` | Fixed; the archive is deleted |
| `docs/specs/coordination.md`, normative, cited `policy.Worktrees().Ledger` — **a field that does not exist** — and listed "the coverage entries in `scripts/verify.sh`", a 19-line shim holding neither | `internal/build/policy/worktree.go`, `policy/gate.go`, `scripts/verify.sh` | Corrected to `policy.Gate().Ledger` and the real coverage table (#475) |
| `#461` was still titled around a false success this session had already fixed | `worktree remove`'s new refusal | Premise corrected on the issue before a lane was spent on it |

## 4. Orphans

- **Five stale lane refs** on the remote, naming branches whose pull requests had merged, closed or
  never existed — released.
- **A merged worktree** whose pull request had landed weeks of session-time earlier, and six local
  branches whose remotes were gone — removed.
- **The archive**: 13 scripts, ~2,100 lines, nothing referencing a byte of it — first kept at parity,
  then deleted as its own rule required (#480).
- **`scripts/` itself was never the problem**: five scripts remained and every one was reached. The
  parity audit concluded, correctly, that no script there was orphaned.

## 5. Process friction

| # | Finding | State |
|---|---|---|
| F1 | No single view of task state: nothing joined worktree → branch → commits ahead of `origin/main` → pull request → lane | **Fixed** — `buildtool task-status` (#469) |
| F2 | Stale lanes had no bulk reap | Partly: `pr-lane status` reports age and staleness; releasing stays per-lane and deliberate |
| F3 | The change playbook never mentioned the landing protocol — no claim, no worktree, no pull request | Reported |
| F4 | No conflict-risk signal; three of seven branches patched files the port had deleted | Mitigated by F1's report, which names the branches and whether a worktree holds them |
| F5 | `coordination.md` §5 makes holding the advisory slot a MUST for every gate run in a shared clone, and **nothing in the tooling takes or checks it** | **Open by decision** — enforcing it changes gate behaviour |
| F6 | `worktree remove` reported success for a name that existed nowhere | **Fixed** (#428) |
| F7 | Prose code-paths are unverified, so `internal/build/<pkg>` drift lived until an audit found it | Reported; a narrow check would have caught all 46 |

## 6. The instrument

`go run ./cmd/buildtool task-status` reads `git for-each-ref`, `git rev-list --count`,
`git status --porcelain`, `git worktree list --porcelain` and one `gh pr list`, and reports one line
per branch plus the states worth acting on. Its finding distinguishes the two kinds of unlanded
branch, which is the difference between a session that is working and work nobody is on:

```text
carries commits, no pull request:
  enhancement/387-…   2 commits ahead of the base ref and no pull request names it, and no worktree holds it
  chore/338-…         1 commit ahead of the base ref and no pull request names it (a worktree holds it)
```

The rule is `internal/build/core/taskstate` over caller data; go-cask's answers — the base ref, which
is the one `worktree add` branches from, and the listing limit — are `internal/build/policy`.

## 7. Landings

| Issue | What it was | PR |
|---|---|---|
| #349 | percent-encoded login deep link; no `cmd.exe` launcher | #414 |
| #362 | record write bounded by the read cap | #418 |
| #395 | `worktree remove` refuses a removal that removes nothing | #428 |
| #431 | `internal/build/core/` paths, renderer and regenerated graph | #432 |
| #417 | the gate receipt ported to Go | #441 |
| #338 | duplicated test helpers given one owner | #460 |
| #454 | `task-status` | #469 |
| #474 | `coordination.md` parity | #475 |
| #387 | a renewal that cannot clobber a claim | #472 |
| #387 | a holder judged alive, gone or unknown | #477 |
| #387 | `land-lane wait` | #479 |
| #480 | the archived shell scripts deleted | #481 |
| #482 | this document, and the scratch-tree rule | its own pull request |

## 8. What the errors taught

Eight errors were hit and reported rather than worked around. Five generalise:

- **The version line is a hot line.** Two branches bumping one versioned file to the same number:
  green locally, red in CI, because CI gates the merge result. Hit three times (`cli.md`,
  `scripts/AGENT.md`, `.github/AGENT.md`). The rule: rebuild on current `main` and take *main's
  number + 1*.
- **A green local gate is not a green CI gate.** WSL resolves `rundll32.exe` through interop, so a
  test that resolved a Windows launcher passed here and failed on the runner. The platform matrix is
  compile-only; a Windows-behaviour change needs `go test` on Windows too.
- **Commit, then gate, then push.** The stamp names a commit. Gating a working tree and then
  committing produces a stamp that authorises nothing, and the pre-push hook refuses — as it should.
- **`os.Rename` replaces its destination on every platform.** A renewal that checked the slot was
  free and then renamed its copy into place could silently destroy a claim made in between; the
  shell version's comment blamed Windows only, and the port inherited the assumption.
- **A scratch tree is not storage.** Worktrees moved out of `.gocache/` (#465) and this document's
  predecessor was lost inside it. `.gocache/` is the build cache; `.worktrees/` holds task worktrees.
  Both are cleaned, neither is tracked, and a record that must survive goes in a pull request.

Two more were self-inflicted and worth naming because they are easy to repeat: editing the **primary
checkout** (stale, shared, and forbidden to touch) because a file was addressed by absolute path
rather than through a task worktree; and pushing a branch whose commit had **never been gated**,
which published an empty branch pointing at the base commit.

## 9. What remains open

- **F5** — the advisory slot's MUST has no tooling behind it. Reported, not fixed.
- **F3/F7** — the change playbook still omits the landing protocol, and nothing verifies a backticked
  path. Both are reported rather than owned by this audit.
- The repository now carries `docs/specs/coordination.md` and the `coordinate` skill, written by
  another session: the process half of this audit's subject, specified in the right place.
