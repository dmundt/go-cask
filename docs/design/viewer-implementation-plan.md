---
type: Design Document
title: Viewer Implementation Plan — go-cask
description: Phased implementation plan for the server-rendered master-detail viewer described by the object-browser mockup.
version: v4
---

# Viewer Implementation Plan — go-cask

Implements the master-detail contract in `docs/specs/viewer-design.md` via
[`object-browser-logic.md`](object-browser-logic.md). Copies no mockup custom
JavaScript except the bounded divider enhancement; equivalent server/htmx
behavior where the byte-layer data model supports it.

## 1. Scope

Included:

- One embedded [viewer.css](../../internal/web/viewer.css) asset, no script of the
  viewer's own; htmx is the only script loaded.
- Composed Go templates: top bar, filters, list, table, pager, inspector,
  integrity result, hexdump.
- Validated URL state: filtering, sorting, pagination, selection, inspector
  panels.
- htmx fragments, ordinary links/forms as the progressive fallback.
- Viewer handler and rendering tests.

Excluded:

- Custom JavaScript, browser persistence, runtime CSS generation.
- Mockup-only reference graph/counts, timestamps, clipboard, client history,
  verify-all behavior.
- Changes to `cas` public APIs or backend metadata contracts.

## 2. Phase 1 — assets and template composition

**Files:** `internal/web/web.go`, `internal/web/viewer.css`,
`internal/web/templates/*.html`, `internal/web/web_test.go`.

1. Embed `viewer.css` beside vendored htmx; serve `/viewer/static/viewer.css` as
   `text/css; charset=utf-8`.
2. Link the stylesheet in the shared `head` template.
3. Component tree before page changes: `viewer-page`, `top-bar`, `filter-bar`,
   `search-control`, `filter-controls`, `browser-workspace`, `object-list`,
   `object-table`, `sort-header`, `object-row`, `pager`, `object-inspector`,
   `inspector-header`, `inspector-panels`, `metadata-panel`, `bytes-panel`,
   `actions-panel`, `integrity`, `hexdump-table`.
4. Focused pre-shaped view model per component; never request parameters,
   storage, or computed view state.
5. Compose object-browser and object-detail documents; preserve existing auth,
   CSRF, raw preview, result behavior.
6. CSS tokens/layout from the design JSON: desktop two-column workspace, compact
   bars and controls, table state styles, 900px responsive layout.

**Acceptance:** semantic full documents on direct viewer pages; stylesheet served
locally; no inline styles or custom scripts; each full page a thin component
assembly; geometry matches the mockup's bars/table/pager/inspector hierarchy.

## 3. Phase 2 — browser query model

**Files:** `internal/web/web.go`, `internal/web/templates/objects.html`,
`internal/web/templates/partials.html`, `internal/web/web_test.go`.

1. Unexported parsed-state type for `q`, `type`, `size`, `status`, `sort`, `dir`,
   `limit`, `offset`, `selected`, `tab`.
2. Validate every parameter before listing or loading object state; HTTP 400 on
   malformed values.
3. Normalize rows: digest, short digest, type, size, session integrity; no
   fabricated references or ages.
4. Filter, sort, count, page in the order defined by `object-browser-logic.md`.
5. Query-preserving links: reset, sorting, page-size changes,
   first/previous/next/last paging, selection, panels.

**Acceptance:** direct URLs reconstruct list/inspector state; 25/50/100/250 limits
work; table result count and pager agree; malformed input gets 400.

## 4. Phase 3 — htmx and progressive enhancement

**Files:** `internal/web/web.go`, `internal/web/templates/*.html`,
`internal/web/web_test.go`.

1. `#object-list` = list/pager fragment target.
2. Filter form: `hx-get`, delayed input trigger, `hx-include`, `hx-push-url`;
   normal GET form submission retained.
3. Enhance sort/pager/selection/panel links only where the plain `href` still
   supplies the canonical state URL.
4. `#object-inspector` = independent fragment target; complete document rendering
   retained for cold detail links.
5. Keep hexdump lazy loading and mutation fragments; on-demand integrity truthful
   from server session state.

**Acceptance:** htmx fragment responses contain no page shell; non-htmx navigation
stays complete; browser back/forward follows pushed URLs; every fragment executes
the same component as its full-page composition.

## 5. Phase 4 — test and accessibility closure

**Files:** `internal/web/web_test.go`, optionally focused template test files.

1. Seed deterministic objects spanning types and size buckets.
2. Table-driven tests: defaults, each invalid query value, filter reset, each sort
   direction, every page boundary, empty filtered result, query-preserving pager
   links.
3. Test direct and htmx selection/panel URLs, all named templates, CSS response
   headers, bounded divider-script behavior.
4. Assert semantic captions/scoped headers, labels, active `aria-sort`,
   selected-row state, status text, empty inspector state.
5. Retain role/CSRF/audit/raw-preview coverage; run targeted web tests, full
   `go test ./...`, `scripts/verify.sh` where local CGO permits.

**Acceptance:** every behavior in `object-browser-logic.md` §8 has a named test;
existing viewer security behavior stays green.

## 6. Delivery order

- Land phases in order.
- A phase may be its own signed commit and pull request only if its tests and
  documentation stay internally consistent.
- Do not begin later phases by adding placeholder controls, mocked data, or
  client-side fallback logic.
