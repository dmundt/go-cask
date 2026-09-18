# GitHub repository operations

This directory contains repository automation and GitHub configuration
conventions. Keep GitHub settings, workflow files, and these rules aligned.

## Main branch

`main` accepts changes through pull requests only. Keep these protections
enabled:

- Require one approving review, dismiss stale approvals, and require approval
  after the last push.
- Require resolved review conversations.
- Require current (`strict`) status checks: `verify`, `security`,
  `linux-amd64`, `linux-arm64`, `windows-amd64`, `Analyze (actions)`, and
  `Analyze (go)`.
- Enforce protections for administrators, require signed commits, and require
  linear history.
- Reject force-pushes and branch deletion.
- Do not configure bypass allowances.

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
- CI runs on code changes; Pages runs only for public documentation inputs.
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
