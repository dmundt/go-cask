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

- Every Go block MUST be a complete unit: it declares its own `package` clause
  and its own imports, and it compiles and vets on its own. There are no
  compiled fragments and no generated context stubs. The `website examples`
  step in `scripts/verify.sh` extracts every `go` fence under `website/`,
  writes each one into its own package directory inside the module, and runs
  `go build` and `go vet` over the whole set, so a non-compiling or
  fragment-only block fails the gate instead of drifting silently.
- Document a contract without pasting a fragment by declaring it in a complete
  unit and asserting that it and the shipped type accept exactly each other's
  implementations — `var _ cas.Hasher = Hasher(nil)` together with
  `var _ Hasher = cas.Hasher(nil)`. The two assignments compile only while the
  method sets agree, so the page cannot drift from `cas`.
- The inventory tables in `concepts/backends.md`, `concepts/hashes.md`,
  `concepts/codecs.md`, and `specifications/object-format.md` MUST name every
  Go package directory under `cas/backend`, `cas/hash`, and `cas/codec`. The
  same gate step checks those tables against the tree, so adding or removing a
  packaged backend, hasher, or codec fails the gate until the matching table
  names it.
- Every Go block MUST match the current public API; the gate's build and vet
  are the check.
- Use fenced code blocks only for code, shell commands, and wire formats.
- Use Markdown links to internal pages. Do not use raw HTML links or HTML
  layout wrappers in Markdown pages.
- Raw HTML is forbidden in every Markdown file: no tags, comments, layout
  wrappers, or HTML/XML/SVG code fences.
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

## Build provenance in the footer

The footer is one line, and `website/macros.py` is the only thing that completes
it: the hook reads the checked-out revision with one call — `git log -1
--format=%h %cs` — and writes the result back to `env.conf["copyright"]`, the
value the theme's own footer partial renders, so the site needs no theme
override and no provenance environment variable. The line shows one provenance
value: that revision's own date as `YYYY-MM-DD`, labelled `Updated` and linked
to the commit — only the date is inside the link, so the label never becomes part
of the URL's text — and the visible text answers "how current are these docs"
while the revision stays in the link target. The date is revision-derived and
never wall-clock, so two builds of one revision render the same footer, and it
carries no zone label because at day granularity no zone is more correct than the
offset the commit records. The `©` year comes from that same date instead of a
literal. The line degrades rather than guesses: with no readable revision the
footer renders the `copyright` value `mkdocs.yml` declares — no fragment and no
year — and the build still succeeds. `website/privacy.md`'s final line is the
privacy policy's own revision date, not the site's; keep that wording so the two
cannot be read as one.

`python3 website/macros.py --selftest` pins the rendered line, the `©` year, the
`Updated` label outside the link, the omitted fragment and the fact that the
revision is not visible text, for fixed inputs, and `scripts/verify.sh` runs it in
the gate; `go test ./internal/website` checks the same text and the shipped
artifacts (the config line, the deleted override, the absent plumbing) without
MkDocs, and re-runs the module self-test when a Python interpreter is available.

## Signed pull-request workflow

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
