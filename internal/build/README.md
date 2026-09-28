---
type: Guide
title: build — go-cask
description: The gate's build decisions — engine checks, go-cask policy for them — plus layout, commands, and where a new check goes.
version: v9
---

# build

The gate's build decisions. Two parts, one module:

- **engine** — the checks, one package each; every table a parameter. Names no repository:
  no `cas/`, no `gitlike/`, no coverage tier.
- **policy** ([`./policy/README.md`](./policy/README.md)) — go-cask's answers: layer
  matrix, coverage tiers, codec guards, inventory tables, footer contract, change-set
  classification, package-graph prose.

`cmd/buildtool` wires engine + go-cask tables → gate step = one
`go run ./cmd/buildtool <command>` call. Command list, exit-status contract:
[`cmd/buildtool/README.md`](../../cmd/buildtool/README.md).

## Layout

| Path | What it is |
|---|---|
| [`changes/`](./changes/README.md) | change-set classification: docs-only, Go, security-relevant, website |
| [`gate/`](./gate/README.md) | gate stamp: verified-commit ledger the pre-push hook reads |
| [`lane/`](./lane/README.md) | landing-lane records: advisory slot, identity, staleness |
| [`claim/`](./claim/README.md) | server-side lane: coordination ref, issue/branch matching, the shared verdict |
| [`verify/`](./verify/README.md) | what a gate run covers, how many packages it builds at once, whether an escape hatch dropped a step |
| [`receipt/`](./receipt/README.md) | the gate receipt's format: parse, render, check-name rule, the evidence identity, the canonical changed-path list |
| [`worktree/`](./worktree/README.md) | relative `.git` link a linked worktree needs; lock protecting it |
| [`toolchain/`](./toolchain/README.md) | where an installed tool lands; whether it is the pinned release |
| [`layers/`](./layers/README.md) | dependency-layer check: arm table + allowed imports |
| [`coverage/`](./coverage/README.md) | coverage policy: tiers, thresholds, exemptions, measurement decision |
| [`docs/`](./docs/README.md) | Markdown integrity: raw HTML, forbidden fences, dead links, mermaid balance, frontmatter, changelog structure |
| [`website/`](./website/README.md) | published site: Go fences materialized + built, inventory tables vs tree, one-line footer |
| [`deps/`](./deps/README.md) | forbidden transitive dependencies; well-formed module graph |
| [`depgraph/`](./depgraph/README.md) | local package graph as committed Mermaid document |
| [`versioning/`](./versioning/README.md) | frontmatter `version:` rule for changed files |
| [`release/`](./release/README.md) | changelog sections → GitHub release notes; publish guards |
| [`taskstate/`](./taskstate/README.md) | which branches carry work no pull request tracks |
| [`bench/`](./bench/README.md) | benchmark capture naming; which capture a fresh run compares against |
| [`examples/`](./examples/README.md) | which example programs a runner executes; which it never runs |
| `policy/` | [go-cask tables + prose](./policy/README.md) for the engine |

## Shape

Same split per package:

- **rule = pure function over caller data** — package list, file list, changelog text,
  policy table;
- **caller reads the world** — `go list`, `git`, filesystem, `gh`;
- **the engine ships no table, path, prose** — those are one repository's answers.

→ A check is testable without a repository, and the engine cannot acquire go-cask's opinions
by accident.

## Commands

Each prints findings; non-zero exit when the rule fails → gate step = call + status check.

```bash
go run ./cmd/buildtool verify               # the gate: every step, in order
go run ./cmd/buildtool layer-matrix         # imports against the layer table
go run ./cmd/buildtool coverage-tier        # every cas/ package carries a tier
go run ./cmd/buildtool coverage-tier --list # the gate's measurement table
go run ./cmd/buildtool coverage-check       # thresholds, one line per package on stdin
go run ./cmd/buildtool markdown-integrity   # every tracked .md
go run ./cmd/buildtool website-examples     # the site's Go fences and inventory tables
go run ./cmd/buildtool website-footer       # the site's one-line footer and its self-test
go run ./cmd/buildtool codec-guards         # gitlike and cas/pack stay codec-free
go run ./cmd/buildtool module-graph         # go list -m names this module
go run ./cmd/buildtool dep-graph            # the committed graph is current
go run ./cmd/buildtool dep-graph --write    # rewrite it (the only writing mode)
go run ./cmd/buildtool version-fields --base <rev> [paths...]
go run ./cmd/buildtool release --tag <tag> [--from <prev>] [--publish]
```

## Adding a check

1. Rule → its own package here: pure function over caller data; ships no table, path, prose.
2. go-cask's answer → `policy/`.
3. `buildtool` subcommand: reads repository (`go list`, file list, git call), calls the rule.
4. Call from the gate's step list in `cmd/buildtool verify` (one command per step, no rule
   of its own). Nothing calls the step list by name: `scripts/verify.sh` is the gate's
   entry point and starts it.
5. Table test beside the rule; reads real repository state → also one in `policy/` vs tree.
6. New package? `go run ./cmd/buildtool dep-graph --write`. The committed graph lists every
   package in the module and the gate pins it byte-for-byte, so a package added without this
   step fails `TestGraphDocRendersTheCommittedDocument` — and only after a whole gate run.

## Testing

One module → one command:

```bash
go test ./internal/build/...   # the engine and this repository's policy
```

Root `./...` reaches every package here, so the gate's race suite covers the engine with no
step of its own. Smoke-fuzz targets are `policy.Verify().Fuzz`; a fuzzer failure kept in
`<package>/testdata/fuzz/` re-runs as an ordinary test.
