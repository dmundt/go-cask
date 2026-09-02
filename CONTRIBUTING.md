# Contributing to go-cask

Thank you for contributing! This project is **specified, not guessed**: the
`.github/instructions/` folder is the single source of truth for design,
contracts, and conventions. Before anything else, read
`.github/instructions/AGENT.md` — it governs every other spec and this
repository.

## Development workflow

### Prerequisites

- Go **1.27** (the toolchain is self-managing via `go.mod`; a Go ≥ 1.21
  install auto-downloads it).
- `git` — the repo uses the branch and tag conventions in
  `.github/instructions/branch-naming.instructions.md` and
  `.github/instructions/versioning.instructions.md`.

### Where things live

| Path        | What it is                                                  |
| ----------- | ----------------------------------------------------------- |
| `cas/`      | The public core library (package `cas`) — see `cas-core`    |
| `internal/` | Implementation detail: `web` (the viewer), `storage`, `index` — not importable outside the module |
| `examples/gitlike/`  | Reference example object model (package `gitlike`)          |
| `cmd/cask`  | The single entry point: CLI store ops + embedded viewer (`cask web`) — spec: `cli.instructions.md` |
| `examples/` | Runnable example programs (`examples.instructions.md`)      |
| `.github/instructions/` | The specification set (19 files)                    |

### The dev loop

```text
go build ./...
go vet ./...
go test -race ./...            # correctness + the lock-free read path
go test -bench=. -benchmem     # performance (performance spec §5)
go run ./cmd/cask web          # the embedded viewer (admin UI, loopback)
go run ./cmd/cask              # CLI store operations
gofmt -l .                     # must be empty
```

Rules from the specs that always apply:

- Standard library only (external deps need justification + vendoring) —
  `coding-guidelines` §3.
- No `any`/reflection in exported APIs — `cas-core` §2, `coding-guidelines`
  §8.
- Doc comments on every exported identifier — `coding-guidelines` §7.
- Extend don't modify: `cas` and `gitlike` are frozen — `extensions` §1.
- The CAS laws must stay green — `testing-strategy` §1.

### Making a change

1. Branch from `main` with a type prefix:
   `git checkout -b feat/<description>` (see `branch-naming` §2).
2. Implement, keeping the relevant spec as the contract. If the change is
   **material** (new requirement, contract change), bump the owning
   instruction file's frontmatter `version` by one (AGENT.md §3) and update
   `defaults.instructions.md` if a default changes (defaults §8).
3. Add tests: unit + the relevant CAS laws; run `-race`.
4. Commit with Conventional Commits (`feat:`, `fix:`, …; `BREAKING CHANGE:`
   footer for breaking changes) — `versioning` §4.
5. Open a PR into `main`; the CI gates (`gofmt`, `go vet`, `go test -race`,
   benchstat) must pass.
6. Delete the branch after merge.

### Changing a spec

Spec changes follow the same flow, plus AGENT.md's maintenance checklist
(§11): frontmatter rules, terminology (§6), cross-reference updates, version
bumps, and registration in AGENT.md §10 + `copilot-instructions.md`'s related
specs. Run the folder audit (frontmatter, file refs, diagram balance) before
opening the PR.

## Reporting issues

- Bugs: include the Go version, the backend in use, and a minimal repro;
  note which CAS law or spec contract is violated.
- Design questions: point at the relevant instruction file and section.

## License

By contributing you agree that your work is licensed under the MIT License
(see [LICENSE](LICENSE)).
