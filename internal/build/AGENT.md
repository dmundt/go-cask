---
type: Agent Instructions
title: Agent instructions — `internal/build`
description: Rule set for the build subtree — engine boundary, policy/engine split, nested-module trap that hides the engine from `go test ./...`, and where each new piece goes.
version: v6
---

# Agent instructions — `internal/build`

Gate's build decisions. Layout + commands: [`README.md`](./README.md). This file: rules
for changing it.

## Engine ships no policy

`core/` = engine; separate Go module; checks only. Never names this repository: no `cas/`,
no `gitlike/`, no `cmd/`, no go-cask path, no coverage tier, no inventory page, no prose.

Table = one repository's answer → lives in `policy/`; reaches engine as parameter. Engine
needs a new table kind → add the type in the engine, `policy` fills it. Never teach the
engine go-cask.

Property = engine serves a second repository. Lost by accident; everything compiles, gate
stays green.

## Engine imports standard library only

No `go.sum` → host repository drops it in without inheriting a dependency policy. New
import in `core/` = decision, not convenience.

## Nested-module trap

`core/go.mod` → `core/` own module. Go skips nested modules. From repo root:

- `go build ./...`, `go vet ./...`, `go test ./...` → engine invisible
- `gofmt -l .` → not format-checked
- `go mod tidy` → its `go.mod` not tidied
- layer matrix, coverage tier check → its packages invisible

Everything passes → engine rots, gate green. Therefore gate's `build engine module` step
runs the engine's build, vet, test by name + second `go mod tidy` in `core/`; `gofmt -l`
covers both trees. New engine check not covered there → runs nowhere.

## Where a new check goes

| Piece | Home | Rule |
|---|---|---|
| The rule | `core/<name>/` | pure function over caller data; stdlib only; ships no table |
| Unit test | `core/<name>/_test.go` | table-driven; no repository |
| go-cask table | `policy/` | matrix, tiers, guards, inventories, prose |
| Test against real tree | `policy/` | only if rule reads real repository state |
| Entry point | `cmd/buildtool` | reads repository, calls engine, prints verdict |
| Gate step | `cmd/buildtool verify`'s step list | one command per step; no rule of its own. `scripts/verify.sh` is the gate's name and starts it |

## Deliberate limits

- **Table as parameter**, never default. No `Default()`, no `Matrix()`, no `Inventories()`.
- **No registry, no reflection, no mutable global.** Explicit typed functions only.
- **Never merge `core/docs` and `core/versioning`.** Frontmatter parse → `docs` (one
  reader → no disagreement); `versioning` owns when a version moves. Split is the point.
- **Engine READMEs current.** Every `core/` package has one; linked from
  [`core/README.md`](./core/README.md) and [`README.md`](./README.md).
- **Every README here is versioned.** Frontmatter `type: Guide`, `title`, one-line
  `description`, `version` — the version-field rule judges only versioned files.
  `internal/build/policy`'s `PackageReadme` states it; `go test ./internal/build/policy/`
  enforces it → new package covered at directory creation. Material change → `version` +1
  (docs/AGENT.md §5); typo → no bump.

## Validation

```bash
(cd internal/build/core && go build ./... && go vet ./... && go test -race ./...)
go test ./internal/build/policy/
./scripts/verify.sh
```

Green run ends `verification passed`; earlier stop = failed.

## Fuzzing what the engine parses

Engine parses repository + tool input → fuzz targets beside the table tests:
`core/coverage`, `core/versioning`, `core/lane`, `core/claim`, `core/gate`,
`core/toolchain`, `core/changes`, `core/docs`, `core/verify`. Gate smoke-fuzzes them beside
the `cas` targets; engine suite
runs `-race` there (engine pure → detector keeps it pure). Failing input → kept in
`<package>/testdata/fuzz/`, committed → regression test. The set the gate runs is
`internal/build/policy`'s, and its tests pin both directions: every target it names exists
in that package, and no engine package carries a fuzz target the gate never smoke-fuzzes.
