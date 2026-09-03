---
title: Viewer Design Brief — go-cask
description: The design brief for the viewer's next iteration (input for OpenDesign) — pure server-side Go templates + htmx only, no JS, no CSS in step 1; a master-detail object browser (list + inspector) with URL-as-state, informed by the cas-kit viewer prototype and the byte-layer viewer design, strictly aligned to the cas model.
version: v3
---

# Viewer Design Brief — go-cask

> This document is the **design brief** for the next iteration of the viewer
> (`internal/web/`): the input an OpenDesign-style pass works from. It fixes
> the target design grammar (structure-only step 1, no JS, no CSS), the view
> set, the component vocabulary, and the htmx interaction map — informed by
> the **cas-kit viewer prototype** (`C:\Users\mundtdan\Downloads\viewer`:
> master-detail layout, URL-as-state query, sortable result fragments, a
> tabbed inspector, honest empty/error states) and **strictly aligned to the
> cas model and the byte-layer viewer design** (viewer-design v4) — not to
> bucket/file-store browser models.
>
> It complements, and MUST NOT contradict, the existing viewer specs:
> `.github/instructions/viewer-design.instructions.md` (dashboard-first
> hypermedia UI, templates + htmx, no CSS/JS, **byte-layer**: objects, bytes,
> integrity — never typed reference graphs),
> `.github/instructions/viewer-security.instructions.md` (authn/authz,
> sessions, CSRF, audit), `.github/instructions/api-design.instructions.md`
> (HTTP conventions), `.github/instructions/coding-guidelines.instructions.md`
> (no CSS/JS rules). Where this brief targets a future state (the deferred CSS
> step), that step stays gated on an explicit decision to relax the no-CSS
> rule — this brief changes nothing by itself (AGENT §8).

---

## 1. Model Alignment

The viewer browses a **content-addressable store**, not buckets or files:

- Identity is the **hash** (`algo:hexdigest`, e.g. `sha256:a1b2…`);
- objects are **immutable blobs** in a self-describing envelope
  (`type@major` + payload, cas-core §8 decision 1); the type is **sniffed
  from the bytes** for display only;
- the store exposes `Stats`, `Verify`, `Delete`, and `GC` (cas-core §4.11);
  object age comes from the file mtime.

Consequences:

- **The viewer is a byte-layer tool** (viewer-design v4): it shows objects,
  bytes, and integrity — it never renders typed reference graphs. Resolving
  references is the app layer's job (e.g. `gitlike`); the viewer does not
  import it.
- **No buckets, no uploads** (ingestion is via the library/CLI — objects are
  immutable), **no user settings**.
- Every screen is a drill-down from the store root: overview → object index →
  object detail (metadata / bytes / integrity).
- The prototype's *demo-only* data has no cas analog and is **out of scope**:
  blake3 digests and CID notation, media-type/path hints, tiers and
  compression (packfiles/compression are deferred possible extensions,
  extensions §3), pinned flags, refcounts/read stats, chunk maps (objects
  are stored whole), and reference graphs. The viewer never invents state
  the store does not maintain: integrity is shown only as **on-demand
  `Verify` results** (verified / corrupt / not-yet-verified), never as a
  persisted index column.

---

## 2. Requirements

- **Pure server-side rendering**: Go `html/template`, one page per URL,
  fragments for htmx swaps. No SPA, no client-side state.
- **No JavaScript**: the only script is the vendored htmx runtime; all
  interactivity is hypermedia (`hx-get`/`hx-post`/`hx-target`/`hx-swap`/
  `hx-push-url`/`hx-include`).
- **No CSS in step 1**: clean semantic HTML first (structure, hierarchy,
  tables, `<dl>`, forms); style is a later, gated step.
- **Desktop-first, responsive**: a dense master-detail workspace at ≥1280px;
  the side panel collapses below it.
- **Inspired by the cas-kit prototype, GitHub, and MinIO Console** in
  information design — restrained, hash-first, no chrome — not in feature
  set.
- **Readability**: short hashes in lists (with algorithm prefix, e.g.
  `sha256:9f86d0…`), full hash in the inspector; `monospace` for hashes,
  numbers, and hex; tabular numerals.
- **Every view is a URL**: filters, sort, page, and selection are query
  parameters; any state is reconstructible and bookmarkable; a swapped
  fragment updates the URL via `HX-Push-Url` so back/forward works.
- **Security posture unchanged** (viewer-security): startup-token login,
  session cookie, role checks, CSRF on every mutation, empty-body 401/403,
  audit-logged mutations.

---

## 3. Views

1. **Login** — token form only; no chrome.
2. **Overview (hub)** — landing page: top-bar chips (objects · bytes ·
   algorithms), algorithm breakdown table, sample objects, search entry that
   jumps into the index. *(The viewer-design-mandated dashboard hub; adopt
   the prototype's chip/top-bar language here and everywhere.)*
3. **Object index (master)** — the center of the app:
   - **filter bar**: search (digest-prefix or type text), type filter
     (sniffed envelope type), size buckets, rows-per-page (25/50/100/250),
     reset-filters link when any filter is active;
   - **results table**: hash (short, algorithm-prefixed), type (`type@major`
     from the envelope), size, age; **sortable columns** (size, age) with
     `aria-sort`; numeric cells right-aligned, monospace;
   - **pager**: `X–Y of N · <bytes>` with page-window elision and
     prev/next — swapped together with the table so counts, sort arrows, and
     rows can never disagree;
   - **row click** loads the inspector fragment; rows degrade to full
     navigation without htmx.
4. **Object detail (inspector)** — server-rendered side panel (or detail
   page on narrow widths) swapped as one unit:
   - **Metadata tab**: full hash, algorithm, size, envelope type
     (`type@major`, sniffed), age; link to the raw bytes route
     (`/objects/{hash}/raw`);
   - **Bytes tab**: hexdump of the first bytes, offset/hex/ASCII — lazy via a
     `revealed` or explicit "load" fragment;
   - **actions**: verify (POST; swaps only the integrity fragment — states:
     verified ✓ / corrupt ✕ with a concrete message / not-yet-verified),
     delete (admin, CSRF + `hx-confirm`);
   - the same URL (`/objects/{hash}`) renders a **full document** on a cold
     load and a **fragment** for htmx (`HX-Request`) — a shared URL never
     shows a bare panel.
5. **Stats** — storage statistics page (objects, bytes, per-algorithm
   counts).
6. **GC (admin)** — admin action with confirm; result swapped as a status
   fragment. No reachable-root editing UI (GC is an app-root concern,
   consistency §4).

**Explicitly out of scope** (no cas analog): buckets overview, upload
dialog, settings, user management, typed reference graphs (byte-layer
decision, viewer-design v4), and the prototype's demo-only columns (§1).

---

## 4. Components

- **Top bar** — brand mark + breadcrumb (`cas-kit / store / Objects`),
  status chips (objects, bytes, algorithms; a "corrupt N" chip appears only
  after a verify found corruption in this session), primary actions; sticky.
- **Filter bar** — owns the durable view state; every fragment request pulls
  it in via `hx-include`; sort lives in a hidden field so headers and filters
  never produce an ambiguous pair.
- **Search box** — detects a hex digest prefix (prefix match, matching bytes
  wrapped in `<mark>` — built, never interpolated) vs. free text (type
  match).
- **Results table** — the core component: sticky header, sortable columns,
  empty-state row; numeric cells right-aligned.
- **Pager** — offset/limit with page-window elision and per-page selector;
  carried in the URL; no client cursor state.
- **Inspector** — the detail panel with radio-driven tabs (see §6 for the
  step-2 technique), `<dl>` metadata, hexdump `<pre>`, integrity fragment.
- **Status tags** — textual tags (no colored pills without CSS): algorithm,
  `type@major`, `verified` / `corrupt` / `unverified`, `empty store`.
  Colors arrive with the gated CSS step.
- **Panel states** — distinct empty copy per state (no match for query vs.
  no objects at all), and an error panel with the error text, a trace id,
  and a retry action — full-page vs. fragment variants.

---

## 5. htmx Interaction Map (no JS)

- Overview: stats panel refreshes out-of-band (`hx-trigger="load"` + OOB).
- Index: filter/search/sort/page all `hx-get` the **results fragment** with
  `hx-include="#filters"` and `HX-Push-Url`; the table, counts, and pager
  swap as one unit.
- Row → inspector: `hx-get /objects/{hash}` targeting `#inspector`.
- Verify: `hx-post` swaps only `#integrity` (pending → resolved, with the
  htmx indicator as the "recomputing" state).
- Bytes: hexdump loads on `revealed` (or an explicit load button for large
  objects).
- Delete / GC: CSRF POST behind `hx-confirm`.
- Every navigation link is a plain `<a>` — with htmx absent or disabled the
  viewer still works via full navigation (progressive enhancement); a cold
  load of any fragment URL returns the full document.

---

## 6. Visual Language (Step 2 — deferred, gated)

Adopt the prototype's token structure (light, neutral, tech-utility
posture):

- `--bg`/`--surface` white-ish neutral, `--fg` dark gray, `--muted`
  secondary gray, `--border` hairline; one **accent** hue — default blue
  (`#0969da`) per the earlier palette decision; the prototype's green
  (`oklch(58% 0.16 145)`) is an equally valid single-accent alternative —
  pick one at the CSS step.
- Status colors (ok / warn / danger / info) for verify results and tags;
  row hover + selected tint; sticky table headers with hairline rules.
- Fonts: system UI body, monospace stack (`JetBrains Mono` / `IBM Plex Mono`
  / `ui-monospace`) for hashes, numbers, hex.
- Layout: 46px top bar + filter bar; results table flexes; the inspector is
  a fixed right column (~370px) that **hides below ~1240px** (the detail
  route then renders as a full page).
- Technique notes: the prototype switches inspector tabs with **hidden radio
  inputs + CSS sibling selectors** (zero round trips, zero JS) and drives
  "pressed" filter pills with `:has(input:checked)` — both are pure CSS and
  only land in this step.
- **Gate**: current specs forbid CSS in `internal/web` (viewer-design,
  coding-guidelines). Step 2 requires an explicit decision to relax that rule
  (viewer-security is unaffected). Step 1 is structure-only regardless.

---

## 7. Step Plan

1. **Structure-only pass** (this brief; no CSS/JS): normalize each view to
   the component grammar above, reusing existing templates and fragment ids
   (`#object-table`, `#inspector`, `#integrity`, `#hexdump`, `#stats-panel`);
   make every view URL-as-state with `HX-Push-Url`; add the empty/error
   states and the reset-filters affordance.
2. **CSS step** (after the explicit rule relaxation): token set from §6,
   master-detail layout with responsive collapse, radio-tab panes, sticky
   headers, tags/pills, focus states.
3. **Polish**: selected-row affordance, reduced-motion handling, keyboard
   focus order.
4. **Fold-in & retire**: as step 1/2 outcomes are implemented, merge the
   accepted results back into `.github/instructions/viewer-design.
   instructions.md` (version bump there) and **delete this brief** — it is a
   proposal for a planned iteration, not a permanent spec (AGENT §1: prefer
   extending an existing file over keeping a parallel one).

---

## 8. Checklist

- [ ] Views in §3 map 1:1 to routes registered in `internal/web` (login,
      dashboard, objects, object, stats, gc)
- [ ] No JS beyond the vendored htmx runtime; no CSS in step 1
- [ ] Every state is a URL; fragments push the URL and degrade to full pages
- [ ] Fragment ids from §4 reused; results swap as one unit; mutations return
      fragments only
- [ ] Byte-layer: no typed reference graphs, no gitlike import in the viewer
      (viewer-design v4)
- [ ] No demo-only store state invented (no tiers/compression/pinned/
      refcounts/CID/chunk maps — §1)
- [ ] Terminology matches AGENT §6 (the viewer, hash, envelope) — no
      bucket/file-store vocabulary
- [ ] Security requirements of viewer-security unchanged and honored
- [ ] CSS step (§6) not started until the no-CSS rule is explicitly relaxed
