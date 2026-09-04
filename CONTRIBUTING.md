# Contributing to go-cask

Thank you for contributing! This project is **specified, not guessed**: the
`docs/instructions/` folder is the single source of truth for design,
contracts, and conventions. Before anything else, read
`docs/instructions/AGENT.md` — it governs every other spec and this
repository.

## Development workflow

### Prerequisites

- Go **1.27** (the toolchain is self-managing via `go.mod`; a Go ≥ 1.21
  install auto-downloads it).
- `git` — the repo uses the branch and tag conventions in
  `docs/instructions/branch-naming.md` and
  `docs/instructions/versioning.md`.

### Where things live

| Path                 | What it is                                                  |
| -------------------- | ----------------------------------------------------------- |
| `cas/`               | The public core library (package `cas`) — see `cas-core`    |
| `internal/`          | Implementation detail: `web` (the viewer), `index` — not importable outside the module |
| `examples/gitlike/`  | Reference example object model (package `gitlike`)          |
| `cmd/cask`           | The single entry point: CLI store ops + embedded viewer (`cask web`) — spec: `cli.md` |
| `examples/`          | Runnable example programs (`examples.md`)                   |
| `docs/instructions/` | The specification set (19 spec files + AGENT.md)            |

### Design decisions & constraints

Before changing structure, APIs, or scope, read the README's *Design
principles & grounding* and the owning specs. The constraints that shape
every change:

- **Single-host kit**: no network JSON API, no SDK, no server binary
  (backend-architecture §1) — HTTP exposure is an `examples/api` pattern.
- **One-directional dependencies**: `cas`/`internal`/`cmd` MUST NOT import
  `examples/`; examples MUST NOT import `internal/` and are self-contained
  except the `gitlike` shared reference library (coding-guidelines §9,
  examples §2 rule 11).
- **Byte-layer viewer**: `internal/web` shows objects, bytes, and integrity —
  no typed references or graphs (viewer-design §7).
- **Lean generic core**: only the cas-core §7.1 surface is stable; adding
  exported surface is a spec change first (library-design §1).
- **Policy-free byte layer**: GC/prune take app-supplied roots; roots are
  pins — there is no per-object pinned property (consistency §4).
- **Examples teach, never ship**: `gitlike` is the shared reference model;
  the product never imports examples.

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
   `defaults.md` if a default changes (defaults §8).
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
