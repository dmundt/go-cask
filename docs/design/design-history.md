---
type: Design Document
title: Design History — go-cask
description: Non-normative record of how the cas design converged — the origin conversation, its ten stages, where each landed; the normative result is cas-core.md.
version: v2
---

# Design History — go-cask

**Non-normative.** Sequence of design decisions and their outcomes. Normative result:
[`../specs/cas-core.md`](../specs/cas-core.md); a conflict is decided there.

## 1. Origin

- Source: DeepSeek design conversation, <https://chat.deepseek.com/share/p7jkdjl1gbyhjipf6r>.
- Captures the **final implementation** the conversation converged on: generic, Git-like,
  content-addressable object store in Go.
- Fully type-safe via generics — no `any` in an exported value position.
- Hash-agnostic core; clients own the algorithm, `sha256` ships as the default.
- Filesystem backend, typed object layers, lazy loading, caching.
- Final user turn ("I want a repo from your code with all the latest changes and features")
  is unanswered inside the share, so the reference implementation is consolidated from the
  last converged state: `cas-core.md` §4 (component specifications with code), §5 (data flows).
- Code produced for this repo MUST follow that contract unless the user explicitly overrides
  it; it is authoritative for agent-assisted changes to `package cas` and for any tool
  honoring `AGENTS.md` (GitHub Copilot, OpenAI Codex, Claude Code, …).

## 2. Stages

**Final state** = fully type-safe, registry-free design.

1. Generic store + `Codec[T]` wrapper over a byte `Backend`.
2. Git-like object store: objects are `[]byte` addressed by `Hash`, typed objects
   (`blob`, `tree`, `commit`) on top.
3. Objects reference each other by hash with types **not known in advance**; a runtime
   registry handles unknown types.
4. Go generics (1.18+/latest): `Store[T]`, `Object[T]`, `Codec[T]`.
5. **Pluggable hash functions; the hash type carries the algorithm**: `Hash` is
   `algo:digest` (e.g. `sha256:a1b2...`) — references self-describing, algorithms
   mixable/migratable.
6. **`Backend` for the filesystem** (`FSBackend`): Git-like fan-out directories
   (configurable n-way/n-level), atomic temp-file writes, lock-free reads, stats,
   verify, GC.
7. **Remove `any`**: fully generic `Object[T]`, `Store[T]`, `JSONCodec[T]`, separate
   per-type stores, no runtime type assertions in the public API.
8. **Cross-type references without `any`**: references are plain `Hash` values;
   resolution type-safe via per-type stores, a `Repository`, a `Resolver` (with a typed
   `ResolvedObject` union for "resolve anything").
9. **Lazy loading + caching**: `CachedObject[T]` (lazy proxy), `CachedStore[T]`, LRU
   eviction, `CachedRepository`, background `Preloader`; prefetch-on-access and
   cache-monitor metrics later became recipes in `examples/notes` and `examples/artifacts`.
10. **Generic core vs. example layer**: the git-like model (`Blob`/`Tree`/`Commit`/`Tag`,
    `Repository`, `Resolver`, `ResolvedObject`, `WalkGraph`, `CachedRepository`,
    `Preloader`) is a shared reference object model in a separate `gitlike/` package — the
    `cas` core stays app-agnostic and generic only.

## 3. Where the stages landed

| Stage | Normative owner today | Outcome |
| --- | --- | --- |
| 1-4 | cas-core §4.6–4.8 | `Codec[T]`, `Object[T]`, `Store[T]` |
| 5 | cas-core §2.4, §4.1–4.2, §8 decision 2 | Algorithm left the address: `Digest` raw bytes, client injects `Hasher` |
| 6 | cas-core §4.4 | `fs.Backend` |
| 7-8 | cas-core §2.6, §4.12; `library-design.md` §1 | `gitlike`, `cas/repo` |
| 9 | cas-core §4.10 | `CachedObject`, `CachedStore`, `lru` |
| 10 | cas-core §3.1, §4.12; `library-design.md` §1.1 | The classes and the dependency matrix |
