---
type: Guide
title: build — go-cask
description: The gate's build decisions — engine module, go-cask policy for it, archived shells — plus layout, commands, and where a new check goes.
version: v5
---

# build

The gate's build decisions. Three parts, separate:

- **[core](./core/README.md)** — build engine; *separate Go module*; own `go.mod`; no
  dependency beyond stdlib; versioned on its own. Reusable half: checks only, every table
  a parameter.
- **policy** ([`./policy/README.md`](./policy/README.md)) — go-cask's answers: layer
  matrix, coverage tiers, codec guards, inventory tables, footer contract, change-set
  classification, package-graph prose. Not reusable; another repository writes its own.
- **shell** ([`./shell/README.md`](./shell/README.md)) — scripts the engine replaced,
  archived at parity.

`cmd/buildtool` wires engine + go-cask tables → gate step = one
`go run ./cmd/buildtool <command>` call. Command list, exit-status contract:
[`cmd/buildtool/README.md`](../../cmd/buildtool/README.md).

## Why a separate module

Serves a second repository. Boundary makes it fact, not intention: engine cannot reach
`cas/`, `gitlike/`, `cmd/`; no inherited dependency; tagged and consumed alone.

Root module reaches it via `require` + local-path `replace`. Engine becomes own repository
→ only that line changes.

## Layout

| Path | What it is |
|---|---|
| [`core/`](./core/README.md) | engine module — seventeen packages, stdlib only |
| [`core/changes/`](./core/changes/README.md) | change-set classification: docs-only, Go, security-relevant, website |
| [`core/gate/`](./core/gate/README.md) | gate stamp: verified-commit ledger the pre-push hook reads |
| [`core/lane/`](./core/lane/README.md) | landing-lane records: advisory slot, identity, staleness |
| [`core/claim/`](./core/claim/README.md) | server-side lane: coordination ref, issue/branch matching, the shared verdict |
| [`core/verify/`](./core/verify/README.md) | what a gate run covers, how many packages it builds at once, whether an escape hatch dropped a step |
| [`core/worktree/`](./core/worktree/README.md) | relative `.git` link a linked worktree needs; lock protecting it |
| [`core/toolchain/`](./core/toolchain/README.md) | where an installed tool lands; whether it is the pinned release |
| [`core/layers/`](./core/layers/README.md) | dependency-layer check: arm table + allowed imports |
| [`core/coverage/`](./core/coverage/README.md) | coverage policy: tiers, thresholds, exemptions, measurement decision |
| [`core/docs/`](./core/docs/README.md) | Markdown integrity: raw HTML, forbidden fences, dead links, mermaid balance, frontmatter, changelog structure |
| [`core/website/`](./core/website/README.md) | published site: Go fences materialized + built, inventory tables vs tree, one-line footer |
| [`core/deps/`](./core/deps/README.md) | forbidden transitive dependencies; well-formed module graph |
| [`core/depgraph/`](./core/depgraph/README.md) | local package graph as committed Mermaid document |
| [`core/versioning/`](./core/versioning/README.md) | frontmatter `version:` rule for changed files |
| [`core/release/`](./core/release/README.md) | changelog sections → GitHub release notes; publish guards |
| [`core/bench/`](./core/bench/README.md) | benchmark capture naming; which capture a fresh run compares against |
| [`core/examples/`](./core/examples/README.md) | which example programs a runner executes; which it never runs |
| `policy/` | [go-cask tables + prose](./policy/README.md) for the engine |
| `shell/` | [scripts the engine replaced](./shell/README.md), archived at parity |

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

1. Rule → `core/`: pure function over caller data; stdlib only; ships no table, path, prose.
2. go-cask's answer → `policy/`.
3. `buildtool` subcommand: reads repository (`go list`, file list, git call), calls engine.
4. Call from the gate's step list in `cmd/buildtool verify` (one command per step, no rule
   of its own). Nothing calls the step list by name: `scripts/verify.sh` is the gate's
   entry point and starts it.
5. Table test in `core/`; reads real repository state → also one in `policy/` vs real tree.

## Testing

Separate modules → separate commands:

```bash
(cd internal/build/core && go test ./...)   # the engine
go test ./internal/build/policy/            # this repository's policy
```

Root `./...` skips the engine (nested module) → gate names it in its
`build engine module` step. Engine check without that step → runs nowhere.
