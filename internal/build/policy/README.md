---
type: Guide
title: policy (build) — go-cask
description: go-cask's answers for the build engine — layer matrix, coverage tiers, guards, inventories, footer contract, change rules, README frontmatter, instruction-file budgets, worktree table, landing lanes, gate table, package-graph prose.
version: v11
---

# policy

go-cask's answers for the engine packages beside it, in [`../README.md`](../README.md).

Engine ships no table. Matrix, tier, prose = one repository's decision → held here. Change
to what the gate enforces → change one file here; the engine keeps taking it as a parameter.

## What it holds

| Decision | Function | Consumed by |
|---|---|---|
| Layers; what each may import | `Matrix` | `deps.Check` |
| Package → coverage tier | `Coverage` | tier check + measurement loop |
| Packages that must not reach the codec layer | `CodecGuards` | `deps.CheckCodecDeps` |
| Which paths a change set consists of; which jobs it can affect | `ScopeRules`, `DocsPaths`, `WebsitePaths` | `scope.Classify`; the `scope` command, which the gate and CI both ask |
| Frontmatter a package README under `internal/build` carries | `PackageReadme` | the build README check |
| Byte ceiling per instruction file | `InstructionBudgets` | the `markdown-integrity` step; the tree walk is `documentation_test.go` |
| Site pages promising an inventory table | `Inventories` | `website.CheckInventory` |
| Published footer: base line, hook, guards | `SiteFooter` | `website.FooterFindings`, `website.CheckSourceGuards`, `website.CheckAbsentPaths` |
| Package graph's title, prose, layer assignment | `GraphDoc` | `deps.Document` |
| Where that document lives | `GraphDocPath` | the `dep-graph` command |
| The gate's entry points and its verified-commit ledger | `Gate` | the `verify` and `pre-push` commands; `landing.Verified`, `landing.Append` |
| The gate's own layout: variables, escape hatches, cross-platform targets, smoke-fuzz set | `Verify` | the `verify` command |
| The pinned static analyzer: name, package, version, config | `Linter` | the `lint` command, and the gate's `lint` step |
| The local advisory slot: directory, records, idle window | `LandLane` | the `land-lane` command; `landing.Decide` |
| The server-side lane: ref namespace, claim window, record file | `PRLane` | the `pr-lane` command; `landing.DecideLane` |
| Task worktree location, base, lock text | `Worktrees` | the `worktree` command; `worktree.GitFile` |
| Benchmark capture command and archive naming | `Benchmarks`, `BenchmarkArchiveName` | the `bench-baseline` and `bench-compare` commands |
| Which example programs a runner executes | `Examples` | the `run-examples` command |
| The pinned vulnerability scanner and its release | `Scanner` | the `security` command; `toolchain.Installed` |

`ModulePath` = this repository's module path; every go-cask-relative prefix is built from
it.

## Tables are authoritative

Each states a rule a specification owns; the specification omits the list → change touches
one file:

- `Matrix` mirrors [`library-design.md`](../../../docs/specs/library-design.md) §1.1. Arm
  absent from the prose, or prose row absent here → drift.
- `Coverage` = gate's contract with
  [`testing-strategy.md`](../../../docs/specs/testing-strategy.md) §5. Threshold edit
  changes what the gate accepts → specification change, not refactor.
- `CodecGuards` = cas-core §4.12 (reference library names no wire format) + §7 (helper
  layer takes the caller's codec).
- `GraphDoc` grouping resolved like layer arms: narrower tree tested first; each claim
  excludes the layer it contains → no package drawn twice.

## Testing

`go test ./policy/` — two kinds:

- **tables themselves** — matrix covers every top-level tree; coverage policy validates
  and names no dropped package; every `cas/` package carries a tier.
- **real tree** — inventory tables match the site; committed graph = `GraphDoc` render;
  every directory under `internal/build` has a README its parent links to; the workflow's
  platform matrix gates the same targets `Verify` hands the cross-platform step; no tracked
  workflow spends a Windows or a macOS runner (#390).

Second kind reads the repository → slower than the engine's tests; only way to catch a
drifted table.
