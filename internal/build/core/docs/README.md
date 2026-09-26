---
type: Guide
title: docs (build engine) — go-cask
description: Markdown integrity: raw HTML, fences, links, mermaid balance, frontmatter, changelog structure.
version: v2
---

# docs

Markdown integrity rules: what a repository's prose may contain; how it must be structured.

## What it checks

| Rule | Function | Notes |
|---|---|---|
| No raw HTML | `CheckMarkdown` | HTML-vocabulary comment or tag in prose — not inside code |
| No HTML/XML/SVG fences | `CheckMarkdown` | fence tagged `html`, `xml` or `svg` |
| Links resolve | `CheckMarkdown` | local file refs, relative or root-absolute; scheme, host or `#fragment` skipped; images skipped |
| Mermaid is balanced | `CheckMarkdown` | more openers than closers → a block swallows the rest of the page |
| Frontmatter | `Field`, `Frontmatter` | the one `version:` reader and writer → rule and generator cannot disagree |
| Changelog structure | `CheckChangelog` | release heading with no link definition; change group repeated in one release; `[Unreleased]` range starting at an older tag |

`CheckMarkdown` takes a root and one file; caller supplies the file list, reads each file,
prints `Finding` values with `Report` — sorted and deduplicated → two runs log identically.

## Why code is stripped first

Prose rules do not apply to code: a Go generic call such as `New[T](encode, decode)` is
shaped exactly like a Markdown link; a sample tag is not raw HTML in the document.
`stripCode` blanks fenced blocks and inline code spans, keeping line numbers honest.

## Deliberate details

- **`*.md` semantics are the caller's**: the patterns that decide which files are
  documentation live in a repository's own classifier, not here.
- **A tag outside the HTML vocabulary is not reported.** Vocabulary is an explicit list,
  not "anything tag-shaped" — a document legitimately writes markup-shaped text.
- **A malformed percent-escape is left as written**, reported as a missing file.

## Testing

`go test ./docs/` — a table test per rule, false positives (generic call, inline code span,
image, tag outside the vocabulary) pinned beside the true positives, plus a CRLF/LF
agreement check.
