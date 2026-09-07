---
title: go-cask Rules Index
description: Quick-reference index of every specification file in docs/instructions/. Match the path you are working on to the corresponding rule file, then read that file for the detailed convention. Updated whenever a rule file is added, removed, or materially changed.
version: v2
---

# go-cask Rules Index

Match the **path or file pattern** you are working on to the rule file, then
read that file for the full convention. The table is ordered by specificity:
longer, more specific paths first. When multiple patterns match, use the
first match.

| Path / pattern | Rule file | Scope |
|---|---|---|
| `cas/hash.go`, `*_test.go` matching `TestHash*` or `BenchmarkHash*` | [`docs/instructions/cas-core.md`](instructions/cas-core.md) §4.1–4.3 | Hash type, `HashFunc`, `ParseHash`, `RegisterHash`, `HashBytes`, `NewHasher` |
| `cas/fsstore.go`, `cas/memstore.go`, `cas/rawstore.go` | [`docs/instructions/cas-core.md`](instructions/cas-core.md) §4.3–4.5 | `RawStore` interface, `FSRawStore` (fan-out, atomic writes, durability), `MemoryRawStore` |
| `cas/store.go`, `cas/codec.go`, `cas/object.go`, `cas/walker.go` | [`docs/instructions/cas-core.md`](instructions/cas-core.md) §4.6–4.12 | Typed layer: `Store[T]`, `Codec[T]`, `Object[T]`, `Walker[T]`, envelope format, write path |
| `cas/cached.go`, `cas/lru.go` | [`docs/instructions/cas-core.md`](instructions/cas-core.md) §4.10 | Caching layer: `CachedStore[T]`, `CachedObject[T]`, `LRUCache[T]`, `CacheMetrics` |
| `cas/maintenance.go`, `cas/fsstore.go` lines for `GC`/`Prune`/`Clean` | [`docs/instructions/consistency.md`](instructions/consistency.md) | GC from roots, age-based pruning, grace model, lock-free writers |
| `cmd/cask/` (any file) | [`docs/instructions/cli.md`](instructions/cli.md) | CLI subcommands, flags, output format, exit codes, store lock, `cask web` |
| `internal/web/`, `cmd/cask/web.go` | [`docs/instructions/backend-architecture.md`](instructions/backend-architecture.md) | Server-side architecture: viewer wiring, middleware, config, lifecycle, deployment |
| `internal/web/` — templates, htmx | [`docs/instructions/frontend-architecture.md`](instructions/frontend-architecture.md) | Browser-facing: hypermedia rendering, `html/template` nesting, htmx, URL-as-state |
| `internal/web/` — sessions, CSRF, roles, audit | [`docs/instructions/viewer-security.md`](instructions/viewer-security.md) | Viewer security: authn/authz, sessions, CSRF, rate limiting, audit logging, startup token |
| `internal/web/` — dashboard, object list, detail, hexdump | [`docs/instructions/viewer-design.md`](instructions/viewer-design.md) | Viewer UI design: dashboard, object inspection, maintenance actions, byte-layer tool |
| `internal/index/` | [`docs/instructions/cas-core.md`](instructions/cas-core.md) §4 + [`examples.md`](instructions/examples.md) §3.4 | Pagination helpers, envelope-type parsing |
| `examples/gitlike/` | [`docs/instructions/examples.md`](instructions/examples.md) §2 + [`docs/instructions/cas-core.md`](instructions/cas-core.md) §4.12 | Reference object model: `Blob`/`Tree`/`Commit`/`Tag`, `Repository`, `Resolver`, `WalkGraph` |
| `examples/files/` | [`docs/instructions/examples.md`](instructions/examples.md) §3.1 | Versioned file store example |
| `examples/artifacts/` | [`docs/instructions/examples.md`](instructions/examples.md) §3.2 | Build artifact cache example (custom hash, gzip codec, monitoring) |
| `examples/notes/` | [`docs/instructions/examples.md`](instructions/examples.md) §3.3 | Document graph example (own types, lazy loading, prefetch) |
| `examples/api/` | [`docs/instructions/examples.md`](instructions/examples.md) §3.4 + [`docs/instructions/api-design.md`](instructions/api-design.md) | HTTP-exposure pattern, shared HTTP conventions |
| `cas/errors.go` | [`docs/instructions/library-design.md`](instructions/library-design.md) | Lean-core budget, sentinel errors, API shape, compatibility |
| `cas/*.go` — any exported identifier | [`docs/instructions/library-design.md`](instructions/library-design.md) | Export budget (~40), stable surface, naming, doc comments |
| `cas/*_test.go` (any) | [`docs/instructions/testing-strategy.md`](instructions/testing-strategy.md) | CAS laws, unit/property/fuzz/race/corruption/golden tests, coverage gates |
| `cas/bench_test.go`, `cas/scale_bench_test.go` | [`docs/instructions/performance.md`](instructions/performance.md) + [`docs/benchmarks.md`](benchmarks.md) | Benchmark suite, allocations, streaming hashing, CI gates, scale probes |
| `docs/instructions/` (any `.md` file) | [`docs/instructions/AGENT.md`](instructions/AGENT.md) | Meta-guide: file naming, frontmatter, version bumps, cross-referencing, checklists |
| `.github/workflows/ci.yml` | [`docs/instructions/AGENT.md`](instructions/AGENT.md) §9 + [`docs/instructions/testing-strategy.md`](instructions/testing-strategy.md) §5 | CI gates: gofmt, vet, test, coverage, fuzz, doc integrity, import boundaries |
| Any file not matched above | [`docs/instructions/AGENT.md`](instructions/AGENT.md) + root [`AGENTS.md`](/AGENTS.md) | Default: start with the meta-guide, then narrow by topic |

## Updating this index

- Add a new row when a new rule file is created or an existing one grows a
  new section that other contributors should be able to find by path.
- Remove or re-target a row when a rule file is renamed or its scope
  changes.
- Keep the table sorted by path specificity (most specific first) so
  the first-match rule stays correct.
- Bump the version in the frontmatter on every material change.

## Related

- [`AGENTS.md`](/AGENTS.md) — the repo-root agent aggregator; auto-read by
  AI agents working in this repo.
- [`docs/instructions/AGENT.md`](instructions/AGENT.md) — meta-guide for
  the instruction folder itself.
- [`docs/benchmarks.md`](benchmarks.md) — how to run and read the
  benchmarks.