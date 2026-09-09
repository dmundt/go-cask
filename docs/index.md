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
| `cas/hash.go`; `TestHash*` / `BenchmarkHash*` | [`cas-core.md`](specs/cas-core.md) §4.1–4.3 |
| `cas/backend.go` | [`cas-core.md`](specs/cas-core.md) §4.3–4.5 |
| `cas/store.go`, `codec.go`, `object.go`, `walker.go` | [`cas-core.md`](specs/cas-core.md) §4.6–4.12 |
| `cas/cached.go`, `lru.go` | [`cas-core.md`](specs/cas-core.md) §4.10 |
| `cas/maintenance.go`; `backend.go` `GC`/`Prune`/`Clean` | [`consistency.md`](specs/consistency.md) |
| `cmd/cask/` | [`cli.md`](specs/cli.md) |
| `internal/web/` (wiring, middleware, config) | [`backend-architecture.md`](specs/backend-architecture.md) |
| `internal/web/` (templates, htmx) | [`frontend-architecture.md`](specs/frontend-architecture.md) |
| `internal/web/` (sessions, CSRF, roles, audit) | [`viewer-security.md`](specs/viewer-security.md) |
| `internal/web/` (dashboard, objects, hexdump) | [`viewer-design.md`](specs/viewer-design.md) |
| `internal/index/` | [`cas-core.md`](specs/cas-core.md) §4 + [`examples.md`](specs/examples.md) §3.4 |
| `examples/gitlike/` | [`examples.md`](specs/examples.md) §2 + [`cas-core.md`](specs/cas-core.md) §4.12 |
| `examples/files/` | [`examples.md`](specs/examples.md) §3.1 |
| `examples/artifacts/` | [`examples.md`](specs/examples.md) §3.2 |
| `examples/notes/` | [`examples.md`](specs/examples.md) §3.3 |
| `examples/api/` | [`examples.md`](specs/examples.md) §3.4 + [`api-design.md`](specs/api-design.md) |
| `cas/errors.go`; any exported `cas.*` identifier | [`library-design.md`](specs/library-design.md) |
| `cas/*_test.go` | [`testing-strategy.md`](specs/testing-strategy.md) |
| `cas/bench_test.go`, `scale_bench_test.go` | [`performance.md`](specs/performance.md) + [`docs/perf/benchmarks.md`](perf/benchmarks.md) |
| `docs/specs/*.md` | [`docs/specs/AGENT.md`](specs/AGENT.md) |
| `.github/workflows/ci.yml` | [`AGENT.md`](specs/AGENT.md) §9 + [`testing-strategy.md`](specs/testing-strategy.md) §5 |
| Everything else | [`docs/specs/AGENT.md`](specs/AGENT.md) + root [`AGENTS.md`](/AGENTS.md) |

**Updating:** add/remove/re-target rows when rule files change. Bump version on material change.

**Sibling indexes:** [`docs/design/index.md`](design/index.md), [`docs/specs/index.md`](specs/index.md), [`docs/perf/index.md`](perf/index.md).
