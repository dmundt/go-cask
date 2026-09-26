---
type: Agent Instructions
title: Agent instructions — `cas`
description: The package-local guide for the core cas subtree — algorithm- and codec-agnostic boundaries, the byte/typed layer split, the canonical memory import aliases, and the repo-wide no-reflection rule with its one recorded exception.
version: v2
---

# Agent instructions — `cas`

Package-local guide applies to core `cas` subtree.

## Purpose

`cas` = generic, hash-agnostic content-addressable store core. Intentionally smaller and more reusable than any one application model, backend, or codec.

Use **content-addressable store** for `cas` package and concrete backends.
Use **content-addressable storage** only for general technique or
architecture pattern.

## Core rules

- Keep `cas` algorithm-agnostic. Never add a default hash algorithm to core package.
- Keep `cas` codec-agnostic. Caller chooses serialization format.
- Keep byte layer and typed layer separate.
- Two packages both declared `package memory`: `cas/backend/mem` (byte-layer backend) and `cas/cache/mem` (cache). Import under canonical aliases `backmem` and `cachemem` — never unaliased, never `mem`, `memory`, `membackend` or `memcache` — so import block and every selector in a file say which half of store they mean. Renaming either package clause = breaking change to frozen surface (cas-core §7.1), waits for major version. `internal/design` enforces aliases: `go test ./internal/design` fails on any other spelling.
- Prefer explicit, typed APIs over reflection or `any` in exported code.
- Go reflection forbidden in `cas` and `cas/*` packages; use explicit typed methods, type switches, codec-specific conversion functions instead. One recorded exception: `isNilValue` in `cas/store.go` — internal nil check every `Put`/`Get` runs, cannot be expressed generically without `reflect.ValueOf` (cas-core §4.7, performance P-04). New reflection use needs same explicit ratification, not quiet addition.
- Keep default policy documents consistent with repo root summary: `SHA-256` = default recommendation, `SHA-512/256` = supported fast alternative, gob = compatibility-only.

## Documentation rules

- Keep README files short, direct, package-focused. Purpose: explain package's role, defaults, policy in few paragraphs, not duplicate full API reference.
- Use one consistent structure across `cas` package docs: title, short summary, included implementations or package overview, policy, typical use, notes.
- Keep package-level language aligned with repo root and `docs/specs` policy: core is generic and agnostic; repo recommends `SHA-256` and JSON, uses `SHA-512/256` as supported fast alternative.
- Link directly to relevant subpackage READMEs when listing implementations. Prefer short labels like `[sha256](./hash/sha256/README.md)` rather than long path names or duplicated prose.
- Link back to parent [`cas`](./README.md) package and repo root [`AGENTS.md`](../AGENTS.md) when a README explains scope or rules.
- Never use README text as a place to restate full API details already covered by Go doc comments and exported interfaces.
- Package compatibility-only or legacy-oriented: say so explicitly, keep wording consistent with rest of repo.

## Default conventions

- Use filesystem backend for durable storage examples.
- Use memory backend for tests and ephemeral examples.
- Prefer JSON for general-purpose portable objects.
- Treat MD5 and SHA-1 as legacy/compatibility choices only.

## Before editing

1. Read repo root [AGENTS.md](../AGENTS.md).
2. Read relevant spec file via [docs/index.md](../docs/index.md) when change touches architecture or API contracts.
3. Keep README wording consistent with package-level policy already described in this subtree.

## Scope

File covers `cas` package and layer READMEs directly under it. Does not define git-like reference model or example programs; those live in their own packages and docs.

## Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
