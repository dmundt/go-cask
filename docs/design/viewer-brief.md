---
type: Design Document
title: Viewer Design Brief — go-cask
description: The design brief for the viewer's next iteration (input for OpenDesign) — pure server-side Go templates + htmx only, no JS, no CSS in step 1; a master-detail object browser (list + inspector) with URL-as-state, informed by the cas-kit viewer prototype and the byte-layer viewer design, strictly aligned to the cas model.
version: v5
---

# Viewer Design Brief — go-cask

Design brief for the next viewer (`internal/web/`) iteration — the input an OpenDesign pass works from. Fixes the target grammar (structure-only step 1, no JS/CSS), view set, component vocabulary, and htmx interaction map — informed by the **cas-kit viewer prototype** (master-detail, URL-as-state query, sortable result fragments, tabbed inspector, honest empty/error states) and **strictly aligned to the cas model and byte-layer viewer design** (viewer-design), not bucket/file-store models. Complements and MUST NOT contradict viewer-design, viewer-security, api-design, coding-guidelines. A deferred CSS step stays gated on an explicit decision to relax the no-CSS rule — this brief changes nothing by itself.

## 1. Model alignment

The viewer browses a **content-addressable store**, not buckets/files:
- Identity is the **digest** (raw digest bytes, rendered as lowercase hex; the printable `sha256:hexdigest` form is a client rendering — the core names no algorithm); objects are **immutable blobs** in a self-describing envelope (`type@major` + payload); the type is **sniffed from the bytes** for display only.
- The store exposes `Stats`, `Verify`, `Delete`, `GC`; object age = file mtime.

Consequences: the viewer is a **byte-layer tool** (shows objects, bytes, integrity; never typed reference graphs — resolution is the app layer's job, e.g. `gitlike`, which the viewer does not import). **No buckets, no uploads** (ingestion via library/CLI; objects immutable), **no user settings**. Every screen drills down from the store root: overview → object index → object detail (metadata/bytes/integrity). Out of scope (no cas analog / demo-only): blake3/CID notation, media-type/path hints, tiers/compression (deferred extensions), pinned flags, refcounts/read stats, chunk maps (objects stored whole), reference graphs. The viewer never invents store state: integrity shown only as **on-demand `Verify` results** (verified / corrupt / not-yet-verified), never a persisted index column.

## 2. Requirements

- Pure server-side rendering: `html/template`, one page per URL, fragments for htmx. No SPA, no client state.
- No JavaScript: only script is the vendored htmx runtime; all interactivity hypermedia (`hx-get`/`hx-post`/`hx-target`/`hx-swap`/`hx-push-url`/`hx-include`).
- No CSS in step 1: clean semantic HTML first; style is a later gated step.
- Desktop-first, responsive: dense master-detail at ≥1280px; side panel collapses below.
- Information design inspired by the cas-kit prototype, GitHub, MinIO Console — restrained, hash-first, no chrome (not feature set).
- Readability: short digests in lists (8 hex chars, e.g. `9f86d081`), the full hex digest in the inspector; monospace + tabular numerals for hashes/numbers/hex.
- Every view is a URL: filters, sort, page, selection as query params; any state reconstructible/bookmarkable; a swapped fragment updates the URL via `HX-Push-Url` so back/forward works.
- Security unchanged (viewer-security): startup-token login, session cookie, roles, CSRF on every mutation, empty-body 401/403, audit-logged mutations.

## 3. Views

1. **Login** — token form only; no chrome.
2. **Overview (hub)** — top-bar chips (objects · bytes), sample objects, search that jumps into the index.
3. **Object index (master)** — center of the app: **filter bar** (search digest-prefix or type text; type filter [sniffed]; size buckets; rows/page 25/50/100/250; reset-filters when active); **results table** (short hash, `type@major`, size, age; sortable size/age columns with `aria-sort`; numeric cells right-aligned monospace); **pager** (`X–Y of N · <bytes>`, page-window elision, prev/next — swapped with the table so counts/sort/rows never disagree); **row click** loads the inspector fragment; rows degrade to full navigation without htmx.
4. **Object detail (inspector)** — server-rendered side panel (or full page on narrow widths), swapped as one unit: **Metadata tab** (full hex digest, algorithm — the client's constant `sha256`, size, envelope type [sniffed], age; link to `/objects/{hash}/raw`); **Bytes tab** (hexdump of first bytes, offset/hex/ASCII — lazy via `revealed` or explicit "load"); **actions** — verify (POST, swaps only the integrity fragment: verified ✓ / corrupt ✕ with message / not-yet-verified), delete (admin, CSRF + `hx-confirm`). The same URL (`/objects/{hash}`) renders a full document on cold load and a fragment for htmx (`HX-Request`) — a shared URL never shows a bare panel.
5. **Stats** — objects, bytes.
6. **GC (admin)** — confirm + status fragment; no reachable-root editing UI (an app-root concern).

Explicitly out of scope: buckets overview, upload dialog, settings, user management, typed reference graphs (byte-layer), the prototype's demo-only columns.

## 4. Components

- **Top bar** — brand + breadcrumb (`cas-kit / store / Objects`), status chips (objects, bytes; "corrupt N" chip appears only after a session verify found corruption), primary actions; sticky.
- **Filter bar** — owns durable view state; every fragment request pulls it via `hx-include`; sort in a hidden field (never an ambiguous headers/filters pair).
- **Search box** — detects hex digest prefix (prefix match, `<mark>`-wrapped — built, never interpolated) vs. free text (type match).
- **Results table** — sticky header, sortable columns, empty-state row; numeric cells right-aligned.
- **Pager** — offset/limit, page-window elision, per-page selector; carried in the URL; no client cursor state.
- **Inspector** — radio-driven tabs, `<dl>` metadata, hexdump `<pre>`, integrity fragment.
- **Status tags** — textual tags (no colored pills without CSS): algorithm, `type@major`, verified/corrupt/unverified, `empty store`; colors arrive with the gated CSS step.
- **Panel states** — distinct empty copy per state (no match for query vs. no objects at all); error panel with error text, trace id, retry — full-page vs. fragment variants.

## 5. htmx interaction map (no JS)

- Overview: stats panel refreshes OOB (`load` + OOB).
- Index: filter/search/sort/page all `hx-get` the results fragment with `hx-include="#filters"` + `HX-Push-Url`; table, counts, pager swap as one unit.
- Row → inspector: `hx-get /objects/{hash}` → `#inspector`.
- Verify: `hx-post` swaps only `#integrity` (pending → resolved; htmx indicator = recomputing).
- Bytes: hexdump on `revealed` (or explicit load for large objects).
- Delete/GC: CSRF POST behind `hx-confirm`.
- Every nav link is a plain `<a>`; without htmx the viewer works via full navigation; a cold load of any fragment URL returns the full document.

## 6. Visual language (step 2 — deferred, gated)

- Tokens: `--bg`/`--surface` white-ish neutral, `--fg` dark gray, `--muted`, `--border` hairline; one **accent** hue (default blue `#0969da`, or green `oklch(58% 0.16 145)` — pick one at the CSS step). Status colors (ok/warn/danger/info); row hover + selected tint; sticky headers with hairline rules.
- Fonts: system UI body; monospace (`JetBrains Mono`/`IBM Plex Mono`/`ui-monospace`) for hashes/numbers/hex.
- Layout: 46px top bar + filter bar; results table flexes; inspector a fixed right column (~370px) hiding below ~1240px (detail route renders full page there).
- Techniques: inspector tabs via **hidden radio inputs + CSS sibling selectors** (zero round trips/JS); "pressed" filter pills via `:has(input:checked)` — both pure CSS, land only in this step.
- **Gate:** current specs forbid CSS in `internal/web` (viewer-design, coding-guidelines). Step 2 requires an explicit decision to relax that rule (viewer-security unaffected). Step 1 is structure-only regardless.

## 7. Step plan

1. **Structure-only pass** (this brief; no CSS/JS): normalize each view to the component grammar, reusing existing templates/fragment ids (`#object-table`, `#inspector`, `#integrity`, `#hexdump`, `#stats-panel`); make every view URL-as-state with `HX-Push-Url`; add empty/error states and reset-filters.
2. **CSS step** (after explicit rule relaxation): token set, master-detail layout with responsive collapse, radio-tab panes, sticky headers, tags/pills, focus states.
3. **Polish**: selected-row affordance, reduced-motion, keyboard focus order.
4. **Fold-in & retire**: as outcomes are implemented, merge accepted results into `viewer-design.md` (version bump) and **delete this brief** — it is a proposal for a planned iteration, not a permanent spec.

## 8. Checklist

- [ ] §3 views map 1:1 to routes registered in `internal/web` (login, dashboard, objects, object, stats, gc)
- [ ] No JS beyond the vendored htmx runtime; no CSS in step 1
- [ ] Every state is a URL; fragments push the URL and degrade to full pages
- [ ] Fragment ids from §4 reused; results swap as one unit; mutations return fragments only
- [ ] Byte-layer: no typed reference graphs, no `gitlike` import in the viewer
- [ ] No demo-only store state invented (no tiers/compression/pinned/refcounts/CID/chunk maps)
- [ ] Terminology matches AGENT §6 (the viewer, hash, envelope) — no bucket/file-store vocabulary
- [ ] viewer-security requirements unchanged and honored
- [ ] CSS step (§6) not started until the no-CSS rule is explicitly relaxed
