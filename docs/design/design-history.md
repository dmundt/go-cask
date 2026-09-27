---
type: Design Document
title: Design History — go-cask
description: Non-normative record of how the cas design converged — the origin conversation, its ten stages, and the state the reference implementation was consolidated from; the normative result lives in cas-core.md.
version: v1
---

# Design History — go-cask

**Non-normative.** How the `cas` design got here: the origin conversation, the
stages it moved through, and the final state. The normative result is
[`../specs/cas-core.md`](../specs/cas-core.md); this file explains, and a
conflict is decided by that spec.

## 1. Origin

Generated from the DeepSeek design conversation at
<https://chat.deepseek.com/share/p7jkdjl1gbyhjipf6r>. It captures the **final
implementation** the conversation converged on: a generic, Git-like,
content-addressable object store component written in Go, fully type-safe via
generics (no `any` in an exported value position), a hash-agnostic core whose
clients own the algorithm (`sha256` ships as the default), a filesystem
backend, typed object layers, lazy loading and caching.

The final user turn ("I want a repo from your code with all the latest changes
and features") is not answered inside the share, so the reference
implementation is consolidated from the last converged state of the
conversation — `cas-core.md` §4 (component specifications with code) and §5
(data flows).

The core contract is authoritative for all agent-assisted changes to
`package cas`, for any tool that honors `AGENTS.md` (GitHub Copilot, OpenAI
Codex, Claude Code, …). Code produced for this repo MUST follow it unless the
user explicitly overrides it.

## 2. Stages

Chat history moved through these stages; **final state** = fully type-safe,
registry-free design:

1. Generic store + `Codec[T]` wrapper over a byte `Backend`.
2. Git-like object store: objects are `[]byte` addressed by `Hash`, with typed
   objects (`blob`, `tree`, `commit`) on top.
3. Objects can reference each other by hash with types **not known in advance**;
   a runtime registry handles unknown types.
4. Go generics (1.18+/latest): `Store[T]`, `Object[T]`, `Codec[T]`.
5. **Pluggable hash functions; the hash type carries the algorithm**:
   `Hash` is `algo:digest` (e.g. `sha256:a1b2...`), so references are
   self-describing and algorithms can be mixed/migrated.
6. **`Backend` for the filesystem** (`FSBackend`): Git-like fan-out
   directories (configurable n-way/n-level), atomic temp-file writes,
   lock-free reads, stats, verify, GC.
7. **Remove `any`**: fully generic `Object[T]`, `Store[T]`, `JSONCodec[T]`,
   separate per-type stores, no runtime type assertions in the public API.
8. **Cross-type references without `any`**: references are plain `Hash` values;
   resolution is type-safe via per-type stores, a `Repository`, and a
   `Resolver` (with a typed `ResolvedObject` union for "resolve anything").
9. **Lazy loading + caching**: `CachedObject[T]` (lazy proxy), `CachedStore[T]`,
   LRU eviction, `CachedRepository`, background `Preloader` (prefetch-on-
   access and cache-monitor metrics later became example recipes in
   `examples/notes` and `examples/artifacts`).
10. **Generic core vs. example layer**: the git-like model (`Blob`/`Tree`/
    `Commit`/`Tag`, `Repository`, `Resolver`, `ResolvedObject`, `WalkGraph`,
    `CachedRepository`, `Preloader`) is a shared reference object model in a separate
    `gitlike/` package — the `cas` core stays app-agnostic and generic only.

## 3. Where the stages landed

| Stage | Normative owner today |
| --- | --- |
| 1-4 | cas-core §4.6–4.8 (`Codec[T]`, `Object[T]`, `Store[T]`) |
| 5 | cas-core §2.4, §4.1–4.2, §8 decision 2 — the algorithm left the address: `Digest` is raw bytes and the client injects a `Hasher` |
| 6 | cas-core §4.4 (`fs.Backend`) |
| 7-8 | cas-core §2.6, §4.12 and `library-design.md` §1 (`gitlike`, `cas/repo`) |
| 9 | cas-core §4.10 (`CachedObject`, `CachedStore`, `lru`) |
| 10 | cas-core §3.1, §4.12 and `library-design.md` §1.1 (the classes and the dependency matrix) |
