---
type: Agent Instructions
title: GitHub repository operations
description: The rules for .github/ — branch protection and required checks, merge and secret-scanning settings, workflow least-privilege and action-pinning policy, local gate receipts, code scanning, and how to validate a settings change with the gh API.
version: v9
---

# GitHub repository operations

Repository automation and GitHub configuration. Keep settings, workflow files and
these rules aligned.

## Main branch

`main` accepts changes through pull requests only. Keep these protections enabled:

- Require resolved review conversations, keep stale-review dismissal on.
- Require these status checks: `verify`, `security`, `platforms`,
  `Analyze (actions)`, `Analyze (go)` — the two CodeQL contexts come from
  `.github/workflows/codeql.yml`, the repository's advanced setup, so its workflow
  must stay enabled ("Code scanning"). Keep `strict` off: the land lane serializes
  landings instead (`docs/specs/landing.md` §5).
- Keep approving-review count at zero, approval-after-last-push off: author and only
  possible reviewer are the same account, so a required approval deadlocks every PR.
- Enforce protections for administrators, require signed commits, require linear
  history.
- Reject force-pushes and branch deletion.
- Never configure bypass allowances.

## Issue labels

Labels come from this vocabulary. Each label's description and colour are repository
settings; add, rename or remove one with this section in the same pull request, and the
live repository MUST match the table (`gh label list`).

| Label | Meaning |
|---|---|
| `accessibility` | A barrier affecting people with disabilities |
| `agent-workflow` | Multi-session/agent landing coordination: the land lane, worktrees, gate stamps and their hooks |
| `api` | Public API design or contract |
| `bug` | Something is not working |
| `chore` | Maintenance or ergonomics work |
| `ci` | Build, gate and CI tooling |
| `dependencies` | A dependency-file update (Dependabot) |
| `documentation` | Improvements or additions to documentation |
| `duplicate` | This issue or pull request already exists |
| `enhancement` | A new feature or request |
| `example` | Example programs and usage examples (examples/, README snippets) |
| `github_actions` | A GitHub Actions update (Dependabot) |
| `good first issue` | Good for newcomers |
| `help wanted` | Extra attention is needed |
| `invalid` | This does not seem right |
| `parity` | A feature-parity gap across backends or the CLI |
| `performance` | A performance improvement |
| `python` | A Python documentation-dependency update (Dependabot) |
| `question` | Further information is requested |
| `security` | A security issue that needs attention |
| `viewer` | Embedded object-browser viewer: screens, layout, filters, inspector, styling |
| `wontfix` | This will not be worked on |

Additive: one type label (`bug`, `enhancement`, `documentation`, `chore`,
`performance`, `security`, `api`, `parity`) plus every area label that applies
(`viewer`, `accessibility`, `example`, `agent-workflow`, `ci`).

### The `viewer` label

Marks work whose subject is the embedded object browser:

- `internal/web/` — templates, the single stylesheet, the object table and its
  columns, filters, the inspector, reference states, integrity display;
- the `cask web` server surface — startup-token handling, login/session, request
  throttle, audit logging, response hygiene;
- `seed-preview` and the preview graph where the viewer is the consumer;
- the viewer specs and page — `docs/specs/viewer-design.md`,
  `docs/specs/viewer-security.md`.

Not for an issue that mentions the viewer among other surfaces: a cross-cutting
refactor, repository tooling or a website/docs change whose subject is elsewhere stays
unlabeled.

List the viewer backlog with
`gh issue list --state all --search 'label:viewer'`.

## Signed pull-request workflow

Rebuild PR branches locally, sign and verify each head commit, re-gate, push
force-with-lease: signed-commit workflow, WSL gate command and coverage thresholds are
`docs/specs/landing.md` §4 and `scripts/AGENT.md` "Running the scripts on Windows".

## Merge and security settings

- Allow squash merges only. Keep merge commits and rebase merges disabled.
- Permit auto-merge only after branch protections pass. Delete merged head branches
  automatically.
- Require web commit signoff.
- Never replace GitHub scanning with custom workflow logic: keep private vulnerability
  reporting, Dependabot alerts/security updates, secret scanning and secret-scanning
  push protection enabled, and re-enable non-provider patterns and validity checks
  once GitHub exposes them.

## Workflow policy

- Grant workflows least-privilege permissions explicitly.
- Pin third-party actions to full commit SHAs with a readable version comment.
- Keep Dependabot updates configured for GitHub Actions, Go modules and Python
  documentation dependencies (`.github/dependabot.yml`).
- CI and CodeQL validate pull requests and support manual runs; CodeQL additionally
  performs its weekly scheduled scan; protected `main` does not repeat the validation
  after a checked pull request is merged.
- Scope security scans to Go- and security-relevant changes, platform jobs to
  Go-relevant changes. Keep the platform matrix on Linux runners only: it cross-compiles
  and vets windows/amd64, darwin/amd64, darwin/arm64 and linux/arm64
  (`docs/specs/testing-strategy.md` §5), so no Windows and no macOS runner is paid for.
  Require the always-running `platforms` aggregate check so conditional matrix jobs
  still gate Go changes without blocking docs-only changes.
- Cancel superseded pull-request runs through workflow concurrency. Pages runs only for
  public documentation inputs.
- Keep required-check names synchronized with the branch protection settings when
  workflow job names change.
- The `verify` job may skip the suite it would otherwise run, but only when a signed
  local gate receipt covers the exact tree it is testing ("Local gate receipts"). The
  required context is the job, never the step: a skipped required check is a skipped
  landing gate.

## Local gate receipts

`scripts/verify.sh` runs the whole gate on the developer's host before a push;
`.githooks/pre-push` publishes that green run as a signed receipt commit under the
coordination ref `refs/gate/<head-sha>` (`scripts/gate-receipt.sh publish`). The
`verify` job verifies the receipt and skips only what it covers.

- **What CI checks before trusting a receipt.** The receipt commit's SSH signature
  verifies against `.github/gate-signers`; the receipt is built on the pull request's
  head commit (its parent) and carries the tree of the commit CI is testing, so a branch
  behind `main` gets the full gate instead of passing on a tree nobody gated; its base
  is an ancestor of both the head and the pull request's base; the diff hash recomputed
  in CI matches; its scope covers the change; it lists every check in
  `gate-receipt.sh`'s `suite_full`. A fork, an unsigned local gate, a missing or stale
  ref or a renamed gate step runs the whole gate.
- **The signature is the anchor, the ref is not.** Push authority decided who
  could create the ref; the allow-list decides whose receipt may excuse a check.
  Rotating the signing key means adding its line to `.github/gate-signers`; until
  then CI runs the full gate, the safe direction.
- **The fast path is entered by evidence, never by its absence.** No step may be
  skipped because a receipt is missing, unreadable or malformed, and the required
  check names (`verify`, `security`, `platforms`, `Analyze (actions)`, `Analyze (go)`)
  stay what branch protection names — a skipped job must still report, or the landing
  gate disappears.

## Code scanning

CodeQL here is **advanced setup**: `.github/workflows/codeql.yml` runs two scoped
analyses and produces the required `Analyze (actions)` and `Analyze (go)` contexts.

- **Keep the repository's default setup OFF**
  (`gh api repos/dmundt/go-cask/code-scanning/default-setup` must report
  `"state": "not-configured"`). Enabling it **disables this workflow** and replaces
  the scoped analyses with unscoped ones: `actions` and `go` on every pull request and
  every push, plus `javascript-typescript` and `python` over `internal/web/htmx.min.js`
  and `website/javascripts/mermaid-10.9.5.min.js` (vendored, minified), two small site
  scripts, and `website/macros.py`.
- **`codeql.yml` analyzes what changed, on both triggers**; path filters and scopes are
  its own.
- Never add a language without the same treatment: an analysis that cannot be scoped to
  a change belongs in the weekly scan, not in every pull request.

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
