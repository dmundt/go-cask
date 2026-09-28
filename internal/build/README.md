---
type: Guide
title: build — go-cask
description: The gate's build decisions — engine checks, go-cask policy for them — plus layout, commands, and where a new check goes.
version: v13
---

# build

The gate's build decisions. One module, two parts:

- **engine** — the checks, one package each; every table a parameter. Names no repository:
  no `cas/`, no `gitlike/`, no coverage tier.
- **policy** ([`./policy/README.md`](./policy/README.md)) — go-cask's answers: layer
  matrix, coverage tiers, codec guards, inventory tables, footer contract, change-set
  classification, package-graph prose.

`cmd/gate` wires engine + go-cask tables → gate step = one
`go run ./cmd/gate <command>` call. Command list, exit-status contract:
[`cmd/gate/README.md`](../../cmd/gate/README.md).

## Layout

| Path | What it is |
|---|---|
| [`landing/`](./landing/README.md) | the landing machinery: server-side lane, local advisory slot, gate stamp, gate receipt |
| [`scope/`](./scope/README.md) | change-set classification; gate run scope, package concurrency, escape hatches |
| [`worktree/`](./worktree/README.md) | relative `.git` link a linked worktree needs; lock protecting it |
| [`toolchain/`](./toolchain/README.md) | where an installed tool lands; whether it is the pinned release |
| [`coverage/`](./coverage/README.md) | coverage policy: tiers, thresholds, exemptions, measurement decision |
| [`docs/`](./docs/README.md) | Markdown integrity: raw HTML, forbidden fences, dead links, mermaid balance, frontmatter, changelog structure |
| [`website/`](./website/README.md) | published site: Go fences materialized + built, inventory tables vs tree, one-line footer |
| [`deps/`](./deps/README.md) | import structure: forbidden dependencies, dependency-layer matrix, committed package graph |
| [`versioning/`](./versioning/README.md) | frontmatter `version:` rule for changed files |
| [`release/`](./release/README.md) | changelog sections → GitHub release notes; publish guards |
| [`taskstate/`](./taskstate/README.md) | which branches carry work no pull request tracks |
| [`board/`](./board/README.md) | the coordinator's readings: the board, the file-overlap matrix, the six landing checks |
| [`bench/`](./bench/README.md) | benchmark capture naming; which capture a fresh run compares against |
| [`examples/`](./examples/README.md) | which example programs a runner executes; which it never runs |
| `policy/` | [go-cask tables + prose](./policy/README.md) for the engine |

## Shape

Rule = pure function over caller data (package list, file list, changelog text, policy
table); caller reads the world (`go list`, `git`, filesystem, `gh`); engine ships no table,
path or prose. → A check is testable without a repository.

## Commands

Each prints findings; non-zero exit when the rule fails → gate step = call + status check.

```bash
go run ./cmd/gate verify               # the gate: every step, in order
go run ./cmd/gate layer-matrix         # imports against the layer table
go run ./cmd/gate coverage-tier        # every cas/ package carries a tier
go run ./cmd/gate coverage-tier --list # the gate's measurement table
go run ./cmd/gate coverage-check       # thresholds, one line per package on stdin
go run ./cmd/gate markdown-integrity   # every tracked .md
go run ./cmd/gate website-examples     # the site's Go fences and inventory tables
go run ./cmd/gate website-footer       # the site's one-line footer and its self-test
go run ./cmd/gate codec-guards         # gitlike and cas/pack stay codec-free
go run ./cmd/gate module-graph         # go list -m names this module
go run ./cmd/gate dep-graph            # the committed graph is current
go run ./cmd/gate dep-graph --write    # rewrite it (the only writing mode)
go run ./cmd/gate version-fields --base <rev> [paths...]
go run ./cmd/gate release --tag <tag> [--from <prev>] [--publish]
```

## Adding a check

1. Rule → its own package here: pure function over caller data; ships no table, path, prose.
2. go-cask's answer → `policy/`.
3. `gate` subcommand: reads repository (`go list`, file list, git call), calls the rule.
4. From the gate's step list in `cmd/gate verify` (one command per step, no rule of its
   own); `scripts/verify.sh` is the entry point that starts it.
5. Table test beside the rule; reads real repository state → also one in `policy/` vs tree.
6. New package? `go run ./cmd/gate dep-graph --write`. The gate pins the committed graph
   byte-for-byte, so a package added without this fails
   `TestGraphDocRendersTheCommittedDocument` — and only after a whole gate run.

## Testing

```bash
go test ./internal/build/...   # the engine and this repository's policy
```

Root `./...` reaches every package here → the gate's race suite covers the engine with no
step of its own. Smoke-fuzz targets: `policy.Verify().Fuzz`; a failure kept in
`<package>/testdata/fuzz/` re-runs as an ordinary test.
