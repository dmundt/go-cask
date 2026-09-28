---
type: Agent Instructions
title: AGENT — go-cask (docs/design/)
description: Router for docs/design/ — non-normative design artifacts (core-overview, design-history, viewer briefs, mockups); conventions in docs/AGENT.md.
version: v10
tags: [go-cask]
status: stable
---

# AGENT — go-cask (docs/design/)

Router, not manual. Governs `docs/design/`; carries no rule body it does not own. Read
[`docs/AGENT.md`](../AGENT.md) for the conventions this file does not own.

| Topic | Owner |
| --- | --- |
| Entry point; path → spec, longest match | `docs/index.md` |
| Repo-root session rules | `AGENTS.md` |
| Frontmatter, trimming, diagrams, checklist | `docs/AGENT.md` §1.1–1.4, §2, §3, §5, §7, §8 |
| Instruction files are routers; 3 KiB ceiling (this file) | `docs/AGENT.md` §2.1 |
| Rebuild branch from `main`, `git cherry-pick -S`, `git verify-commit`, `--force-with-lease` | `docs/specs/landing.md` |
| Viewer design contract `viewer-brief.md` serves | `docs/specs/viewer-design.md` |
| What lives in `docs/design/` | `index.md` |

## Owned here

| Rule | Value |
| --- | --- |
| File names | `.md` kebab-case (`core-overview.md`, `viewer-brief.md`); no `.instructions.md` suffix |
| Vendor names | kept as-is: `go-cask-object-browser.design.json`, `go-cask-viewer.html` |
| Frontmatter | four keys — `type`, `title` (`{Title} — go-cask`), one-line `description`, `version` |
| `type` value | `Design` here; `Agent Instructions` for this file (`docs/AGENT.md` §1.2) |
| HTML/JSON artifacts | carry no frontmatter |
| Raw HTML | forbidden in `.md`; HTML only in the dedicated non-Markdown mockup artifact |
| Cross-refs | specs by `docs/specs/` path; siblings relative (`./viewer-brief.md`) |
| Precedence | non-normative — conflict → spec wins (`docs/specs/AGENT.md` §8) |
| Lifecycle | fold a viewer-brief outcome into `docs/specs/viewer-design.md`, then delete the brief |

Markdown frontmatter every file here MUST open with:

```yaml
---
type: Design
title: {Title} — go-cask
description: One sentence stating the document's purpose.
version: v1
---
```
