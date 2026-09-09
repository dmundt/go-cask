---
type: Agent Instructions
title: AGENT — go-cask Instruction Folder Guide
description: The meta-guide for docs/specs/ — file naming, frontmatter, document structure, normative language, shared terminology, cross-referencing, precedence, and the maintenance checklist that keeps every instruction file consistent.
version: v13
tags: [go-cask]
status: stable
---

# AGENT — go-cask Instruction Folder Guide

Governs the other files in this folder. Every agent (Copilot, other AI tooling) and every human maintainer editing any `docs/specs/*.md` MUST follow it so the folder stays one coherent specification set. Scope: the normative specs (core architecture, coding style, security, APIs, viewer design, examples, performance, testing, operations). The repo-root `AGENTS.md` is the aggregator pointing at them (outside this folder; follows the same style where applicable).

## 1. Purpose & scope

- The folder is the **single source of truth** for how go-cask is designed, built, secured, tested, and operated.
- Every file states **requirements** (MUST/SHALL be true) and **context** (why), not project prose.
- New files only when a real gap exists (cf. examples.md §5); prefer extending an existing file.

## 2. File naming

- Pattern `<Topic>.md`, one topic per file, lowercase kebab-case domain noun. The folder name already says "instructions", so filenames carry **no** `.instructions` suffix.
- Topics are domain nouns: `api-design`, `backend-architecture`, `branch-naming`, `cas-core`, `cli`, `coding-guidelines`, `consistency`, `defaults`, `examples`, `extensions`, `frontend-architecture`, `library-design`, `object-versioning`, `operations`, `performance`, `testing-strategy`, `versioning`, `viewer-design`, `viewer-security`.
- No redundant prefixes (never `go-`, never "instructions" inside a name). `-api`/`-design`/`-security` suffixes disambiguate viewer facets. This meta-guide is the sole `AGENT.md`.

## 3. Frontmatter (required)

Every file MUST begin with exactly three YAML keys:

```yaml
---
title: <Topic> — go-cask
version: v5
description: One sentence stating what the file requires/documents and who it applies to.
---
```

- `title` matches the H1 exactly (minus `# `). `version` starts at `v1`; increment by one on **material** change (requirements, contracts, structure) — not on cosmetic fixes (typos, formatting, wording). `description` is one line. No other keys; no blank line before `---`.

## 4. Document structure

1. H1 `# <Title>` identical to frontmatter title.
2. Intro blockquote (2–6 lines): what the file governs, then a `Related:` line of backticked sibling specs it must be read with.
3. Numbered `## N.` sections from `## 1. Purpose & Scope`; subsections `### 3.1` (or `### 4.13`).
4. `---` between top-level sections.
5. Closing `## N. Checklist` of acceptance items derived from the body.

- Requirements stated once and referenced, never duplicated with drift. Tables for enumerations/contracts; fenced code (`go`, `yaml`, `text`, `mermaid`) for concrete shapes; prose for rationale. Reference the shared glossary (§6); do not redefine terms.

## 5. Normative language & tone

- **MUST/MUST NOT/SHALL/SHALL NOT** = hard requirements. **SHOULD/SHOULD NOT** = strong recommendation with documented reason. **MAY** = optional; state the decision point.
- Imperative, present tense, active voice; no marketing/"we"/filler. Rules checkable. One provenance sentence allowed in the intro (never repeated per section).

## 6. Terminology (shared glossary)

All files MUST use exactly these terms (forbidden synonyms listed):

| Term | Meaning |
|---|---|
| go-cask / CASK | The project (Content Addressable Store Kit). |
| CAS / CASK | Acronyms, ALL-CAPS: "CAS" = Content Addressable Store, "CASK" = Content Addressable Store Kit. Never lowercase — lowercase `cas` is the Go package (next row). |
| `cas` package | The generic core library (`cas/`, `package cas`). |
| `gitlike` package | Shared reference object-model library at `gitlike/`: Blob/Tree/Commit/Tag, Repository, Resolver. NOT part of `cas`. |
| the viewer | The embedded technical browser UI (`internal/web/`). Not "debug UI". |
| viewer API | The hypermedia surface under `/viewer/` (HTML). |
| CAS API | The JSON HTTP API **pattern** demonstrated by `examples/api` — the product ships no network surface. |
| `Backend` | The non-generic byte-storage interface; impls in subpackages: `fs.Backend` (disk), `memory.Backend` (in-memory). |
| `Store[T]` / `Hash` | Generic typed store / content address `algo:hexdigest` validated with `ParseHash`. |
| fan-out / lock-free reads / CAS laws | Directory layout (`FanOut`/`FanLevels`); `Get`/`Exists`/`List`/`Stats` take no lock; the testing-strategy §1 invariants. |

Forbidden/deprecated: "debug UI"/`debug_ui` → **viewer**; "go-coding-guidelines" → **coding-guidelines**; "Repository/Resolver in the core" → they are the **gitlike shared reference layer**; "sharded paths" → **fan-out**.

## 7. Cross-referencing

- Sibling files by backticked path (`docs/specs/cas-core.md`) or short backticked name (`cas-core.md`) in a `Related:` line. Reference sections by number (`§4.4`, `P-05`, `§2`), never approximate prose.
- A contract change updates **all** referencing files in one pass; `grep` for the changed term across `docs/specs/` and `.github/` must be clean. The `AGENTS.md` "Related specs" list MUST list every instruction file (add new ones).

## 8. Precedence & conflict resolution

On conflict this order decides (highest first):
1. **Security** — `viewer-security.md` is non-negotiable for the viewer; nothing may weaken it.
2. **Common conventions** — `api-design.md`, `coding-guidelines.md`, `library-design.md`.
3. **Architecture** — `cas-core.md` (component contracts); `backend-architecture.md`/`frontend-architecture.md` compose them; `performance`/`testing-strategy`/`operations` refine.
4. **Examples** — `examples.md` may demonstrate, never redefine.
5. **This file (AGENT.md)** governs the documents themselves.

Fix the **more specific** document to match the more general one, unless the specific document is higher in this order. Never leave two contradicting statements in the folder.

## 9. Diagram & formatting rules

- Mermaid for relationships/flow: `classDiagram` for object models, `flowchart` for flows; one diagram per concept next to what it visualizes.
- **Mermaid blocks MUST be balanced** (every ```mermaid opener has a matching closer; unbalanced fences break rendering and swallow the rest of the file). The only exception is an explicitly stated illustrative fragment, labeled in the surrounding text. Never leave one unbalanced without that statement.
- ASCII allowed alongside mermaid (raw/terminal) but box-aligned; prefer mermaid when both exist.
- Code fences always tagged (`go`, `yaml`, `text`, `json`, `html`, `mermaid`). Pipe tables with a header separator; `:---:` only where meaningful. Line width ≤ ~100; LF; UTF-8. Requirements numbered (`P-01…`) only when cross-referenced.

## 10. Editing & maintenance checklist

Before committing any change to a file in this folder:
- [x] Frontmatter present; `title` == H1; one-line `description`
- [x] `version` bumped on material change (requirements/contracts/restructure), not cosmetic
- [x] Structure per §4; `---` between sections; checklist where applicable
- [x] Terminology matches §6 (no "debug UI", "go-coding-guidelines", "Repository in core")
- [x] Normative language per §5
- [x] Cross-references updated in ALL files mentioning the term; `grep` of old terms across `docs/specs/` and `.github/` returns nothing
- [x] New files added to the `AGENTS.md` aggregator "Related specs" list
- [x] No contradictions with higher-precedence files (§8)
- [x] Diagrams valid; fences tagged; every mermaid block balanced unless labeled as an illustrative fragment
