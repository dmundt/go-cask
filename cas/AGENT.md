---
type: Agent Instructions
title: Agent instructions — `cas`
description: The package-local guide for the core cas subtree — algorithm- and codec-agnostic boundaries, the byte/typed layer split, the canonical memory import aliases, and the repo-wide no-reflection rule with its one recorded exception.
version: v3
---

# Agent instructions — `cas`

Package-local guide for the core `cas` subtree.

## Purpose

`cas` = generic, hash-agnostic content-addressable store core, intentionally smaller and more
reusable than any one application model, backend, or codec.

Use **content-addressable store** for the `cas` package and concrete backends; use
**content-addressable storage** only for the general technique or architecture pattern.

## Core rules

- Keep `cas` algorithm-agnostic: never add a default hash algorithm to the core package.
- Keep `cas` codec-agnostic: the caller chooses the serialization format.
- Keep the byte layer and the typed layer separate.
- Two packages both declare `package memory`: `cas/backend/mem` (byte-layer backend) and
  `cas/cache/mem` (cache). Import under the canonical aliases `backmem` and `cachemem` — never
  unaliased, never `mem`, `memory`, `membackend` or `memcache` — so the import block and every
  selector in a file say which half of the store is meant. Renaming either package clause is a
  breaking change to a frozen surface (cas-core §7.1) and waits for a major version.
  `internal/design` enforces the aliases: `go test ./internal/design` fails on any other
  spelling.
- Prefer explicit, typed APIs over reflection or `any` in exported code.
- Go reflection is forbidden in `cas` and `cas/*`; use explicit typed methods, type switches, or
  codec-specific conversion functions instead. One recorded exception: `isNilValue` in
  `cas/store.go` — the internal nil check every `Put`/`Get` runs, which cannot be expressed
  generically without `reflect.ValueOf` (cas-core §4.7, performance P-04). New reflection use
  needs the same explicit ratification, not a quiet addition.

## Documentation rules

- Keep README files short, direct and package-focused — role, defaults, policy — never a
  duplicated API reference: full API details belong in Go doc comments and exported interfaces.
- Use one consistent structure across `cas` package docs: title, short summary, included
  implementations or package overview, policy, typical use, notes.
- Keep package-level language aligned with the repo root and `docs/specs` policy: the core is
  generic and agnostic; the repo recommends `SHA-256` and JSON, uses `SHA-512/256` as the
  supported fast alternative, treats gob as compatibility-only, and ships neither MD5 nor SHA-1.
- Link directly to the relevant subpackage READMEs when listing implementations, with short
  labels like `[sha256](./hash/sha256/README.md)` rather than long path names or duplicated
  prose.
- Link back to the parent [`cas`](./README.md) package and the repo root
  [`AGENTS.md`](../AGENTS.md) when a README explains scope or rules.
- A compatibility-only or legacy-oriented package says so explicitly, in wording consistent with
  the rest of the repo.

## Default conventions

- Filesystem backend for durable storage examples.
- Memory backend for tests and ephemeral examples.
- JSON for general-purpose portable objects.
- MD5 and SHA-1 are legacy/compatibility choices only.

## Before editing

1. Read the repo root [AGENTS.md](../AGENTS.md).
2. Read the relevant spec file via [docs/index.md](../docs/index.md) when the change touches
   architecture or API contracts.
3. Keep README wording consistent with the package-level policy already described in this
   subtree.

## Scope

Covers the `cas` package and the layer READMEs directly under it. Does not define the git-like
reference model or example programs; those live in their own packages and docs.

## Signed pull-request workflow

Owned by [docs/specs/landing.md](../docs/specs/landing.md): branch rebuilt locally from `main`,
signed commits (`git cherry-pick -S`, `git verify-commit`), `git push --force-with-lease`, never
GitHub's server-side rebase or update-branch, auto-merge only after verification and checks.
