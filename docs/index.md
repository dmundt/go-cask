---
okf_version: "0.2"
title: go-cask Rules Index
description: Path-first rule lookup. Match file path -> spec file -> detailed rules. Short enough for starting instructions.
version: v59
---

# go-cask Rules Index

**Lookup:** for the file being edited, match its path against the first column (longest match wins), read the linked rule file, and apply it while planning and editing. A row carries no rule: the rule file owns it, and the path column names the files it governs.

| Path | Rule file |
|---|---|
| `cas/digest.go`, `cas/hasher.go`; `TestDigest*` / `FuzzParseDigest` | [`cas-core.md`](specs/cas-core.md) §4.1–4.3 |
| `cas/backend/*`, `cas/backend/fs`, `cas/backend/mem`, `cas/backend/packfs`, `cas/backend/snapshot`, `cas/backend/stream.go`, `cas/backend/context.go`, `cas/backend/options.go` | [`cas-core.md`](specs/cas-core.md) §4.3–4.5, §4.14 + [`library-design.md`](specs/library-design.md) §4.2 |
| `cas/internal/atomicfile/` | [`cas-core.md`](specs/cas-core.md) §4.4 (the atomic write path) |
| `cas/backend.go` | [`cas-core.md`](specs/cas-core.md) §4.3–4.5 |
| `cas/hash/`, `cas/hash/sha256/`, `cas/hash/sha512/`, `cas/hash/sha512_256/` | [`cas-core.md`](specs/cas-core.md) §4.2 + [`defaults.md`](specs/defaults.md) §2 |
| `cas/verifier.go`, `cas/verifier_test.go`, `cas/sweep.go`, `cas/sweep_test.go`, `cas/reachability.go`, `cas/capabilities.go`, `cas/capabilities_test.go` | [`cas-core.md`](specs/cas-core.md) §4.3, §4.9, §4.11, §4.13 |
| `cas/verify/sidecar/*` | [`operations.md`](specs/operations.md) §6 + [`cas-core.md`](specs/cas-core.md) §4.11 + [`library-design.md`](specs/library-design.md) §1 |
| `cas/verify/*`, `cas/verify/crc32/*` | [`cas-core.md`](specs/cas-core.md) §4.3, §4.11 + [`cas/verify/AGENT.md`](../cas/verify/AGENT.md) |
| `cas/store.go`, `cas/codec.go`, `cas/object.go`, `cas/walker.go`, `cas/batch.go`, `cas/readclose.go`, `cas/envelope.go`, `cas/stream.go` | [`cas-core.md`](specs/cas-core.md) §4.6–4.13 + §8 d1 |
| `cas/codec/json/`, `cas/codec/gob/`, `cas/codec/cbor/`, `cas/codec/binary/`, `cas/codec/gzip/`, `cas/codec/zlib/`, `cas/codec/flate/` | [`cas-core.md`](specs/cas-core.md) §4.2, §4.6, §7.1 + [`defaults.md`](specs/defaults.md) §2 |
| `cas/cache/validate.go`, `cas/cache/mem/cached.go`, `cas/cache/lru/lru.go`, `cas/cache/prefetch/` | [`cas-core.md`](specs/cas-core.md) §4.10 |
| `cas/pack/` | [`cas-core.md`](specs/cas-core.md) §7 + [`cas/pack/README.md`](../cas/pack/README.md) |
| `cas/backend/fs/fs.go`, `cas/stats.go` | [`consistency.md`](specs/consistency.md) |
| `cas/refs/` | [`consistency.md`](specs/consistency.md) §4 + [`library-design.md`](specs/library-design.md) §1 |
| `cas/repo/` | [`consistency.md`](specs/consistency.md) §4 + [`cas-core.md`](specs/cas-core.md) §4.12 + [`library-design.md`](specs/library-design.md) §1 |
| `cas/errors.go`; any exported `cas.*` | [`library-design.md`](specs/library-design.md) |
| `cas/*_test.go` | [`testing-strategy.md`](specs/testing-strategy.md) |
| any package under `cas/**` (coverage tier) | [`testing-strategy.md`](specs/testing-strategy.md) §5 |
| `cas/bloom/`, `cas/bloom/counting/`, `cas/bloom/standard/`, `cas/bloom/persistent/` | [`performance.md`](specs/performance.md) + [`consistency.md`](specs/consistency.md) + [`cas/bloom/README.md`](../cas/bloom/README.md) + [`coding-guidelines.md`](specs/coding-guidelines.md) §3 |
| `cmd/cask/` | [`cli.md`](specs/cli.md) + [`cmd/cask/README.md`](../cmd/cask/README.md) |
| `cmd/buildtool/` | [`cmd/buildtool/README.md`](../cmd/buildtool/README.md) + [`internal/build/README.md`](../internal/build/README.md) + [`scripts/AGENT.md`](../scripts/AGENT.md) |
| `internal/web/` (wiring, middleware, config) | [`backend-architecture.md`](specs/backend-architecture.md) + [`internal/web/README.md`](../internal/web/README.md) |
| `internal/web/` (templates, htmx) | [`frontend-architecture.md`](specs/frontend-architecture.md) + [`internal/web/README.md`](../internal/web/README.md) |
| `internal/web/` (sessions, CSRF, roles, audit) | [`viewer-security.md`](specs/viewer-security.md) + [`internal/web/README.md`](../internal/web/README.md) |
| `internal/web/` (objects, hexdump) | [`viewer-design.md`](specs/viewer-design.md) + [`internal/web/README.md`](../internal/web/README.md) + [`cmd/cask/README.md`](../cmd/cask/README.md) |
| `internal/index/` | [`cas-core.md`](specs/cas-core.md) §4 + [`examples.md`](specs/examples.md) §3.4 |
| `internal/store/` (backend-selection seam: `Kind`/`ParseKind`/`Open`/`OpenViewer`) | [`backend-architecture.md`](specs/backend-architecture.md) + [`cli.md`](specs/cli.md) |
| `internal/design/` | [`library-design.md`](specs/library-design.md) §1, §4.5 + [`cas/AGENT.md`](../cas/AGENT.md) + [`cli.md`](specs/cli.md) §3 |
| `internal/build/` | [`internal/build/README.md`](../internal/build/README.md) + [`internal/build/AGENT.md`](../internal/build/AGENT.md) + [`library-design.md`](specs/library-design.md) §1.1 + [`testing-strategy.md`](specs/testing-strategy.md) §5 + [`docs/specs/AGENT.md`](specs/AGENT.md) §9 + [`website/AGENT.md`](../website/AGENT.md) + [`cas-core.md`](specs/cas-core.md) §4.12, §7 |
| `internal/build/board/` | [`coordination.md`](specs/coordination.md) §2, §4, §6 + [`internal/build/board/README.md`](../internal/build/board/README.md) |
| `.golangci.yml` | [`coding-guidelines.md`](specs/coding-guidelines.md) §3 (the dependency policy) + [`library-design.md`](specs/library-design.md) §1.1 (the layer matrix its `depguard` block mirrors) + [`internal/build/README.md`](../internal/build/README.md) |
| `gitlike/` | [`cas-core.md`](specs/cas-core.md) §4.12 + [`library-design.md`](specs/library-design.md) §1, §1.1 |
| `examples/*` | [`examples.md`](specs/examples.md) §2 + [`library-design.md`](specs/library-design.md) §1.1 |
| `examples/files/`, `examples/artifacts/`, `examples/notes/`, `examples/api/`, `examples/bloom/`, `examples/pack/` | [`examples.md`](specs/examples.md) §3.1–3.4 + [`api-design.md`](specs/api-design.md) (`api`) + [`performance.md`](specs/performance.md) §5.1 (`bloom`) |
| `scripts/` | [`scripts/README.md`](../scripts/README.md) + [`scripts/AGENT.md`](../scripts/AGENT.md) |
| `benchmarks/` | [`benchmarks/AGENT.md`](../benchmarks/AGENT.md) + [`performance.md`](specs/performance.md) + [`benchmarks/README.md`](../benchmarks/README.md) |
| `docs/specs/*.md` | [`docs/specs/AGENT.md`](specs/AGENT.md) |
| `.github/workflows/ci.yml` | [`docs/specs/AGENT.md`](specs/AGENT.md) §9 + [`testing-strategy.md`](specs/testing-strategy.md) §5 |
| `.github/copilot-instructions.md` | root [`AGENTS.md`](../AGENTS.md) |
| `.agents/`, `.agents/skills/` | [`../.agents/AGENT.md`](../.agents/AGENT.md); a skill is its own `SKILL.md` ([`cask-change`](../.agents/skills/cask-change/SKILL.md), [`coordinate`](../.agents/skills/coordinate/SKILL.md)) |
| `AGENTS.md` | [`docs/AGENT.md`](../docs/AGENT.md) §2.1 |
| Everything else | [`docs/specs/AGENT.md`](specs/AGENT.md) + root [`AGENTS.md`](../AGENTS.md) + [`landing.md`](specs/landing.md) (worktree, lane, gate, landing) + [`coordination.md`](specs/coordination.md) (many landings: board, waves, gate serialization) |

**Updating:** add/remove/re-target rows when rule files change; bump version on material change. A row carries paths and links only — an explanation belongs in the rule file it points at. Sibling indexes: [`docs/design/index.md`](design/index.md), [`docs/specs/index.md`](specs/index.md).
