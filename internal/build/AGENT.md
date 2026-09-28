---
type: Agent Instructions
title: Agent instructions — `internal/build`
description: Rule set for the build subtree — engine/policy split, where each new piece goes, README and versioning duties, fuzz coverage.
version: v11
---

# Agent instructions — `internal/build`

Gate's build decisions. Layout + commands: [`README.md`](./README.md). This file: rules
for changing it.

## Engine ships no policy

Engine = every package here except `policy/`; checks only. Never names this repository: no
`cas/`, no `gitlike/`, no `cmd/`, no go-cask path, no coverage tier, no inventory page, no
prose.

Table = one repository's answer → `policy/`; reaches the engine as a parameter. Engine needs
a new table kind → add the type in the engine, `policy` fills it. Never teach the engine
go-cask.

## Engine imports standard library only

A third-party import in an engine package is a decision under
[`coding-guidelines.md`](../docs/specs/coding-guidelines.md) §3, never a convenience.
`depguard` in [`.golangci.yml`](../../.golangci.yml) is a second signal for the import
boundary; `layer-matrix` owns it.

## One module

Root `./...` reaches every package here: build, vet, test, `gofmt -l .`, `go mod tidy`, the
layer matrix and the coverage tiers. A check covered by nothing else must name itself in the
gate's step list — say so where it is added.

## Where a new check goes

| Piece | Home | Rule |
|---|---|---|
| The rule | `<name>/` | pure function over caller data; stdlib only; ships no table |
| Unit test | `<name>/_test.go` | table-driven; no repository |
| go-cask table | `policy/` | matrix, tiers, guards, inventories, prose |
| Test against real tree | `policy/` | only if the rule reads real repository state |
| Entry point | `cmd/gate` | reads the repository, calls the rule, prints the verdict |
| Gate step | `cmd/gate verify`'s step list | one command per step; no rule of its own. `scripts/verify.sh` is the gate's name and starts it |

## Deliberate limits

- **Table as parameter**, never default. No `Default()`, no `Matrix()`, no `Inventories()`.
- **No registry, no reflection, no mutable global.** Explicit typed functions only.
- **Never merge `docs` and `versioning`.** Frontmatter parse → `docs` (one reader → no
  disagreement); `versioning` owns when a version moves.
- **Package READMEs current**, one per package, linked from [`README.md`](./README.md).
- **Every README here is versioned.** Frontmatter `type: Guide`, `title`, one-line
  `description`, `version`; the version-field rule judges only versioned files.
  `internal/build/policy`'s `PackageReadme` states it, and
  `go test ./internal/build/policy/` enforces it → a new package is covered at directory
  creation. Material change → `version` +1 (docs/AGENT.md §5); typo → no bump.

## Validation

```bash
go test -race ./internal/build/...
./scripts/verify.sh
```

A green run ends `verification passed`; an earlier stop is a failure.

## Fuzzing what the engine parses

Fuzz targets beside the table tests: `coverage`, `versioning`, `lane`, `claim`, `gate`,
`toolchain`, `changes`, `docs`, `verify`, `receipt`. The gate smoke-fuzzes them beside the
`cas` targets; a failing input kept in `<package>/testdata/fuzz/` is committed → regression
test. The set the gate runs is `internal/build/policy`'s, and its tests pin both directions:
every target it names exists in that package, and no engine package carries a fuzz target the
gate never smoke-fuzzes. `board` parses forge and diff text and ships none: its reading is
covered by fixtures, which a target would only reproduce.
