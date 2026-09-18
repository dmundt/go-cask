# Contributing

This project is intentionally organized in layers so the core storage model stays generic while application behavior remains clear and explicit.

## Repository layout

- `cas/` — the generic storage core
- `gitlike/` — a reference object model built on top of `cas`
- `cas/backend/` — storage backends such as filesystem and memory
- `examples/` — runnable examples and usage patterns
- `docs/specs/` — normative architecture and implementation documentation
- `website/` — public documentation for developers and adopters

## Public docs versus source-of-truth docs

The public website explains the project and its ideas. The normative implementation rules live in `docs/specs/` and in the repository root `AGENTS.md`.

Keep the public site focused on:

- architecture
- content addressing
- object identity
- code examples
- recipes and usage guidance

Avoid exposing internal maintenance policy or agent instructions in the public documentation.

## Verification

Before submitting a change, run:

```bash
bash ./scripts/verify.sh
```

This is the repository’s main preflight gate.

## Good contributions

Good changes tend to be small, technical, and easy to reason about. Prefer clear examples, focused docs, and architecture-aligned improvements over broad rewording or marketing-heavy copy.
