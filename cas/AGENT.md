# Agent instructions — `cas`

This package-local guide applies to the core `cas` subtree.

## Purpose

`cas` is the generic, hash-agnostic content-addressable store core. It is intentionally smaller and more reusable than any one application model, backend, or codec.

## Core rules

- Keep `cas` algorithm-agnostic. Do not add a default hash algorithm to the core package.
- Keep `cas` codec-agnostic. The caller chooses serialization format.
- Keep the byte layer and typed layer separate.
- Prefer explicit, typed APIs over reflection or `any` in exported code.
- Keep default policy documents consistent with the repo root summary: `SHA-256` is the default recommendation, `SHA-512/256` is the supported fast alternative, and gob is compatibility-only.

## Documentation rules

- Keep README files short, direct, and package-focused. The purpose is to explain the package's role, defaults, and policy in a few paragraphs, not to duplicate the full API reference.
- Use one consistent structure across `cas` package docs: title, short summary, included implementations or package overview, policy, typical use, and notes.
- Keep package-level language aligned with the repo root and the `docs/specs` policy: the core is generic and agnostic; the repo recommends `SHA-256` + JSON and uses `SHA-512/256` as a supported fast alternative.
- Link directly to relevant subpackage READMEs when listing implementations. Prefer short labels like `[sha256](./sha256/README.md)` rather than long path names or duplicated prose.
- Link back to the parent [`cas`](./README.md) package and to the repo root [`AGENTS.md`](../AGENTS.md) when a README explains scope or rules.
- Do not use README text as a place to restate full API details already covered by Go doc comments and exported interfaces.
- When a package is compatibility-only or legacy-oriented, say so explicitly and keep the wording consistent with the rest of the repo.

## Default conventions

- Use the filesystem backend for durable storage examples.
- Use the memory backend for tests and ephemeral examples.
- Prefer JSON for general-purpose portable objects.
- Treat MD5 and SHA-1 as legacy/compatibility choices only.

## Before editing

1. Read the repo root [AGENTS.md](../AGENTS.md).
2. Read the relevant spec file via [docs/index.md](../docs/index.md) when the change touches architecture or API contracts.
3. Keep README wording consistent with the package-level policy already described in this subtree.

## Scope

This file covers the `cas` package and the layer READMEs directly under it. It does not define the git-like reference model or the example programs; those live in their own packages and docs.
