---
type: Design Document
title: Viewer Design Brief — go-cask
description: Extracted visual and component brief for translating the object-browser mockup into the server-rendered go-cask viewer.
version: v8
---

# Viewer Design Brief — go-cask

Extracts the visual language and component anatomy of
[`go-cask-viewer.html`](go-cask-viewer.html) and
[`go-cask-object-browser.design.json`](go-cask-object-browser.design.json).
Guides the viewer implementation in `internal/web/`. The normative contract is
`docs/specs/viewer-design.md`; where the prototype conflicts with that
contract, the prototype is adapted to server-rendered URL state.

## 1. Product shape

Build a responsive, desktop-first master-detail object browser:

```text
top bar:      CA go-cask                                      action/nav
filter bar:   search | type | size | integrity state | reset
workspace:    object table + pager  |  selected-object inspector
```

Top and filter bars are compact. The list is deliberately dense, hash-first, and scrollable.
The inspector preserves context beside the list on desktop and follows it as a
full-width section on narrow screens.

## 2. Extracted visual grammar

Use the mockup's token values exactly in the one central `internal/web/viewer.css`
file:

- near-white background/surface, dark blue-gray foreground, muted metadata,
  hairline borders, green accent, blue reference accent;
- system UI body face; mono face with tabular numerals for hashes, numbers, and
  hex;
- 46px top bar, 47px filter bar, 14px horizontal page inset, 8px filter gap;
- 30px inputs, 4–5px control radii, compact 27px pager controls;
- 440px inspector, natively resizable, 700px minimum table width;
- dense table rows, sticky uppercase mono headers, right-aligned numeric
  columns, row hover/selection inset, visible two-pixel focus rings;
- responsive transition at 900px: horizontal filter scrolling, list first,
  then inspector, ordinary document scroll.

No inline styles, remote fonts, imports, images, or additional stylesheets; the
CSS never supplies content needed for navigation, state, labels, or
accessibility.

## 3. Component map

| Mockup region | Go template component | Server-driven adaptation |
|---|---|---|
| Brand/top bar | `top-bar` | link/navigation/action uses ordinary links or forms |
| Filter bar | `filter-bar` | a GET form encodes query state |
| Search and clear | `search-control` | clearing is a reset/query link, not client mutation |
| Type/size/state selects | `filter-controls` | values are validated URL parameters |
| Sortable headers | `sort-header` | complete-query links toggle server sort direction |
| Object rows | `object-row` | row link selects a digest via URL |
| Result/pager footer | `pager` | server computes slice/count; pager is links |
| Inspector header | `object-inspector` | selected digest is URL state |
| Metadata/bytes/actions | `inspector-panels` | panel is `tab` URL state; bytes lazy-load through htmx |
| Integrity pill/result | `integrity` | only server-known on-demand result; never fabricated |

`object-list` composes the object table and pager and is one htmx swap target;
`object-inspector` is a distinct target. Components are named templates with
pre-shaped Go data, so fragments and full pages share identical markup.

## 4. Prototype reconciliation

The mockup includes behaviors that cannot be copied literally without custom
JavaScript or unsupported CAS data. Translate them as follows:

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

This preserves the mockup's information density, hierarchy, and visual
language, without shipping a second client application.

## 5. Implementation sequence

1. Add and embed central `viewer.css`; add the stylesheet link to the shared head.
2. Split templates into composition-level components: shell/top bar, filter
   bar, table/sort header/row, pager, inspector/panels, and result fragments.
3. Add validated server-side filter, sort, pagination, selection, and panel
   state; return list and inspector as reusable fragments.
4. Move current object/detail markup onto composed components.
5. Add route/template tests for direct loads, htmx swaps, pagination bounds,
   query preservation, roles/CSRF, and narrow/desktop semantic structure.

## 6. Acceptance cues

- Wide view reads as one calm operational workspace rather than cards.
- A user can scan hash/type/size quickly, page without losing filters, and
  inspect an object without losing the list.
- Every state can be bookmarked or opened directly.
- With htmx absent, forms and links still complete every navigation/action.
- CSS failure leaves a complete, usable semantic document.
