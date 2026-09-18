# Contributing

This project is intentionally organized in layers.

## Repository layout

- `cas/` — the generic storage core
- `gitlike/` — reference object model built on top of `cas`
- `cas/backend/` — storage backends such as filesystem and memory
- `examples/` — runnable example programs
- `docs/specs/` — normative architecture and implementation documentation
- `website/` — public docs site for developers and adopters

## Public docs and source-of-truth docs

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

This is the repo’s main preflight gate.
