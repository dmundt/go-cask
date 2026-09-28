---
type: Agent Instructions
title: AGENT — go-cask (docs/ folder)
description: Meta-guide for docs/ — OKF format, naming, versioning, trimming, the instruction-file budget, the maintenance checklist. Governs docs/index.md, docs/specs/, docs/design/ and any later subdirectory; the benchmark guide lives at benchmarks/README.md; root AGENTS.md is the agent entry point before it.
version: v24
---

# AGENT — go-cask (docs/ folder)

Governs all non-instruction docs in `docs/`. Instruction specs under `docs/specs/` have their own `AGENT.md`. **Before any change**, read `docs/index.md` first (path → spec table), then this file (or `docs/specs/AGENT.md`) for conventions.

## Table of contents

- [Format — OKF v0.2](#1-format--okf-v02)
- [Trimming](#2-trimming)
- [Adding a file](#3-adding-a-file)
- [Removing/renaming a file](#4-removingrenaming-a-file)
- [Versioning](#5-versioning)
- [Constructor naming](#6-constructor-naming-in-example-code)
- [Diagram and formatting rules](#7-diagram-and-formatting-rules)
- [Editing and maintenance checklist](#8-editing-and-maintenance-checklist)

## 1. Format — OKF v0.2

Every `.md` file in `docs/` MUST be a valid OKF v0.2 concept document.

### 1.1 Frontmatter

| Field | When | Example |
|---|---|---|
| `type:` | Every non-index file | `Specification`/`Design Document`/`Guide`/`Agent Instructions` |
| `okf_version: "0.2"` | Only `index.md` files | |
| `title:` | Always | |
| `description:` | Always (single line) | |
| `version:` | Always (custom key) | `v1` |
| `tags:`/`status:` | Optional (`stable`/`draft`/`deprecated`) | |

`type` and `version` required. Other OKF keys (`sources`, `generated`,
`verified`, `stale_after`, `tags`, `status`) optional, MUST be used when
applicable.

Every `AGENT.md` in the repository carries the same four keys, not only those under
`docs/`: a package-local guide (`cas/AGENT.md`, `scripts/AGENT.md`, `benchmarks/data/AGENT.md`)
gets its own `type`, `title` (identical to its H1), one-line `description` and `version` for
the same reason a spec does — a reader can tell how current the instructions are. Keys = the
table above; no package-local guide adds one.

### 1.2 Type values and locations

`Specification` → every `docs/specs/` file; `Design Document` → every `docs/design/` file; `Guide` → `benchmarks/README.md` and similar how-to; `Agent Instructions` → every `AGENT.md`, wherever it lives (root `AGENTS.md`, `docs/AGENT.md`, package-local guides under `cas/`, `cas/codec/`, `cas/verify/`, `benchmarks/`, `benchmarks/data/`, `examples/`, `scripts/`, `website/`, `.agents/` and `.github/`).

### 1.3 Index files

Every subdirectory MUST have an `index.md`. Root `docs/index.md` = top-level rule index; subdirectory indexes (`docs/design/index.md`, `docs/specs/index.md`) shorter. All index files carry `okf_version: "0.2"` and no `type`.

### 1.4 Markdown-only content

Raw HTML forbidden in every Markdown file, including HTML comments, tags,
layout wrappers, HTML/XML/SVG code fences. Use Markdown constructs, Mermaid,
or links instead. HTML belongs only in dedicated non-Markdown assets such as
viewer mockup; it MUST NOT be copied into a `.md` file.

## 2. Trimming

- Every doc must earn its bytes: replaceable-by-a-pointer → pointer; a section repeating another spec → remove and reference the canonical source.
- No cross-document duplication: each fact lives in one place (`defaults.md` or its owning spec), referenced not restated.
- **Mermaid diagrams are exempt** from trimming (visualize complex relationships, kept even when large).
- Deferred-feature sketches, historical rationales and single-run benchmark samples → remove, replace with a pointer to the deferral record.
- Keep the three directories: `docs/specs/` (normative, 22 files), `docs/design/` (non-normative), `benchmarks/README.md` (guide beside the benchmark code, outside `docs/`).

### 2.1 Instruction files are routers, and they have a budget

An agent instruction file — root `AGENTS.md` and every package-local `AGENT.md` — is read into
context while an agent works in the tree it governs, so its bytes recur. It routes; it does not
specify.

- **Every instruction file has a byte ceiling**, in `internal/build/policy`'s
  `InstructionBudgets` table: `internal/build/docs` measures each file against it and
  `go run ./cmd/buildtool markdown-integrity` fails above it. Each number is a ratchet set just
  above the file's size when the ceiling was added — room for a rule or two, none to grow into a
  specification. Raising one is a deliberate policy change in the change that needs it, never the
  fix for a failure.
- Root `AGENTS.md` MUST stay **≤ 6 KiB**: read at the start of every session whatever the change,
  it is a router and MUST sit far below its ceiling — a ceiling, never a target to fill.
- A rule belongs to exactly one owning file; the router names that file and section. An
  instruction file carries no prose, rationale, restated rule, duplicated diagram, table row or
  code example — moving a block out means moving its content to the owner, never deleting it.
  Narration stating no rule (a past port, a dated incident, a status aside) is removed; a rule its
  owner already states becomes a pointer.
- Discoverability stays routed, not restated: the path→spec map is [`index.md`](index.md), the
  tree list is [`../README.md`](../README.md) "Repository layout". Route through those rather than
  growing either into a second inventory.
- Telegraphic style in every instruction file: fragments over sentences, tables over paragraphs,
  backticked paths over descriptions. Telegraphic is not terser rules — every rule, fact, path,
  command, identifier, number, link and code block survives.

## 3. Adding a file

1. Create at correct `docs/` path. 2. Add frontmatter (`type`, `title`, `description`, `version`). 3. Add row to parent `index.md`. 4. If it introduces a new agent-matchable path pattern, add row to `docs/index.md`.

## 4. Removing/renaming a file

1. Remove/update its row in parent `index.md` and in `docs/index.md`. 2. Update all cross-references in other docs. 3. Bump `version` on every affected file.

## 5. Versioning

`version: v1, v2, …` — bump by one on material change; cosmetic fixes (typos/formatting) do NOT bump. `version` = custom key (OKF defines none) — mechanism for consumers to detect staleness.

## 6. Constructor naming (in example code)

Go examples in these docs MUST name constructors per `coding-guidelines.md` §1: plain `New()` when a package exposes one primary type (`fs.New`, `backmem.New`, `json.New[T]`, `gob.NewRaw[T]`, `lru.New`); `NewType()`/`NewXyz()` for multiple important types or a non-primary constructed type (`cas.NewDigest`, `cas.NewWalker`, `cas.New`). `prefetch.NewSmartCache` is the frozen-surface exception keeping its name (cas-core §7.1). Code wins over example — update the example (a non-compiling doc example is a defect).

## 7. Diagram and formatting rules

- Mermaid for relationships/flow; ASCII only alongside mermaid (raw views).
- Code fences always carry a language tag (`go`, `yaml`, `text`, `mermaid`).
- In prose, never use the ampersand as a substitute for `and` unless the text is code, a literal symbol, a diagram, or a mermaid block where syntax or the source domain requires the symbol.
- Line width ≤ ~100 chars; LF endings; UTF-8.

## 8. Editing and maintenance checklist

Before committing any change to a file in `docs/` (outside `docs/specs/`):
- [ ] OKF frontmatter present (`type`, `title`, `description`, `version`; `okf_version: "0.2"` for indexes) — on every `AGENT.md` too, not only files under `docs/` (§1.1)
- [x] `version` bumped on material change — enforced by `internal/build/versioning` from `verify.sh` (it detects a missing bump, not a cosmetic one)
- [ ] No duplication — check `defaults.md` and owning specs first
- [ ] Cross-references updated in ALL files mentioning the changed term
- [ ] Ampersand used only where required by code, literal symbol text, or diagram syntax
- [ ] On add/remove/rename, `docs/index.md` and the parent `index.md` updated
- [ ] Mermaid blocks balanced; all code fences tagged
- [ ] No raw HTML, HTML comments, or HTML/XML/SVG fences
- [ ] LF endings, UTF-8
- [ ] An instruction file changed: within the §2.1 budget, and every block removed from it relocated to its owner

The same item list applies, shape adapted to the artifact, when adding or
changing a repository skill under `.agents/skills/`; that directory's own
[`AGENT.md`](../.agents/AGENT.md) governs the OKF fields a skill replaces with
its two-key frontmatter, its size budget, its provenance requirement.

## 9. Signed pull-request workflow

Signed commits, no server-side rebase or update-branch: owner [`docs/specs/landing.md`](specs/landing.md) §4–§5.
