---
type: Agent Instructions
title: AGENT — go-cask Examples
description: Rules for runnable example programs under examples/, including folder conventions, README requirements, test expectations, and the allowed scope of teaching code.
version: v5
---

# AGENT — go-cask Examples

Folder contains runnable teaching programs for go-cask. Every example MUST stay focused on one real pattern, keep library core untouched, document pattern clearly enough that an app author can copy it.

Related: [AGENTS.md](../AGENTS.md), [docs/specs/examples.md](../docs/specs/examples.md), [docs/index.md](../docs/index.md).

## 1. Purpose and scope

- `examples/<name>/` for runnable programs (`package main`) and their tests/docs.
- Examples = teaching code, not library code. Must demonstrate real app wiring using public API only.
- `gitlike/` = reference support library at module root, not example: 2nd-class library at application layer that apps and examples import (AGENTS.md, "Layers and citizen classes").
- Never change core `cas` package to accommodate an example's convenience or missing feature. Feature missing: fix library or spec instead of hacking around it in example.

## 2. Mandatory structure

Each example directory MUST include:

- `main.go` or equivalent runnable entrypoint
- `README.md` with the required teaching sections
- `main_test.go` or package-local tests where behavior is assertable

## 3. Required README content

Each example README MUST include:

- short title and purpose
- `What it demonstrates` describing primary aspect and acceptance criteria
- list of exact `cas`/`gitlike` APIs used
- what the example extends or wires together
- code walkthrough pointing to key files and their roles
- a Mermaid diagram
- how to run it with exact commands and expected output shape

Keep README short and direct. Should teach pattern and scope, not duplicate full API reference.

## 4. Example rules

- One focus per example: one real workflow, not a kitchen sink.
- Use stdlib only. No external dependencies in example code.
- Never import another example package, `internal/**` or `cmd/**`; module's libraries (`cas/**`, `gitlike`) are ordinary imports. Rule 11 of `docs/specs/examples.md` §2 states it, AGENTS.md carries layer matrix.
- Keep examples self-contained. Prefer teaching code inline rather than introducing a new helper package unless a second consumer justifies it.
- Use secure defaults: `SHA-256` for identity, JSON for readable formats, filesystem storage for durable examples.
- Never add `any` to example APIs. Prefer explicit typed models.
- Use command-line or simple app wiring patterns easy to follow.
- Keep output meaningful: hashes, stats, graph traversal, dedup results, loaded objects, or error output that proves the pattern.

## 5. Testing and validation

- Examples MUST compile with repo's Go toolchain.
- Example tests should verify behavior the example teaches.
- Use `go test ./examples/...` as standard local validation command for this folder.

## 6. Scope boundaries

- Examples are not core library. Must not be used to bend library into app-specific APIs.
- Examples may define small local types and helper structs when needed for teaching, but must not become a second shared library.
- Keep design consistent with `docs/specs/examples.md` and repo root `AGENTS.md`.

## 7. Checklist

- [ ] Example stays focused on one real pattern
- [ ] README includes purpose, APIs used, walkthrough, Mermaid diagram, and run commands
- [ ] Code uses public APIs only and does not modify `cas`/`gitlike` for convenience
- [ ] Tests compile and verify the demonstrated behavior
- [ ] Example remains consistent with repo-level guidance and the examples spec

## 8. Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
