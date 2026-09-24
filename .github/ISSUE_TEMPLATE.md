## Problem

What is wrong, missing, or stale, and who it affects. Link the rule, spec
section, or code path that shows it.

## Proposed change

The outcome you want, in one paragraph. Keep the title's prefix honest —
`feat`, `fix`, `docs`, `chore` — because it selects the branch type in
[`docs/specs/branch-naming.md`](../docs/specs/branch-naming.md) §2.

## Affected surface

- [ ] `cas/` — the generic core
- [ ] `cas/backend/*` or `cas/codec/*`
- [ ] `cmd/cask` or `internal/web/` — CLI and viewer
- [ ] `gitlike/`, `examples/`, or `benchmarks/`
- [ ] documentation — `docs/`, `website/`, or a Markdown file
- [ ] repository tooling — `scripts/` or `.github/`

## Evidence

The command and its output, the failing test, the spec section, or the code
path. A copy-pasteable transcript beats a description.

## Definition of done

- [ ] The rule this change follows is named ([`docs/index.md`](../docs/index.md) maps a path to its rule file).
- [ ] [`CHANGELOG.md`](../CHANGELOG.md) records the change when a library consumer, CLI user, operator, or the viewer can observe it.
- [ ] `./scripts/verify.sh` ends with `verification passed`.
- [ ] The branch is `<type>/<NNN>-<kebab>` for this issue and its head commit is signed.
