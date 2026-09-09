---
okf_version: "0.2"
title: go-cask Specification Set
description: Index of every instruction file by concern. See docs/index.md for path-first lookup.
version: v2
---

# go-cask Specification Set

All files in this folder; [`AGENT.md`](AGENT.md) governs them. Start at [`docs/index.md`](../index.md) for path-first lookup.

| Concern | File |
|---|---|
| Core library (every component, flows, concurrency) | [`cas-core.md`](cas-core.md) |
| Lean-core budget, errors, API shape, compat | [`library-design.md`](library-design.md) |
| Coding style (no `any`, std-lib, templates+htmx) | [`coding-guidelines.md`](coding-guidelines.md) |
| GC, pruning, grace model, lock-free writers | [`consistency.md`](consistency.md) |
| CLI (subcommands, flags, output, exit codes) | [`cli.md`](cli.md) |
| Server-side (viewer wiring, middleware, lifecycle) | [`backend-architecture.md`](backend-architecture.md) |
| Browser-facing (hypermedia, htmx, URL-as-state) | [`frontend-architecture.md`](frontend-architecture.md) |
| Viewer UI (dashboard, hexdump, maintenance) | [`viewer-design.md`](viewer-design.md) |
| Viewer security (authn/authz, sessions, CSRF, audit) | [`viewer-security.md`](viewer-security.md) |
| HTTP conventions (status codes, errors, streaming) | [`api-design.md`](api-design.md) |
| Example rules + proposed examples | [`examples.md`](examples.md) |
| Extension contract + deferred catalog | [`extensions.md`](extensions.md) |
| Performance (benchmarks, allocations, CI gates) | [`performance.md`](performance.md) |
| Testing (CAS laws, fuzz, race, coverage gates) | [`testing-strategy.md`](testing-strategy.md) |
| Operations (durability, fsync, observability) | [`operations.md`](operations.md) |
| Defaults (constants, hash algo, fan-out, limits) | [`defaults.md`](defaults.md) |
| Object versioning (envelope, type@major) | [`object-versioning.md`](object-versioning.md) |
| Go module versioning (tags, branches, releases) | [`versioning.md`](versioning.md) |
| Branch naming patterns | [`branch-naming.md`](branch-naming.md) |
| Meta-guide for this folder | [`AGENT.md`](AGENT.md) |
