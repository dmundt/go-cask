---
okf_version: "0.2"
title: go-cask Rules Index
description: Path-first rule lookup. Match file path -> spec file -> detailed rules. Short enough for starting instructions.
version: v24
---

# go-cask Rules Index

**Lookup:** for the file being edited, match its path against the first column (longest match wins), read the linked rule file, and apply it while planning and editing.

| Path | Rule file |
|---|---|
| `cas/digest.go`, `cas/hasher.go`; `TestDigest*` / `FuzzParseDigest` | [`cas-core.md`](specs/cas-core.md) §4.1–4.3 |
| `cas/backend/*` / `cas/backend/fs` / `cas/backend/mem` / `cas/backend/packfs` / `cas/backend/snapshot` / `cas/backend/stream.go` / `cas/backend/context.go` / `cas/backend/options.go` | [`cas-core.md`](specs/cas-core.md) §4.3–4.5, §4.14 (`packfs` is a shipped backend, not a deferred extension) + architecture boundary rule: backends stay storage-only; the shared `cas/backend` contract is the streaming/context plumbing only (`ContextReader`, `WriteAll`, `ReadAll`, `ReadPayload`) — **each backend declares its own `Option` and there is no shared one**, so a cross-backend option is a compile error rather than a silent no-op ([`library-design.md`](specs/library-design.md) §4.2); helper packages must not become implicit backends |
| `cas/hash/` (the hasher helpers), `cas/hash/sha256/`, `cas/hash/sha512/`, `cas/hash/sha512_256/` (the shipped client hashers) | [`cas-core.md`](specs/cas-core.md) §4.2 + [`defaults.md`](specs/defaults.md) (the shipped default is `sha256`; the core names no algorithm) |
| `cas/backend.go` | [`cas-core.md`](specs/cas-core.md) §4.3–4.5 |
| `cas/verifier.go`, `cas/verifier_test.go`, `cas/sweep.go`, `cas/sweep_test.go`, `cas/reachability.go`, `cas/capabilities.go`, `cas/capabilities_test.go` | [`cas-core.md`](specs/cas-core.md) §4.3, §4.11, §4.13 + architecture boundary rule: verification/sweep/reachability are a separate, backend-agnostic maintenance layer above the storage contract (go-cask#137) |
| `cas/verify/*` / `cas/verify/crc32/*` | [`cas-core.md`](specs/cas-core.md) §4.3, §4.11 + architecture boundary rule: verification helpers are maintenance-only and must not redefine the storage model; they are `cas.Hasher` implementations for a store deliberately addressed by that checksum, so they verify only objects addressed with it |
| `cas/store.go`, `cas/codec.go`, `cas/object.go`, `cas/walker.go`, `cas/batch.go` (`GetMany`/`BatchGetter`), `cas/envelope.go` (TLV readers incl. `PeekVersion`) | [`cas-core.md`](specs/cas-core.md) §4.6–4.13 + §8 d1 |
| `cas/codec/json/`, `cas/codec/gob/`, `cas/codec/cbor/`, `cas/codec/binary/`, `cas/codec/gzip/`, `cas/codec/zlib/`, `cas/codec/flate/` | [`cas-core.md`](specs/cas-core.md) §4.2, §4.6, §7.1 (stable surface) + [`defaults.md`](specs/defaults.md) (`flate` is the default compression wrapper; `MaxDecodedBytes` binds the decompressing wrappers) |
| `cas/cache/validate.go` (the shared cache validation layer), `cas/cache/mem/cached.go`, `cas/cache/lru/lru.go`, `cas/cache/prefetch/` | [`cas-core.md`](specs/cas-core.md) §4.10 |
| `cas/pack/` (app-facing manifest/helper layer) | [`cas-core.md`](specs/cas-core.md) §7 + architecture boundary rule: helper/manifest logic stays out of the core and is not a backend |
| `cas/backend/fs/fs.go` (`Stats`/`Verify`/`GC`/`Prune`/`Clean`/`Size`), `cas/stats.go` | [`consistency.md`](specs/consistency.md) |
| `cas/refs/` (named mutable pointers, reflog) | [`consistency.md`](specs/consistency.md) §4 (root set for GC) + [`library-design.md`](specs/library-design.md) |
| `cas/repo/` (typed cross-type registry, Walk, Reachable) | [`consistency.md`](specs/consistency.md) §4 (root set for GC) + [`cas-core.md`](specs/cas-core.md) §4.12 + [`library-design.md`](specs/library-design.md) |
| `cmd/cask/` | [`cli.md`](specs/cli.md) + the [viewer page](../website/viewer.md) for the user-facing explanation of `cask web` |
| `internal/web/` (wiring, middleware, config) | [`backend-architecture.md`](specs/backend-architecture.md) + [`internal/web/README.md`](../internal/web/README.md) |
| `internal/web/` (templates, htmx) | [`frontend-architecture.md`](specs/frontend-architecture.md) + [`internal/web/README.md`](../internal/web/README.md) |
| `internal/web/` (sessions, CSRF, roles, audit) | [`viewer-security.md`](specs/viewer-security.md) + [`internal/web/README.md`](../internal/web/README.md) |
| `internal/web/` (objects, hexdump) | [`viewer-design.md`](specs/viewer-design.md) + [`internal/web/README.md`](../internal/web/README.md) + the [viewer page](../website/viewer.md) for the user-facing explanation of the reference states |
| `internal/index/` | [`cas-core.md`](specs/cas-core.md) §4 + [`examples.md`](specs/examples.md) §3.4 |
| `internal/store/` (backend-selection seam: `Kind`/`ParseKind`/`Open`/`OpenViewer`) | [`backend-architecture.md`](specs/backend-architecture.md) + [`cli.md`](specs/cli.md) |
| `internal/design/` (design-rule checks the gate runs, starting with the no-`any` rule) | [`library-design.md`](specs/library-design.md) §5 |
| `gitlike/` | [`examples.md`](specs/examples.md) §2 + [`cas-core.md`](specs/cas-core.md) §4.12 |
| `examples/files/` | [`examples.md`](specs/examples.md) §3.1 |
| `examples/artifacts/` | [`examples.md`](specs/examples.md) §3.2 |
| `examples/notes/` | [`examples.md`](specs/examples.md) §3.3 |
| `examples/api/` | [`examples.md`](specs/examples.md) §3.4 + [`api-design.md`](specs/api-design.md) |
| `examples/bloom/`, `examples/pack/` | [`examples.md`](specs/examples.md) §3 + [`performance.md`](specs/performance.md) §5.1 (bloom) |
| `cas/errors.go`; any exported `cas.*` | [`library-design.md`](specs/library-design.md) |
| `cas/*_test.go` | [`testing-strategy.md`](specs/testing-strategy.md) |
| any package under `cas/**` (coverage tier, `scripts/verify.sh` `coverage_targets` / `coverage_exempt`) | [`testing-strategy.md`](specs/testing-strategy.md) §5 |
| `cas/bloom/` and its subpackages `cas/bloom/counting/`, `cas/bloom/standard/`, `cas/bloom/persistent/` | [`performance.md`](specs/performance.md) + [`consistency.md`](specs/consistency.md) + [`cas/bloom/README.md`](/cas/bloom/README.md) + [`coding-guidelines.md`](specs/coding-guidelines.md) §3 (`persistent` is the approved `golang.org/x/sys` mmap exception) |
| `scripts/` | [`scripts/AGENT.md`](../scripts/AGENT.md) + [`scripts/README.md`](../scripts/README.md) |
| `benchmarks/` | [`benchmarks/AGENT.md`](/benchmarks/AGENT.md) + [`performance.md`](specs/performance.md) + [`benchmarks/README.md`](/benchmarks/README.md) |
| `docs/specs/*.md` | [`docs/specs/AGENT.md`](specs/AGENT.md) |
| `.github/workflows/ci.yml` | [`AGENT.md`](specs/AGENT.md) §9 + [`testing-strategy.md`](specs/testing-strategy.md) §5 |
| `.agents/` (agent tooling: skills and their guide) | [`../.agents/AGENT.md`](../.agents/AGENT.md) governs it; a skill is also its own `SKILL.md` (see the row below) |
| `.agents/skills/` (agent skills discovered at the project root) | the skill's own `SKILL.md` (directory bundle `<name>/SKILL.md`): [`../.agents/skills/cask-change/SKILL.md`](../.agents/skills/cask-change/SKILL.md) points at the rule files for a change |
| Everything else | [`docs/specs/AGENT.md`](specs/AGENT.md) + root [`AGENTS.md`](/AGENTS.md) |

**Updating:** add/remove/re-target rows when rule files change; bump version on material change. Sibling indexes: [`docs/design/index.md`](design/index.md), [`docs/specs/index.md`](specs/index.md).
