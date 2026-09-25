---
type: Agent Instructions
title: AGENT — go-cask Examples
description: Rules for runnable example programs under examples/, including folder conventions, README requirements, test expectations, and the allowed scope of teaching code.
version: v3
---

# AGENT — go-cask Examples

This folder contains the runnable teaching programs for go-cask. Every example MUST stay focused on one real pattern, keep the library core untouched, and document the pattern clearly enough that an app author can copy it.

Related: [AGENTS.md](../AGENTS.md), [docs/specs/examples.md](../docs/specs/examples.md), [docs/index.md](../docs/index.md).

## 1. Purpose and scope

- `examples/<name>/` is for runnable programs (`package main`) and their tests/docs.
- Examples are teaching code, not library code. They must demonstrate real app wiring using the public API only.
- `gitlike/` stays the shared reference library; it is not a runnable example.
- Do not change the core `cas` package to accommodate an example's convenience or missing feature. If a feature is missing, fix the library or spec instead of hacking around it in the example.

## 2. Mandatory structure

Each example directory MUST include:

- `main.go` or equivalent runnable entrypoint
- `README.md` with the required teaching sections
- `main_test.go` or package-local tests where behavior is assertable

## 3. Required README content

Each example README MUST include:

- a short title and purpose
- `What it demonstrates` describing the primary aspect and acceptance criteria
- a list of the exact `cas`/`gitlike` APIs used
- what the example extends or wires together
- a code walkthrough pointing to the key files and their roles
- a Mermaid diagram
- how to run it with exact commands and expected output shape

Keep the README short and direct. It should teach pattern and scope, not duplicate the full API reference.

## 4. Example rules

- One focus per example: one real workflow, not a kitchen sink.
- Use stdlib only. No external dependencies in example code.
- Do not import another example package; only `gitlike` is the allowed shared dependency.
- Keep examples self-contained. Prefer teaching code inline rather than introducing a new helper package unless a second consumer justifies it.
- Use secure defaults: `SHA-256` for identity, JSON for readable formats, and filesystem storage for durable examples.
- Do not add `any` to example APIs. Prefer explicit typed models.
- Use command-line or simple app wiring patterns that are easy to follow.
- Keep output meaningful: hashes, stats, graph traversal, dedup results, loaded objects, or error output that proves the pattern.

## 5. Testing and validation

- Examples MUST compile with the repo's Go toolchain.
- Example tests should verify the behavior the example teaches.
- Use `go test ./examples/...` as the standard local validation command for this folder.

## 6. Scope boundaries

- Examples are not the core library. They must not be used to bend the library into app-specific APIs.
- Examples may define small local types and helper structs when needed for teaching, but they should not become a second shared library.
- Keep the design consistent with `docs/specs/examples.md` and the repo root `AGENTS.md`.

## 7. Checklist

- [ ] Example stays focused on one real pattern
- [ ] README includes purpose, APIs used, walkthrough, Mermaid diagram, and run commands
- [ ] Code uses public APIs only and does not modify `cas`/`gitlike` for convenience
- [ ] Tests compile and verify the demonstrated behavior
- [ ] Example remains consistent with repo-level guidance and the examples spec

## 8. Signed pull-request workflow

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
