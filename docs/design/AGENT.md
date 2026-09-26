---
type: Agent Instructions
title: AGENT — go-cask (docs/design/)
description: This file governs docs/design/ — non-normative design docs (core-overview pointer, viewer-brief, mockups). Follows the conventions in docs/AGENT.md.
version: v8
tags: [go-cask]
status: stable
---

# AGENT — go-cask (docs/design/)

`docs/design/` holds non-normative design artifacts (core-overview pointer, viewer design brief, object-browser design JSON, viewer HTML mockup); instruction specs live in `docs/specs/`, governed by `docs/specs/AGENT.md`; repo-root `AGENTS.md` is entry point. Before any change, read `docs/index.md` first.

## 1. File naming

- `.md` files lowercase kebab-case (`core-overview.md`, `viewer-brief.md`).
- Vendor file names preserved (`go-cask-object-browser.design.json`, `go-cask-viewer.html`).
- No `.instructions.md` suffix (convention for spec folder).

## 2. Frontmatter (required)

Every `.md` file MUST begin with four YAML keys spec folder uses (`type`, `title`, `description`, `version`):

```yaml
---
type: Design
title: {Title} — go-cask
description: One sentence stating the document's purpose.
version: v1
---
```

- `version` = simple marker; bump by one on material change, not cosmetic fixes.
- HTML and JSON files need no frontmatter — display/reference artifacts.
- Markdown files MUST NOT contain raw HTML, HTML comments, tags, layout
  wrappers, or HTML/XML/SVG code fences; keep HTML only in dedicated
  non-Markdown mockup artifacts.

## 3. Cross-referencing

- Instruction specs by `docs/specs/` paths; sibling design docs by relative path (`./viewer-brief.md`); `docs/index.md` for entry.

## 4. Maintenance

- Docs **non-normative** — inform but never override instruction specs; conflict: instruction spec wins (`docs/specs/AGENT.md` §8).
- Viewer-brief outcomes folded back into viewer specs: delete brief (extend existing file rather than add parallel one).
- `docs/design/` entries listed in design docs; no section-10-style inventory needed.

## 5. Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
