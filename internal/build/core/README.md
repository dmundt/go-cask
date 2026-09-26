---
type: Guide
title: build core — go-cask
description: Build engine — separate Go module, checks a repository gate runs, every table caller-supplied; package list, shape, usage, testing.
version: v5
---

# core

Build engine: separate Go module; the checks a gate runs; every table supplied by the
caller.

Stdlib only, no `go.sum`. Names no repository: no `cas/`, `gitlike/`, `cmd/`, no coverage
tier → reusable. go-cask's answers: `../policy`. Wiring entry point: `cmd/buildtool`.

## Packages

| Package | Rule | README |
|---|---|---|
| `changes` | which paths a change set touches; what that decides | [changes](./changes/README.md) |
| `gate` | gate stamp: verified-commit ledger the pre-push hook reads | [gate](./gate/README.md) |
| `lane` | landing-lane records: advisory slot, identity, staleness | [lane](./lane/README.md) |
| `claim` | server-side lane: coordination ref, issue/branch matching, the shared verdict | [claim](./claim/README.md) |
| `verify` | what a gate run covers, how many packages it builds at once, whether an escape hatch dropped a step | [verify](./verify/README.md) |
| `worktree` | relative `.git` link a linked worktree needs; lock protecting it | [worktree](./worktree/README.md) |
| `toolchain` | where a `go install`ed tool lands; whether the installed one is the pinned release | [toolchain](./toolchain/README.md) |
| `bench` | benchmark capture naming; which capture a fresh run compares against | [bench](./bench/README.md) |
| `examples` | which example programs a runner executes; which it never runs | [examples](./examples/README.md) |
| `layers` | which packages of a module may import which | [layers](./layers/README.md) |
| `coverage` | per-package coverage threshold; exempt packages | [coverage](./coverage/README.md) |
| `docs` | Markdown integrity: raw HTML, forbidden fences, dead links, mermaid balance, frontmatter, changelog structure | [docs](./docs/README.md) |
| `website` | site Go fences compile; inventory tables match tree; one-line footer pinned | [website](./website/README.md) |
| `deps` | no forbidden dependency; well-formed module graph | [deps](./deps/README.md) |
| `depgraph` | local package graph as committed Mermaid document | [depgraph](./depgraph/README.md) |
| `versioning` | changed versioned file moved its frontmatter `version:` | [versioning](./versioning/README.md) |
| `release` | changelog section → GitHub release notes; tag publish guards | [release](./release/README.md) |

## Shape

Same split per package → reusable:

- **rule = pure function over caller data** — package list, file list, changelog text,
  policy table;
- **caller reads the world** — `go list`, `git`, filesystem, `gh`;
- **engine ships no table, path, prose** — those are one repository's answers.

→ Check testable without a repository; importing the engine cannot inherit go-cask's
opinions.

## Using it

```bash
# Unit tests: toolchain only.
go test ./...

# Caller builds a table, asks for a verdict.
go build ./...
```

Consumed by path (`require` + local `replace` inside go-cask) → tagged and consumed alone
once it is a repository.

## Testing

Gate runs the suite under `-race` and smoke-fuzzes the input readers — measurement lines,
frontmatter, slot records, ledger line, scanner version report, changed path. Fuzzer
failure kept in `<package>/testdata/fuzz/` → re-runs as an ordinary test:

```bash
(cd internal/build/core && go test -race ./...)
(cd internal/build/core && go test -run=^$ -fuzz=FuzzField -fuzztime=5s ./versioning/)
```

## Adding a package

Pure functions; no table; stdlib only; table test beside it; README linked from the table
above. New table kind → declare the type here, caller fills it.

## See also

- [`../README.md`](../README.md) — subtree, policy split, command list
- [`../AGENT.md`](../AGENT.md) — rules for changing any of this
