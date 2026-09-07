---
okf_version: "0.2"
title: go-cask Rules Index
description: Path-first rule lookup. Match file path -> spec file -> detailed rules. Short enough for starting instructions.
version: v3
---

# go-cask Rules Index

**Four-step lookup:**
1. Identify file being edited.
2. Match its path against first column (longest match wins).
3. Read file named in second column.
4. Apply rules while planning and editing.

| Path | Rule file |
|---|---|
| `cas/hash.go`; `TestHash*` / `BenchmarkHash*` | [`cas-core.md`](instructions/cas-core.md) §4.1–4.3 |
| `cas/fsstore.go`, `memstore.go`, `rawstore.go` | [`cas-core.md`](instructions/cas-core.md) §4.3–4.5 |
| `cas/store.go`, `codec.go`, `object.go`, `walker.go` | [`cas-core.md`](instructions/cas-core.md) §4.6–4.12 |
| `cas/cached.go`, `lru.go` | [`cas-core.md`](instructions/cas-core.md) §4.10 |
| `cas/maintenance.go`; `fsstore.go` `GC`/`Prune`/`Clean` | [`consistency.md`](instructions/consistency.md) |
| `cmd/cask/` | [`cli.md`](instructions/cli.md) |
| `internal/web/` (wiring, middleware, config) | [`backend-architecture.md`](instructions/backend-architecture.md) |
| `internal/web/` (templates, htmx) | [`frontend-architecture.md`](instructions/frontend-architecture.md) |
| `internal/web/` (sessions, CSRF, roles, audit) | [`viewer-security.md`](instructions/viewer-security.md) |
| `internal/web/` (dashboard, objects, hexdump) | [`viewer-design.md`](instructions/viewer-design.md) |
| `internal/index/` | [`cas-core.md`](instructions/cas-core.md) §4 + [`examples.md`](instructions/examples.md) §3.4 |
| `examples/gitlike/` | [`examples.md`](instructions/examples.md) §2 + [`cas-core.md`](instructions/cas-core.md) §4.12 |
| `examples/files/` | [`examples.md`](instructions/examples.md) §3.1 |
| `examples/artifacts/` | [`examples.md`](instructions/examples.md) §3.2 |
| `examples/notes/` | [`examples.md`](instructions/examples.md) §3.3 |
| `examples/api/` | [`examples.md`](instructions/examples.md) §3.4 + [`api-design.md`](instructions/api-design.md) |
| `cas/errors.go`; any exported `cas.*` identifier | [`library-design.md`](instructions/library-design.md) |
| `cas/*_test.go` | [`testing-strategy.md`](instructions/testing-strategy.md) |
| `cas/bench_test.go`, `scale_bench_test.go` | [`performance.md`](instructions/performance.md) + [`docs/perf/benchmarks.md`](performance/benchmarks.md) |
| `docs/specs/*.md` | [`docs/specs/AGENT.md`](instructions/AGENT.md) |
| `.github/workflows/ci.yml` | [`AGENT.md`](instructions/AGENT.md) §9 + [`testing-strategy.md`](instructions/testing-strategy.md) §5 |
| Everything else | [`docs/specs/AGENT.md`](instructions/AGENT.md) + root [`AGENTS.md`](/AGENTS.md) |

**Updating:** add/remove/re-target rows when rule files change. Bump version on material change.

**Sibling indexes:** [`docs/design/index.md`](design/index.md), [`docs/specs/index.md`](instructions/index.md), [`docs/perf/index.md`](performance/index.md).
