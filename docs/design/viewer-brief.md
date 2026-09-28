---
type: Design Document
title: Viewer Design Brief — go-cask
description: Extracted visual and component brief for translating the object-browser mockup into the server-rendered go-cask viewer.
version: v9
---

# Viewer Design Brief — go-cask

Extracts visual language and component anatomy of
[`go-cask-viewer.html`](go-cask-viewer.html) and
[`go-cask-object-browser.design.json`](go-cask-object-browser.design.json); guides the viewer
implementation in `internal/web/`. Normative contract: `docs/specs/viewer-design.md` — where
the prototype conflicts, it is adapted to server-rendered URL state.

## 1. Product shape

- Responsive, desktop-first master-detail object browser.
- Compact top and filter bars; deliberately dense, hash-first, scrollable list.
- Inspector beside the list on desktop; full-width section after the list on narrow screens.

```text
top bar:      CA go-cask                                      action/nav
filter bar:   search | type | size | integrity state | reset
workspace:    object table + pager  |  selected-object inspector
```

## 2. Extracted visual grammar

Mockup token values, used as-is in the one central `internal/web/viewer.css`:

- **Surfaces:** near-white background/surface, dark blue-gray foreground, muted metadata,
  hairline borders, green accent, blue reference accent.
- **Type:** system UI body face; mono face with tabular numerals for hashes, numbers, hex.
- **Bars:** 46px top bar, 47px filter bar, 14px horizontal page inset, 8px filter gap.
- **Controls:** 30px inputs, 4–5px radii, compact 27px pager controls.
- **Inspector:** 440px, natively resizable; 700px minimum table width.
- **Table:** dense rows, sticky uppercase mono headers, right-aligned numeric columns,
  row hover/selection inset, visible two-pixel focus rings.
- **Responsive:** transition at 900px — horizontal filter scrolling, list first, then
  inspector, ordinary document scroll.

No inline styles, remote fonts, imports, images, or additional stylesheets. The CSS supplies
no content needed for navigation, state, labels, or accessibility.

## 3. Component map

Mockup regions → named Go templates with pre-shaped data, so fragments and full pages share
identical markup. Components: `top-bar`, `filter-bar`, `search-control`, `filter-controls`,
`sort-header`, `object-row`, `pager`, `object-inspector`, `inspector-panels`, `integrity`.

- `object-list` composes table and pager — one htmx swap target.
- `object-inspector` — a distinct target; selection is URL state.
- Component responsibilities and swap boundaries: `docs/specs/viewer-design.md` §4.

## 4. Prototype reconciliation

Mockup behaviors not copyable literally without custom JavaScript or unsupported CAS data:

| Mockup behavior | Viewer implementation |
|---|---|
| Live search | htmx GET plus normal GET-form fallback |
| Sort, filters, page size, pager | validated query parameters and server render |
| Row selection | `selected` query parameter; htmx inspector swap |
| Metadata/References/Bytes tabs | `tab` query parameter; links/buttons, no client tab state |
| Resizable inspector | CSS `resize` bounded by `min-width`/`max-width`; width resets on reload |
| Copy digest | full digest in a readonly selectable field; no clipboard API |
| Reference panels/refs count | omitted; byte layer cannot resolve typed references |
| Written time | omitted until backend exposes truthful metadata |
| Status state | `not verified` or result of an on-demand server verification |
| Global Verify | not shown until an authorized bounded server operation exists |

Keeps the mockup's density, hierarchy, and visual language; no second client application.

## 5. Implementation sequence

1. Add and embed central `viewer.css`; link it from the shared head.
2. Split templates into components: shell/top bar, filter bar, table/sort header/row, pager,
   inspector/panels, result fragments.
3. Add validated server-side filter, sort, pagination, selection, panel state; return list and
   inspector as reusable fragments.
4. Move current object/detail markup onto composed components.
5. Add route/template tests: direct loads, htmx swaps, pagination bounds, query preservation,
   roles/CSRF, narrow/desktop semantic structure.

## 6. Acceptance cues

- Wide view reads as one calm operational workspace, not cards.
- Scan hash/type/size quickly; page without losing filters; inspect without losing the list.
- Every state bookmarkable or directly openable.
- With htmx absent, forms and links still complete every navigation/action.
- CSS failure leaves a complete, usable semantic document.

Spec-owned detail (`docs/specs/viewer-design.md`): component swap targets §4; URL state,
validation, `nav` trail, panel links §5; semantics, `aria-sort`, focus rings §6; test
coverage §7; checklist §8.
