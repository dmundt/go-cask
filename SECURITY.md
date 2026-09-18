# Security policy

## Supported versions

Security fixes are made on the latest release and the `main` branch.

| Version | Supported |
| --- | --- |
| Latest release | Yes |
| `main` | Yes |
| Earlier releases | No |

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/dmundt/go-cask/security/advisories/new)
for suspected vulnerabilities. Do not open a public issue before a fix is
available.

Include affected versions, a minimal reproduction, impact, and any suggested
mitigation. Reports receive an acknowledgment within seven days. After a fix
is available, coordinate disclosure timing through the private report.

## Security controls

`main` accepts changes through reviewed pull requests only. Required CI,
CodeQL analysis, signed commits, stale-review dismissal, and resolved review
conversations protect merges. Secret scanning, push protection, Dependabot
alerts, and Dependabot security updates are enabled in GitHub.
