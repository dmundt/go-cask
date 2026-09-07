---
title: go-cask Specification Set
description: Every instruction file in docs/instructions/, organized by concern. Match your topic to the file, then read it for the detailed convention. See docs/index.md for the path-first lookup table.
version: v1
---

# go-cask Specification Set

This folder contains the **normative specifications** of the go-cask
project — every component contract, design rule, and acceptance criterion.
[`docs/instructions/AGENT.md`](AGENT.md) is the meta-guide that governs them
all.

## Index by concern

| Concern | Spec file |
|---|---|
| Core library — every component contract, flows, concurrency | [`cas-core.md`](cas-core.md) |
| Lean-core budget, sentinel errors, API shape, compatibility | [`library-design.md`](library-design.md) |
| Coding style, no `any`, std-lib only, templates + htmx | [`coding-guidelines.md`](coding-guidelines.md) |
| GC, pruning, grace model, lock-free writers | [`consistency.md`](consistency.md) |
| CLI subcommands, flags, output, exit codes | [`cli.md`](cli.md) |
| Server-side architecture, viewer wiring, middleware, lifecycle | [`backend-architecture.md`](backend-architecture.md) |
| Browser-facing: hypermedia, htmx, URL-as-state | [`frontend-architecture.md`](frontend-architecture.md) |
| Viewer UI design, dashboard, hexdump, maintenance | [`viewer-design.md`](viewer-design.md) |
| Viewer security: authn/authz, sessions, CSRF, audit | [`viewer-security.md`](viewer-security.md) |
| Shared HTTP conventions, status codes, error mapping | [`api-design.md`](api-design.md) |
| Example program rules, five proposed examples | [`examples.md`](examples.md) |
| Extension contract, designed-but-deferred extensions | [`extensions.md`](extensions.md) |
| Performance: benchmarks, allocations, streaming hashing, CI gates | [`performance.md`](performance.md) |
| Testing: CAS laws, unit/property/fuzz/race, coverage gates | [`testing-strategy.md`](testing-strategy.md) |
| Production durability, fsync, observability, migration | [`operations.md`](operations.md) |
| Default values and constants (hash algo, fan-out, rate limits, …) | [`defaults.md`](defaults.md) |
| Object-model versioning, envelope format, type@major | [`object-versioning.md`](object-versioning.md) |
| Go module versioning, tags, branches, release process | [`versioning.md`](versioning.md) |
| Branch naming patterns and lifecycle | [`branch-naming.md`](branch-naming.md) |
| Meta-guide for this folder | [`AGENT.md`](AGENT.md) |

## Related

- [`docs/index.md`](../index.md) — top-level rule index (path-first lookup).
- [`docs/design/index.md`](../design/index.md) — design doc index.
- [`docs/AGENT.md`](../AGENT.md) — governance for docs outside this folder.