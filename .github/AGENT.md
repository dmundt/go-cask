---
type: Agent Instructions
title: GitHub repository operations
description: The rules for .github/ — branch protection and required checks, merge and secret-scanning settings, workflow least-privilege and action-pinning policy, and how to validate a settings change with the gh API.
version: v4
---

# GitHub repository operations

Directory contains repository automation and GitHub configuration
conventions. Keep GitHub settings, workflow files, and these rules aligned.

## Markdown policy

Raw HTML strictly forbidden in every Markdown file in this repository.
Use valid Markdown syntax only; never add HTML tags, comments, layout
wrappers, or embedded HTML blocks.

## Main branch

`main` accepts changes through pull requests only. Keep these protections
enabled:

- Require resolved review conversations, keep stale-review dismissal on.
- Require these status checks: `verify`, `security`, `platforms`,
  `Analyze (actions)`, and `Analyze (go)`. The two CodeQL contexts come from
  `.github/workflows/codeql.yml`, the repository's advanced setup, which is why
  its workflow must stay enabled (see "Code scanning"). Keep `strict` off: an
  up-to-date branch not required, because the land lane serializes landings
  instead (root `AGENTS.md`, "Serialized landing, worktrees and gates").
- Keep approving-review count at zero, approval-after-last-push off: in
  this single-account repository the pull-request author and the only possible
  reviewer are the same account, so a required approval would deadlock every
  pull request.
- Enforce protections for administrators, require signed commits, require
  linear history.
- Reject force-pushes and branch deletion.
- Never configure bypass allowances.

## Signed pull-request workflow

When signed commits are required, rebuild PR branches locally from current
`main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Before every
PR creation or update, run `./scripts/verify.sh` and confirm all configured
coverage thresholds pass. Enable auto-merge or merge only after signature
verification, required checks, and coverage checks pass.

On Windows, run that gate under WSL as described in
[`scripts/AGENT.md`](../scripts/AGENT.md): the race and coverage gate needs cgo
and a C compiler, which the Windows toolchain cannot take from WSL's `gcc`, and
coverage measured on Windows does not predict the gate.

## Merge and security settings

- Allow squash merges only. Keep merge commits and rebase merges disabled.
- Permit auto-merge only after branch protections pass. Delete merged head
  branches automatically.
- Require web commit signoff.
- Keep private vulnerability reporting, Dependabot alerts/security updates,
  secret scanning, and secret-scanning push protection enabled.
- GitHub currently reports secret-scanning non-provider patterns and validity
  checks as unavailable for this repository. Re-enable them if GitHub exposes
  support; never replace GitHub scanning with custom workflow logic.

## Workflow policy

- Grant workflows least-privilege permissions explicitly.
- Pin third-party actions to full commit SHAs with a readable version comment.
- Keep Dependabot updates configured for GitHub Actions, Go modules, and Python
  documentation dependencies.
- CI and CodeQL validate pull requests and support manual runs; CodeQL also
  performs its weekly scheduled scan. Protected `main` does not repeat the
  same validation after a checked pull request is merged.
- Scope security scans to Go- and security-relevant changes, platform jobs
  to Go-relevant changes. Keep native Windows amd64 and Linux arm64 coverage;
  the primary `verify` job already provides Linux amd64 build, race, test,
  coverage validation. Require the always-running `platforms` aggregate check
  so conditional matrix jobs still gate Go changes without blocking docs-only
  changes.
- Cancel superseded pull-request runs through workflow concurrency.
- Pages runs only for public documentation inputs.
- Keep required-check names synchronized with the branch protection settings
  when workflow job names change.
- The `verify` job may skip the suite it would otherwise run, but only when a
  signed local gate receipt covers the exact tree it is testing (see "Local gate
  receipts"). Whatever it decides, the job still reports: the required context is
  the job, never the step, and a skipped required check is a skipped landing gate.

## Local gate receipts

`scripts/verify.sh` runs the whole gate on the developer's host before a push,
`.githooks/pre-push` publishes that green run as a signed receipt commit under the
coordination ref `refs/gate/<head-sha>` (`scripts/gate-receipt.sh publish`). The
`verify` job verifies the receipt and skips only what the receipt covers, so the
merge gate is unchanged in *what* it accepts while the duplicated compute moves to
the host that already paid for it.

- **What CI checks before trusting a receipt.** The receipt commit's SSH signature
  verifies against [`.github/gate-signers`](./gate-signers); the receipt is built
  on the pull request's head commit (its parent) and carries the tree of the commit
  CI is testing (the merge result, so a branch that fell behind `main` gets the full
  gate instead of
  passing on a tree nobody gated); its base is an ancestor of both the head and the
  pull request's base; the diff hash recomputed in CI matches; its scope covers the
  change; it lists every check in `gate-receipt.sh`'s `suite_full`. Anything
  short of that runs the whole gate — a fork, an unsigned local gate, a missing or
  stale ref, a renamed gate section.
- **The signature is the anchor, the ref is not.** Push authority decided who
  could create the ref; the allow-list decides whose receipt may excuse a check.
  Rotating the signing key means adding its line to `.github/gate-signers`; until
  then CI simply runs the full gate, the safe direction.
- **The fast path is entered by evidence, never by its absence.** No step may be
  skipped because a receipt is missing, unreadable or malformed, and the required
  check names (`verify`, `security`, `platforms`, `Analyze (actions)`,
  `Analyze (go)`) stay what branch protection names — a skipped job must still
  report, or the landing gate disappears.

## Code scanning

CodeQL here is **advanced setup**: `.github/workflows/codeql.yml` runs two scoped
analyses and produces the required `Analyze (actions)` and `Analyze (go)`
contexts. Neither switched on by GitHub.

- **Keep the repository's default setup OFF**
  (`gh api repos/dmundt/go-cask/code-scanning/default-setup` must report
  `"state": "not-configured"`). Enabling it **disables this workflow** — GitHub
  deactivated it on 2026-09-22, seven minutes after default setup was
  configured, only symptom which analyses appeared — and it replaces
  the scoped analyses with unscoped ones: `actions` and `go` on every pull request
  and every push, plus `javascript-typescript` and `python` over
  `internal/web/htmx.min.js` and `website/javascripts/mermaid-10.9.5.min.js`
  (vendored, minified), two small site scripts, and `website/macros.py` — five
  files that have never produced a finding, at roughly two minutes of runner time
  per pull request and per push to `main`.
- **`codeql.yml` analyzes what changed, on both triggers.** A pull request is
  diffed against its base and a push to `main` against `github.event.before`; the
  actions analysis runs only when `.github/workflows/**` changed and the Go one
  only when a `.go`/`go.mod`/`go.sum` file changed. A scheduled or manual run has
  no base, so it analyzes everything.
- **The `push` trigger on `main` keeps the Security tab current.**
  CodeQL's own validation warns without it — default-branch alerts then
  refreshed only by the weekly scan. Its path filters keep a documentation merge
  from starting a run at all.
- Never add a language to this workflow without the same treatment: an
  analysis that cannot be scoped to a change belongs in the weekly scan, not in
  every pull request.

## Validation

After changing branch protection or repository settings, verify them:

```bash
gh api repos/dmundt/go-cask/branches/main/protection
gh api repos/dmundt/go-cask
gh api repos/dmundt/go-cask/private-vulnerability-reporting
gh api repos/dmundt/go-cask/automated-security-fixes
gh api repos/dmundt/go-cask/code-scanning/default-setup
gh workflow list --all
```
