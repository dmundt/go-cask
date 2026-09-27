---
type: Agent Instructions
title: AGENT — go-cask Instruction Folder Guide
description: Meta-guide for docs/specs/ — naming, frontmatter, structure, normative language, terminology, cross-referencing, precedence, checklist.
version: v34
tags: [go-cask]
status: stable
---

# AGENT — go-cask Instruction Folder Guide

Governs this folder's files. Every agent (Copilot, other AI tooling) and human editor of any
`docs/specs/*.md` MUST follow it — one coherent set. Scope: normative specs (architecture, coding
style, security, APIs, viewer design, examples, performance, testing, operations). Root `AGENTS.md`
aggregates them, outside this folder; same style where applicable.

## Table of contents

- [Purpose and scope](#1-purpose-and-scope)
- [File naming](#2-file-naming)
- [Frontmatter](#3-frontmatter-required)
- [Document structure](#4-document-structure)
- [Normative language and tone](#5-normative-language-and-tone)
- [Terminology](#6-terminology-shared-glossary)
- [Cross-referencing](#7-cross-referencing)
- [Maintenance checklist](#10-editing-and-maintenance-checklist)

## 1. Purpose and scope

- Single source of truth: how go-cask is designed, built, secured, tested, operated.
- Every file: requirements (MUST/SHALL hold) plus context (why) — never project prose.
- New files only for a gap (cf. examples.md §5); extend an existing file first.
- `.agents/skills/`: instruction files too, but process, not specification — owner
  [`../../.agents/AGENT.md`](../../.agents/AGENT.md); **not** members of this set. Two-key frontmatter,
  not §3's four keys. A skill MUST NOT restate a requirement this folder states; reference it instead.

## 2. File naming

Long docs, multiple numbered sections: shallow TOC under the H1 — 3–8 links, no deep nesting, only
when scanning benefits.

- Pattern `<Topic>.md`: one topic per file, lowercase kebab-case domain noun. These files are the
  instruction set under `docs/specs/`, so filenames carry **no** `.instructions` suffix.
- Domain nouns: `api-design`, `backend-architecture`, `branch-naming`, `cas-core`, `cli`,
  `coding-guidelines`, `consistency`, `defaults`, `examples`, `extensions`,
  `frontend-architecture`, `library-design`, `object-versioning`, `operations`, `performance`,
  `testing-strategy`, `versioning`, `viewer-design`, `viewer-security`.
- No redundant prefixes (never `go-`, never "instructions" inside a name). `-api`/`-design`/
  `-security` suffixes disambiguate viewer facets. Here this meta-guide is the sole `AGENT.md`; other
  package-local guides (`cas/AGENT.md`, `scripts/AGENT.md`, `benchmarks/AGENT.md`, …) are instruction
  files under `docs/AGENT.md` §1.1, not members of this set.

## 3. Frontmatter (required)

Every topic file MUST begin with exactly four YAML keys, in this order:

```yaml
---
type: Specification
title: {Topic} — go-cask
description: One sentence stating what the file requires/documents and who it applies to.
version: v5
---
```

- `type` = OKF concept type (`docs/AGENT.md` §1.2): `Specification` for topic files, `Agent
  Instructions` here. `title` = H1 minus `# `. `description` one line. `version: v1` upward, +1 on
  **material** change (requirements, contracts, structure), never cosmetic — rule and gate check owned
  by `docs/AGENT.md` §5. No blank line before `---`.
- Index files: `index.md` carries `okf_version: "0.2"`, **no** `type` (`docs/AGENT.md` §1.3); four
  keys `okf_version`/`title`/`description`/`version`.
- `AGENT.md` guides — this meta-guide, every package-local guide outside this folder — same four keys
  (`docs/AGENT.md` §1.1): `title` identical to H1, `version` moving on a material change to the guide.
- `tags:`/`status:` only optional keys, only where applicable (this meta-guide: `tags: [go-cask]`,
  `status: stable`). No other keys.

## 4. Document structure

1. H1 `# {Title}` identical to frontmatter title.
2. Intro paragraph (2–6 lines) after the H1 — plain prose, not a blockquote: what the file governs,
   plus a closing `Related:` line of backticked specs when it depends on siblings.
3. Numbered `## N.` sections (`## 1. <topic>` — `Purpose and scope` where it fits, a domain noun
   otherwise); subsections `### 3.1` (or `### 4.13`).
4. No `---` separators in the body: a spec's only `---` lines are the two frontmatter delimiters.
5. Closing `## N. Checklist` of acceptance items derived from the body.

- Requirements once, referenced, never duplicated with drift — pointer-first trimming and the
  no-duplication rule are `docs/AGENT.md` §2. Tables for enumerations/contracts; fenced code (`go`,
  `yaml`, `text`, `mermaid`) for concrete shapes; prose for rationale. Raw HTML, HTML comments, tags,
  layout wrappers, HTML/XML/SVG fences forbidden in every `.md` file (`docs/AGENT.md` §1.4).
  Reference the shared glossary (§6); never redefine terms.
- **Codec wrappers cascadeable by design.** Any new `Codec[T]` wrapper MUST preserve the
  single-stack rule: an outer codec wraps an inner codec, transforms only the serialized bytes.
  Canonical pattern in docs and tests; never add constructor variants that break composition.

## 5. Normative language and tone

- **MUST/MUST NOT/SHALL/SHALL NOT** = hard requirements. **SHOULD/SHOULD NOT** = strong
  recommendation, documented reason. **MAY** = optional; state the decision point.
- Imperative, present tense, active voice; no marketing/"we"/filler. Rules checkable. One provenance
  sentence in the intro, never repeated per section.

## 6. Terminology (shared glossary)

All files MUST use exactly these terms (forbidden synonyms listed):

| Term | Meaning |
|---|---|
| go-cask / CASK | The project (Content-Addressable Store Kit). |
| CAS / CASK | Acronyms, ALL-CAPS: "CAS" = Content-Addressable Store, "CASK" = Content-Addressable Store Kit. Never lowercase — lowercase `cas` the Go package (next row). |
| `cas` package | Generic core library (`cas/`, `package cas`). |
| `gitlike` package | Shared reference object-model library at `gitlike/`: Blob/Tree/Commit/Tag, Repository, Resolver. NOT part of `cas`. |
| the viewer | Embedded technical browser UI (`internal/web/`). Not "debug UI". |
| viewer API | Hypermedia surface under `/viewer/` (HTML). |
| CAS API | JSON HTTP API **pattern** demonstrated by `examples/api` — the product ships no network surface. |
| `Backend` | Non-generic byte-storage interface; impls in subpackages: `fs.Backend` (disk), `cas/backend/mem`'s `Backend` (in-memory, imported as `backmem`). |
| `Store[T]` / `Digest` | Generic typed store / content address: raw digest bytes (zero value = absent), one lowercase-hex string; the client's `Hasher` validates it. Printable `sha256:hexdigest` form is a client rendering (`cas/hash/sha256`). |
| fan-out / lock-free reads / CAS laws | Directory layout (`FanOut`/`FanLevels`); `Get`/`Exists`/`List`/`Stats` take no lock; testing-strategy §1 invariants. |
| `hash` vs `digest` | **`digest` = the value**: `cas.Digest`, `Hasher.Digest`, `pathToDigest`, `digestPath`, `Digest.Prefix`, `test.DigestData`. **`hash` = the algorithm, the user-facing word**: `Hasher`, `cas/hash/sha256`, `hash.Hash`, "hash-on-write", CLI/API/viewer `{hash}` params, `hash` JSON keys/UI labels. Frozen wire tag: `gitlike.TreeEntry.Hash` keeps `json:"hash,omitzero"` (a rename re-addresses every tree); the Go field waits for the v2 rename. Never name a new identifier holding a `cas.Digest` "hash"; never rename a user-facing `hash` or stored tag to "digest". |
| `examples/` vs `Example` functions | Two artifacts, both "example". `examples/<name>/` = runnable teaching **programs** (`package main` + README + mermaid, examples.md §2). `Example*` in `_test.go` = **executable godoc documentation**: rendered by pkg.go.dev, executed by `go test`, output pinned by `// Output:` (coding-guidelines §10). Belongs to the package it documents, lives in that package's external `_test` package, public API only — never a test helper: the snippet must be copy-pasteable. Never "consolidate" one into the other; never move an Example to another package (it stops being documentation there). |

**content-addressable store**: CASK, go-cask, the `cas` package, a `Backend`, any concrete
implementation. **content-addressable storage**: the general technique, architecture pattern,
conceptual explanation. `CAS` = **Content-Addressable Store**; `CASK` = **Content-Addressable Store
Kit**. Never swap the phrases mechanically when context needs a different scope.

Forbidden/deprecated: "debug UI"/`debug_ui` → **viewer**; "go-coding-guidelines" →
**coding-guidelines**; "Repository/Resolver in the core" → the **gitlike shared reference layer**;
"sharded paths" → **fan-out**; "hash" for a digest *value* (a Go identifier holding a `cas.Digest`)
→ **digest** (see glossary row; "hash" stays correct for the algorithm and user-facing names); bare
"example" → say `examples/<name>` program or `Example` function.

## 7. Cross-referencing

- Sibling files: backticked path (`docs/specs/cas-core.md`) or short backticked name (`cas-core.md`)
  in a `Related:` line. Sections by number (`§4.4`, `P-05`, `§2`), never approximate prose.
- A contract change updates **all** referencing files in one pass; `grep` of the changed term across
  `docs/specs/` and `.github/` clean. Inventory: this folder's `index.md`; path-first lookup:
  `docs/index.md`; new instruction file registered there. Root `AGENTS.md` routes and lists none —
  names a rule's owner, never the rule.

## 8. Precedence and conflict resolution

On conflict this order decides (highest first):
1. **Security** — `viewer-security.md` non-negotiable for the viewer; nothing may weaken it.
2. **Common conventions** — `api-design.md`, `coding-guidelines.md`, `library-design.md`.
3. **Architecture** — `cas-core.md` (component contracts); `backend-architecture.md`/
   `frontend-architecture.md` compose them; `performance`/`testing-strategy`/`operations` refine.
4. **Examples** — `examples.md` may demonstrate, never redefine.
5. **This file (AGENT.md)** — governs the documents themselves.

Fix the **more specific** document to match the more general one, unless the specific document ranks
higher here. Never leave two contradicting statements in the folder.

- **A direct instruction from the user outranks every file here.** These files state the standing
  contract; repository code MUST follow them unless the user explicitly overrides them for the work
  at hand. Never dropped silently: security — an instruction that would weaken `viewer-security.md`
  is reported back, not obeyed blind and not ignored.

## 9. Diagram and formatting rules

- Mermaid for relationships/flow: `classDiagram` for object models, `flowchart` for flows; one
  diagram per concept, beside what it visualizes.
- **Mermaid blocks MUST be balanced** (every ```mermaid opener has a matching closer; unbalanced
  fences break rendering and swallow the rest of the file). Only exception: an explicitly stated
  illustrative fragment, labeled in the surrounding text. Never leave one unbalanced without that
  statement.
- ASCII allowed alongside mermaid (raw/terminal), box-aligned; prefer mermaid when both exist.
- Fences always tagged (`go`, `yaml`, `text`, `json`, `mermaid`); raw HTML, HTML comments and
  HTML/XML/SVG fences forbidden (`docs/AGENT.md` §1.4). Pipe tables with a header separator;
  `:---:` only where meaningful. Line width ≤ ~100; LF; UTF-8. Requirements numbered (`P-01…`) only
  when cross-referenced. `internal/build/core/docs` enforces the policy, run by
  `./scripts/verify.sh` in both scopes.

## 10. Editing and maintenance checklist

Before committing any change to a file in this folder:
- [x] Frontmatter present; `title` == H1; one-line `description`
- [x] `version` bumped on material change (requirements/contracts/restructure), not cosmetic — the gate enforces the bump's presence: `internal/build/core/versioning` from `./scripts/verify.sh` names a changed versioned file whose `version` did not move, and never judges materiality
- [x] Structure per §4 (no body `---` separators); checklist where applicable
- [x] Terminology matches §6 (no "debug UI", "go-coding-guidelines", "Repository in core")
- [x] Normative language per §5
- [x] Cross-references updated in ALL files mentioning the term; `grep` of old terms across `docs/specs/` and `.github/` returns nothing
- [x] New files registered in `docs/specs/index.md` (and `docs/index.md` when a new path pattern appears)
- [x] No contradictions with higher-precedence files (§8)
- [x] Diagrams valid; fences tagged; every mermaid block balanced unless labeled as an illustrative fragment
- [x] No raw HTML, HTML comments, or HTML/XML/SVG fences — checked by `internal/build/core/docs`, which walks every tracked `.md` (mermaid balance included)

## 11. Signed pull-request workflow

Signed commits, no server-side rebase or update-branch: owner [`docs/specs/landing.md`](landing.md)
§5, not restated here.

