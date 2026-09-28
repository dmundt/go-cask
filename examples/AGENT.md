---
type: Agent Instructions
title: AGENT — go-cask Examples
description: Rules for runnable example programs under examples/, including folder conventions, README requirements, test expectations, and the allowed scope of teaching code.
version: v7
---

# AGENT — go-cask Examples

Runnable teaching programs. Each example MUST stay focused on one real pattern, keep library
core untouched, and document the pattern clearly enough to copy.

Related: [AGENTS.md](../AGENTS.md), [docs/specs/examples.md](../docs/specs/examples.md), [docs/index.md](../docs/index.md).

## 1. Purpose and scope

- `examples/<name>/` — runnable programs (`package main`) plus their tests/docs.
- Examples = teaching code, not library code: real app wiring, public API only.
- `gitlike/` = reference support library at module root, not an example: 2nd-class library at
  the application layer that apps and examples import (library-design.md §1.1).
- Never change core `cas` for an example's convenience or missing feature: fix the library or
  the spec.

## 2. Mandatory structure

Each example directory MUST include:

- `main.go` or equivalent runnable entrypoint
- `README.md` with the required teaching sections
- `main_test.go` or package-local tests where behavior is assertable

## 3. Required README content

Each example README MUST include:

- short title and purpose
- `What it demonstrates`: primary aspect and acceptance criteria
- exact `cas`/`gitlike` APIs used
- what the example extends or wires together
- code walkthrough naming key files and roles
- a Mermaid diagram
- exact run commands and expected output shape

Short and direct: teach pattern and scope, never duplicate the full API reference.

## 4. Example rules

- One focus per example: one real workflow, not a kitchen sink.
- stdlib only; no external dependencies in example code.
- Never import another example package, `internal/**` or `cmd/**`; `cas/**` and `gitlike` are
  ordinary imports (examples.md §2 rule 11; layer matrix in library-design.md §1.1).
- Self-contained: teach inline rather than adding a helper package unless a second consumer
  justifies it.
- Secure defaults: `SHA-256` identity, JSON readable formats, filesystem storage for durable
  examples.
- No `any` in example APIs; explicit typed models.
- Command-line or simple app wiring, easy to follow.
- Meaningful output: hashes, stats, graph traversal, dedup results, loaded objects, or error
  output that proves the pattern.

## 5. Testing and validation

- Examples MUST compile with the repo's Go toolchain.
- Example tests verify the behavior the example teaches.
- Standard local validation: `go test ./examples/...`.

## 6. Scope boundaries

- Examples are not core library and MUST NOT bend it into app-specific APIs.
- Small local types and helper structs are allowed for teaching; a second shared library is not.
- Keep design consistent with `docs/specs/examples.md` and root `AGENTS.md`.

## 7. Checklist

- [ ] Example stays focused on one real pattern
- [ ] README includes purpose, APIs used, walkthrough, Mermaid diagram, and run commands
- [ ] Code uses public APIs only and does not modify `cas`/`gitlike` for convenience
- [ ] Tests compile and verify the demonstrated behavior
- [ ] Example remains consistent with repo-level guidance and the examples spec

## 8. Signed pull-request workflow

Signed commits: rebuild PR branches locally from current `main`, never GitHub's server-side
rebase or update-branch; `git cherry-pick -S`, `git verify-commit` every head commit,
`git push --force-with-lease`, auto-merge only after signature verification and the required
checks pass (`docs/specs/landing.md`).
