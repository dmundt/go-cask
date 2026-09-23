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

The footer partial (`website/overrides/partials/copyright.html`, loaded through
`theme.custom_dir` in `mkdocs.yml`) shows the date and short revision of the
revision the site was built from, and derives the copyright year from that same
date instead of keeping a literal. `website/macros.py` publishes both values.

- `SITE_BUILD_DATE` — the deployed revision's committer date, ISO 8601. The
  hook normalizes it to UTC and renders `YYYY-MM-DD`. The deploy workflow
  passes `github.event.head_commit.timestamp` on push and falls back to
  `git log -1 --format=%cI` when a workflow dispatch has no commit payload.
- `SITE_REVISION` — the deployed revision, short form, passed from
  `github.event.head_commit.id` with `github.sha` as the fallback.

Both start as empty strings in `mkdocs.yml`'s `extra` block; the hook overwrites
them before any page or template renders. They are sourced from the revision,
never from wall-clock time, so two builds of one revision produce the same
footer. Check that after a change to the footer or the hook: the built
`site/index.html` must contain one `md-copyright__site-build` line naming the
value of `SITE_REVISION` it was given, and repeating the build with the same
values must reproduce that line byte for byte.

Unlike `IMPRESSUM`, neither is required: a local build without the environment
variables falls back to the checkout's own `git log` and `git rev-parse`, and
when even that is unavailable the value stays empty. The footer then omits the
build line, or drops the year, rather than rendering an empty or stale label.
`mkdocs build --strict` and `mkdocs serve` must both keep succeeding with no new
environment variable set. The build-provenance markup lives in the HTML partial
only: raw HTML stays forbidden in every `*.md` file, and the doc-integrity pass
in `scripts/verify.sh` enforces that.

`website/privacy.md`'s final line is the privacy policy's own revision date
("Privacy policy revision: …"), not the site's build date; keep that wording so
the two dates cannot be read as one.

## Signed pull-request workflow

When repository policy requires signed commits, rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, and push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
