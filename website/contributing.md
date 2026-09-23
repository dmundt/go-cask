# Contributing

The project is organized in layers so the core storage model stays generic
while application behavior remains explicit. Keep changes aligned with that
boundary.

## Repository layout

- `cas/` — the generic storage core (byte layer + typed layer) plus the
  app-facing helpers `cas/pack`, `cas/bloom`, `cas/verify`, `cas/refs`,
  `cas/repo`, and `cas/cache`
- `cas/backend/` — storage backends (filesystem, memory, opt-in packfile) and
  the portable archive helper `cas/backend/snapshot`
- `cas/codec/` — codec implementations (JSON, gob, binary, CBOR, compression
  wrappers)
- `cas/hash/` — hasher implementations (SHA-256, SHA-512, SHA-512/256)
- `gitlike/` — a reference object model built on top of `cas`
- `examples/` — runnable examples and usage patterns
- `internal/` — implementation details not importable outside the module
- `docs/specs/` — the normative specification set
- `website/` — this public documentation site

## Public docs versus normative specs

This website explains the project for adopters: architecture, concepts,
recipes, and accurate code examples. The normative implementation rules that
constrain agent- and human-assisted changes to the repository live in
`docs/specs/` and the repository root `AGENTS.md`. Keep that distinction: the
public site should never need a reader to open `AGENTS.md` to understand how
to use the library.

## Before you submit a change

Run the project verification gate (Bash — Git Bash or WSL on Windows):

```bash
bash ./scripts/verify.sh
```

This runs `gofmt`, `go mod tidy` drift checks, `go vet`, import-boundary
checks, `govulncheck`, and the test suite with race detection and per-package
coverage — the same gate CI runs on every push and pull request.

## Good contributions

Good changes are small, technical, and easy to reason about:

- a focused fix or feature with tests that exercise it
- documentation and code examples kept in sync with the actual API
- changes that respect the existing layer boundaries (for example: `cas/`
  never imports `examples/` or `gitlike/`)

Update `CHANGELOG.md` for any user-visible change, following the existing
Keep a Changelog structure.
