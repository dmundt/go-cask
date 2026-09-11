---
type: Agent Instructions
title: AGENT — go-cask Instruction Folder Guide
description: The meta-guide for docs/specs/ — file naming, frontmatter, document structure, normative language, shared terminology, cross-referencing, precedence, and the maintenance checklist that keeps every instruction file consistent.
version: v21
tags: [go-cask]
status: stable
---

# AGENT — go-cask Instruction Folder Guide

Governs the other files in this folder. Every agent (Copilot, other AI tooling) and every human maintainer editing any `docs/specs/*.md` MUST follow it so the folder stays one coherent specification set. Scope: the normative specs (core architecture, coding style, security, APIs, viewer design, examples, performance, testing, operations). The repo-root `AGENTS.md` is the aggregator pointing at them (outside this folder; follows the same style where applicable).

## 1. Purpose and scope

- The folder is the **single source of truth** for how go-cask is designed, built, secured, tested, and operated.
- Every file states **requirements** (MUST/SHALL be true) and **context** (why), not project prose.
- New files only when a real gap exists (cf. examples.md §5); prefer extending an existing file.

## 2. File naming

- Pattern `<Topic>.md`, one topic per file, lowercase kebab-case domain noun. These files are the instruction set under `docs/specs/`, so filenames carry **no** `.instructions` suffix.
- Topics are domain nouns: `api-design`, `backend-architecture`, `branch-naming`, `cas-core`, `cli`, `coding-guidelines`, `consistency`, `defaults`, `examples`, `extensions`, `frontend-architecture`, `library-design`, `object-versioning`, `operations`, `performance`, `testing-strategy`, `versioning`, `viewer-design`, `viewer-security`.
- No redundant prefixes (never `go-`, never "instructions" inside a name). `-api`/`-design`/`-security` suffixes disambiguate viewer facets. This meta-guide is the sole `AGENT.md`.

## 3. Frontmatter (required)

Every topic file MUST begin with exactly four YAML keys, in this order:

```yaml
---
type: Specification
title: <Topic> — go-cask
description: One sentence stating what the file requires/documents and who it applies to.
version: v5
---
```

- `type` is the OKF concept type (`docs/AGENT.md` §1.2): `Specification` for every topic file here, `Agent Instructions` for this meta-guide. `title` matches the H1 exactly (minus `# `). `description` is one line. `version` starts at `v1`; increment by one on **material** change (requirements, contracts, structure) — not on cosmetic fixes (typos, formatting, wording, line endings). No blank line before `---`.
- Index files are the exception: this folder's `index.md` carries `okf_version: "0.2"` and **no** `type` (`docs/AGENT.md` §1.3), so its four keys are `okf_version`/`title`/`description`/`version`.
- `tags:` and `status:` are the only optional keys, and only where applicable (this meta-guide carries `tags: [go-cask]`, `status: stable`). No other keys.

## 4. Document structure

1. H1 `# <Title>` identical to frontmatter title.
2. Intro paragraph (2–6 lines) immediately after the H1 — plain prose, not a blockquote: what the file governs, and, where the file depends on siblings, a closing `Related:` line of backticked specs it must be read with.
3. Numbered `## N.` sections (`## 1. <topic>` — `Purpose and scope` where that fits, a domain noun otherwise); subsections `### 3.1` (or `### 4.13`).
4. No `---` separators in the body: a spec's only `---` lines are the two frontmatter delimiters.
5. Closing `## N. Checklist` of acceptance items derived from the body.

- Requirements stated once and referenced, never duplicated with drift. Tables for enumerations/contracts; fenced code (`go`, `yaml`, `text`, `mermaid`) for concrete shapes; prose for rationale. Reference the shared glossary (§6); do not redefine terms.

## 5. Normative language and tone

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
| `Store[T]` / `Digest` | Generic typed store / content address: raw digest bytes (zero value = absent), rendered as one lowercase-hex string; the client's `Hasher` validates it. The printable `sha256:hexdigest` form is a client rendering (`cas/hash/sha256`). |
| fan-out / lock-free reads / CAS laws | Directory layout (`FanOut`/`FanLevels`); `Get`/`Exists`/`List`/`Stats` take no lock; the testing-strategy §1 invariants. |
| `hash` vs `digest` | **`digest` is the value**: `cas.Digest`, `Hasher.Digest`, `pathToDigest`, `digestPath`, `Digest.Prefix`, `test.DigestData`. **`hash` is the algorithm and the user-facing word**: `Hasher`, `cas/hash/sha256`, `hash.Hash`, "hash-on-write", the CLI/API/viewer `{hash}` params and `hash` JSON keys/UI labels. A stored wire tag is frozen: `gitlike.TreeEntry.Hash` keeps `json:"hash,omitzero"` (renaming the tag would re-address every tree), and the Go field is scheduled for the v2 rename. Rule: never name a new identifier that holds a `cas.Digest` "hash", and never rename a user-facing `hash` or a stored tag to "digest". |
| `examples/` vs `Example` functions | Two different artifacts that both say "example". `examples/<name>/` = runnable teaching **programs** (`package main` + README + mermaid, examples.md §2). `Example*` functions in `_test.go` = **executable godoc documentation**: rendered by pkg.go.dev, executed by `go test`, output pinned by `// Output:` (coding-guidelines §10). An Example belongs to the package it documents, lives in that package's external `_test` package, and MUST use public API only — never a test helper, since the rendered snippet must be copy-pasteable. Never "consolidate" one into the other, and never move an Example to another package (it stops being documentation there). |

Forbidden/deprecated: "debug UI"/`debug_ui` → **viewer**; "go-coding-guidelines" → **coding-guidelines**; "Repository/Resolver in the core" → they are the **gitlike shared reference layer**; "sharded paths" → **fan-out**; "hash" for a digest *value* (a Go identifier holding a `cas.Digest`) → **digest** (see the glossary row above; "hash" stays correct for the algorithm and for user-facing names); bare "example" (say `examples/<name>` program or `Example` function).

## 7. Cross-referencing

- Sibling files by backticked path (`docs/specs/cas-core.md`) or short backticked name (`cas-core.md`) in a `Related:` line. Reference sections by number (`§4.4`, `P-05`, `§2`), never approximate prose.
- A contract change updates **all** referencing files in one pass; `grep` for the changed term across `docs/specs/` and `.github/` must be clean. The `AGENTS.md` "Related specs" list MUST list every instruction file (add new ones).

## 8. Precedence and conflict resolution

On conflict this order decides (highest first):
1. **Security** — `viewer-security.md` is non-negotiable for the viewer; nothing may weaken it.
2. **Common conventions** — `api-design.md`, `coding-guidelines.md`, `library-design.md`.
3. **Architecture** — `cas-core.md` (component contracts); `backend-architecture.md`/`frontend-architecture.md` compose them; `performance`/`testing-strategy`/`operations` refine.
4. **Examples** — `examples.md` may demonstrate, never redefine.
5. **This file (AGENT.md)** governs the documents themselves.

Fix the **more specific** document to match the more general one, unless the specific document is higher in this order. Never leave two contradicting statements in the folder.

## 9. Diagram and formatting rules

- Mermaid for relationships/flow: `classDiagram` for object models, `flowchart` for flows; one diagram per concept next to what it visualizes.
- **Mermaid blocks MUST be balanced** (every ```mermaid opener has a matching closer; unbalanced fences break rendering and swallow the rest of the file). The only exception is an explicitly stated illustrative fragment, labeled in the surrounding text. Never leave one unbalanced without that statement.
- ASCII allowed alongside mermaid (raw/terminal) but box-aligned; prefer mermaid when both exist.
- Code fences always tagged (`go`, `yaml`, `text`, `json`, `html`, `mermaid`). Pipe tables with a header separator; `:---:` only where meaningful. Line width ≤ ~100; LF; UTF-8. Requirements numbered (`P-01…`) only when cross-referenced.

## 10. Editing and maintenance checklist

Before committing any change to a file in this folder:
- [x] Frontmatter present; `title` == H1; one-line `description`
- [x] `version` bumped on material change (requirements/contracts/restructure), not cosmetic
- [x] Structure per §4 (no body `---` separators); checklist where applicable
- [x] Terminology matches §6 (no "debug UI", "go-coding-guidelines", "Repository in core")
- [x] Normative language per §5
- [x] Cross-references updated in ALL files mentioning the term; `grep` of old terms across `docs/specs/` and `.github/` returns nothing
- [x] New files added to the `AGENTS.md` aggregator "Related specs" list
- [x] No contradictions with higher-precedence files (§8)
- [x] Diagrams valid; fences tagged; every mermaid block balanced unless labeled as an illustrative fragment
