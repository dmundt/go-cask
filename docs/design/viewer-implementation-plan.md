---
type: Design Document
title: Viewer Implementation Plan — go-cask
description: Phased implementation plan for the server-rendered master-detail viewer described by the object-browser mockup.
version: v2
---

# Viewer Implementation Plan — go-cask

This plan implements the master-detail viewer contract in
`docs/specs/viewer-design.md`, using the formal translation in
[`object-browser-logic.md`](object-browser-logic.md). It does not copy the
mockup's custom JavaScript, except the bounded divider enhancement; it delivers
equivalent server/htmx behavior where the byte-layer data model supports it.

## 1. Scope

Included:

- One embedded [viewer.css](../../internal/web/viewer.css) asset and no
  script of the viewer's own — htmx is the only JavaScript the page loads.
- Composed Go templates for the top bar, filters, list, table, pager,
  inspector, integrity result, and hexdump.
- Validated URL state for filtering, sorting, pagination, selection, and
  inspector panels.
- htmx fragments with ordinary links/forms as the progressive fallback.
- Viewer handler and rendering tests.

Excluded:

- Custom JavaScript, browser persistence, and runtime CSS generation.
- Mockup-only reference graph/counts, timestamps,
  clipboard, client history, and verify-all behavior.
- Changes to `cas` public APIs or backend metadata contracts.

## 2. Phase 1 — assets and template composition

**Files:** `internal/web/web.go`, `internal/web/viewer.css`,
`internal/web/templates/*.html`, `internal/web/web_test.go`.

1. Embed `viewer.css` beside vendored htmx and serve it as
   `/viewer/static/viewer.css` with `text/css; charset=utf-8`.
2. Add the stylesheet link in the shared `head` template.
3. Establish the component tree before changing pages: `viewer-page`,
   `top-bar`, `filter-bar`, `search-control`, `filter-controls`,
   `browser-workspace`, `object-list`, `object-table`, `sort-header`,
   `object-row`, `pager`, `object-inspector`, `inspector-header`,
   `inspector-panels`, `metadata-panel`, `bytes-panel`, `actions-panel`,
   `integrity`, and `hexdump-table`.
4. Give every component a focused pre-shaped view model. Components do not
   read request parameters, call storage, or calculate view state.
5. Compose object-browser and object-detail documents from these components.
   Preserve existing auth, CSRF, raw preview, and result behavior.
6. Implement CSS tokens/layout from the design JSON: desktop two-column
   workspace, compact bars and controls, table state styles, and the 900px
   responsive layout.

**Acceptance:** direct viewer pages render semantic full documents; stylesheet
is locally served; no inline styles or custom scripts; each full page is a thin
component assembly; visual geometry matches the mockup's bars/table/pager/
inspector hierarchy.

## 3. Phase 2 — browser query model

**Files:** `internal/web/web.go`, `internal/web/templates/objects.html`,
`internal/web/templates/partials.html`, `internal/web/web_test.go`.

1. Introduce an unexported parsed-state type for `q`, `type`, `size`, `status`,
   `sort`, `dir`, `limit`, `offset`, `selected`, and `tab`.
2. Validate every parameter before listing or loading object state; respond
   with HTTP 400 for malformed values.
3. Normalize list rows with digest, short digest, type, size, and session
   integrity state. Do not add fabricated reference or age values.
4. Filter, sort, count, and page records in the order defined by
   `object-browser-logic.md`.
5. Build complete query-preserving links for reset, sorting, page-size changes,
   first/previous/next/last paging, selection, and panels.

**Acceptance:** direct URLs reconstruct list/inspector state; 25/50/100/250
limits work; table result count and pager agree; malformed input gets 400.

## 4. Phase 3 — htmx and progressive enhancement

**Files:** `internal/web/web.go`, `internal/web/templates/*.html`,
`internal/web/web_test.go`.

1. Make `#object-list` the list/pager fragment target.
2. Use the filter form with `hx-get`, delayed input trigger, `hx-include`, and
   `hx-push-url`, retaining normal GET form submission.
3. Enhance sort/pager/selection/panel links only where their plain `href`
   still supplies the canonical state URL.
4. Make `#object-inspector` an independent fragment target; retain complete
   document rendering for cold detail links.
5. Keep hexdump lazy loading and mutation fragments; render on-demand
   integrity truthfully from server session state.

**Acceptance:** htmx fragment responses contain no page shell; non-htmx
navigation remains complete; browser back/forward follows pushed URLs; every
fragment executes the same component named by its full-page composition.

## 5. Phase 4 — test and accessibility closure

**Files:** `internal/web/web_test.go`, optionally focused template test files.

1. Seed deterministic objects spanning types and size buckets.
2. Add table-driven tests for defaults, each invalid query value, filter reset,
   each sort direction, every page boundary, empty filtered result, and
   query-preserving pager links.
3. Test direct and htmx selection/panel URLs, all named templates, CSS response
   headers, and bounded divider-script behavior.
4. Assert semantic captions/scoped headers, labels, active `aria-sort`,
   selected-row state, status text, and empty inspector state.
5. Retain existing role/CSRF/audit/raw-preview coverage; run targeted web
   tests, full `go test ./...`, and `scripts/verify.sh` where local CGO permits.

**Acceptance:** every behavior in `object-browser-logic.md` §8 has a named
test; all existing viewer security behavior remains green.

## 6. Delivery order

Land phases in order. Each phase may be its own signed commit and pull request
only if its tests and documentation remain internally consistent. Do not begin
later phases by adding placeholder controls, mocked data, or client-side
fallback logic.
