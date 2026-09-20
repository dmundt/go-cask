# GitHub repository operations

This directory contains repository automation and GitHub configuration
conventions. Keep GitHub settings, workflow files, and these rules aligned.

## Markdown policy

Raw HTML is strictly forbidden in every Markdown file in this repository.
Use valid Markdown syntax only; do not add HTML tags, comments, layout
wrappers, or embedded HTML blocks.

## Main branch

`main` accepts changes through pull requests only. Keep these protections
enabled:

- Require one approving review, dismiss stale approvals, and require approval
  after the last push.
- Require resolved review conversations.
- Require current (`strict`) status checks: `verify`, `security`,
  `linux-arm64`, `windows-amd64`, `Analyze (actions)`, and `Analyze (go)`.
- Enforce protections for administrators, require signed commits, and require
  linear history.
- Reject force-pushes and branch deletion.
- Do not configure bypass allowances.

## Signed pull-request workflow

When signed commits are required, rebuild PR branches locally from current
`main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Before every
PR creation or update, run `./scripts/verify.sh` and confirm all configured
coverage thresholds pass. Enable auto-merge or merge only after signature
verification, required checks, and coverage checks pass.

## Merge and security settings

- Allow squash merges only. Keep merge commits and rebase merges disabled.
- Permit auto-merge only after branch protections pass. Delete merged head
  branches automatically.
- Require web commit signoff.
- Keep private vulnerability reporting, Dependabot alerts/security updates,
  secret scanning, and secret-scanning push protection enabled.
- GitHub currently reports secret-scanning non-provider patterns and validity
  checks as unavailable for this repository. Re-enable them if GitHub exposes
  support; do not replace GitHub scanning with custom workflow logic.

## Workflow policy

- Grant workflows least-privilege permissions explicitly.
- Pin third-party actions to full commit SHAs with a readable version comment.
- Keep Dependabot updates configured for GitHub Actions, Go modules, and Python
  documentation dependencies.
- CI and CodeQL validate pull requests and support manual runs; CodeQL also
  performs its weekly scheduled scan. Protected `main` does not repeat the
  same validation after a checked pull request is merged.
- Scope security scans to Go- and security-relevant changes, and platform jobs
  to Go-relevant changes. Keep native Windows amd64 and Linux arm64 coverage;
  the primary `verify` job already provides Linux amd64 build, race, test, and
  coverage validation.
- Cancel superseded pull-request runs through workflow concurrency.
- Pages runs only for public documentation inputs.
- Keep required-check names synchronized with the branch protection settings
  when workflow job names change.

## Validation

After changing branch protection or repository settings, verify them:

```bash
gh api repos/dmundt/go-cask/branches/main/protection
gh api repos/dmundt/go-cask
gh api repos/dmundt/go-cask/private-vulnerability-reporting
gh api repos/dmundt/go-cask/automated-security-fixes
```
