# Website Authoring Guide

This directory contains the published go-cask documentation site. It is a
developer-facing companion to the authoritative repository documentation under
`docs/specs/`; it does not replace those specifications.

## Content

- Write for Go developers evaluating or adopting go-cask.
- State only behavior implemented in the repository. Link to source or a
  specification when a contract needs detail.
- Keep the tone direct and technical. Explain constraints, non-goals, and
  operational trade-offs alongside benefits.
- Use `go-cask` for the project, `cas` for the core package, `Digest` for the
  content address, and `Hasher` for its algorithm provider.
- Use **content-addressable store** for go-cask and concrete implementations;
  use **content-addressable storage** for the general technique or concept.
- Distinguish the generic `cas` core from the `gitlike` reference package.
- Keep one concept per page. Prefer headings, prose, tables, lists, and
  runnable examples over landing-page components or promotional copy.

## Examples and links

- Every Go example MUST match the current public API and compile when presented
  as a complete program.
- Use fenced code blocks only for code, shell commands, and wire formats.
- Use Markdown links to internal pages. Do not use raw HTML links or HTML
  layout wrappers in Markdown pages.
- Use Mermaid only when it adds information unavailable in prose or a table.
  Keep diagrams small, directional, and free of decorative styling.

## Visual direction

- Keep the site document-first: strong typography, whitespace, thin dividers,
  and a neutral palette with restrained accent color.
- Do not add shadows, decorative icons, pill badges, gradients, or card-grid
  layouts unless content comparison genuinely requires a table or panel.
- Prefer sharp or near-sharp edges and avoid nested visual frames.
- Code blocks MUST wrap long lines instead of exposing horizontal scrollbars.
- Keep the top bar visible at every viewport width. On narrow screens, retain
  the left hamburger, compact search control, and a right-aligned GitHub icon.
- Keep search fields on the white page surface with the same thin gray border
  used by tables and section dividers.

## Validation

Run the site build after website changes:

```text
python -m mkdocs build --strict
```

Preview through `python -m mkdocs serve`; browser `file://` pages cannot load
MkDocs' search index, so local search is not a valid file-preview check.
