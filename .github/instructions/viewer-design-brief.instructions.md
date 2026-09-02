---
title: Viewer Design Brief — go-cask
description: The design brief for the viewer's next iteration (input for OpenDesign) — pure server-side Go templates + htmx only, no JS, no CSS in step 1; pages, components, interaction map, and a deferred neutral-light palette, strictly aligned to the cas model.
version: v1
---

# Viewer Design Brief — go-cask

> This document is the **design brief** for the next iteration of the viewer
> (`internal/web/`): the input an OpenDesign-style pass works from. It fixes
> the target design grammar (structure-only step 1, no JS, no CSS), the page
> set, the component vocabulary, and the htmx interaction map — **strictly
> aligned to the cas model**, not to bucket/file-store browser models.
>
> It complements, and MUST NOT contradict, the existing viewer specs:
> `.github/instructions/viewer-design.instructions.md` (dashboard-first
> hypermedia UI, templates + htmx, no CSS/JS),
> `.github/instructions/viewer-security.instructions.md` (authn/authz,
> sessions, CSRF, audit), `.github/instructions/api-design.instructions.md`
> (HTTP conventions), `.github/instructions/coding-guidelines.instructions.md`
> (no CSS/JS rules). Where this brief targets a future state (the deferred CSS
> step), that step stays gated on an explicit decision to relax the no-CSS
> rule — this brief changes nothing by itself (AGENT §8).

---

## 1. Model Alignment

The viewer browses a **content-addressed store**, not buckets or files:

- Identity is the **hash** (`algo:hexdigest`, e.g. `sha256:a1b2…`);
- objects are **immutable blobs** in a self-describing envelope
  (`type@major` + payload, cas-core §8 decision 1);
- objects **reference each other by hash**;
- the store exposes `Stats`, `Verify`, `Delete`, and `GC` (cas-core §4.11).

Consequences:

- **No buckets, no uploads** (ingestion is via the library/CLI — objects are
  immutable; nothing to upload from the viewer), **no user settings**.
- Every screen is a drill-down from the store root: overview → object list →
  object detail → references / raw bytes / graph.
- Example-brief pages with no cas analog (buckets overview, upload dialog,
  settings) are deliberately **out of scope**.

---

## 2. Requirements

- **Pure server-side rendering**: Go `html/template`, one page per URL,
  fragments for htmx swaps. No SPA, no client-side state.
- **No JavaScript**: the only script is the vendored htmx runtime; all
  interactivity is hypermedia (`hx-get`/`hx-post`/`hx-target`/`hx-swap`).
- **No CSS in step 1**: clean semantic HTML first (structure, hierarchy,
  tables, `<dl>`, forms); style is a later, gated step.
- **Desktop-first, responsive**: dense but scannable at ≥1280px; degrades
  gracefully narrower.
- **Inspired by GitHub and MinIO Console** in information design —
  restrained, hash-first, no chrome — not in feature set.
- **Readability**: short hashes (8 hex chars) in lists, full hash on detail;
  `monospace` for hashes and hex (inline code styling only, no CSS step 1).
- **Security posture unchanged** (viewer-security): startup-token login,
  session cookie, role checks, CSRF on every mutation, empty-body 401/403,
  audit-logged mutations.

---

## 3. Pages

1. **Login** — token form only; no chrome.
2. **Overview (hub)** — landing page after login: stat cards (objects ·
   bytes · algorithms), algorithm breakdown table, sample objects with quick
   links, search entry. *(The viewer-design-mandated dashboard hub; keep the
   hub model, refine structure.)*
3. **Object list** — searchable, paginated table of all objects (short hash,
   type, size), with an algorithm filter; URL carries `q`/`algo`/`limit`/
   `offset` (URL-as-state).
4. **Object detail** — the centerpiece:
   - **metadata panel** (`<dl>`): full hash, algorithm, size, envelope type
     (`type@major`), reference count;
   - **references**: outgoing links to referenced hashes; incoming when
     resolvable (best-effort via the resolver);
   - **actions**: verify (inline result fragment), delete (admin, CSRF +
     `hx-confirm`);
   - **hexdump**: lazy-loaded fragment (`hx-trigger="revealed"`),
     offset/hex/ASCII table.
5. **Graph** — best-effort reference graph for one object (gitlike-coupled,
   in-process only; documented limitation).
6. **Stats** — storage statistics page (objects, bytes, per-algorithm
   counts).
7. **GC (admin)** — admin action with confirm; result swapped as a status
   fragment. No reachable-root editing UI (GC is an app-root concern,
   consistency §4).

---

## 4. Components

- **Top navigation** — brand, primary links (Overview · Objects · Stats ·
  GC), session/role indicator; sticky at top.
- **Breadcrumbs** — `Overview / Objects / <short hash>` trail on deep
  drill-downs.
- **Table view** — the core component: short hash (linked, monospace), type,
  size; consistent `<caption>`, `th scope="col"`, empty-state row.
- **Search box** — filters the object list by type and hash prefix; submits
  as GET (shareable, refreshable).
- **Pagination** — offset/limit, prev/next; carried in the URL; no client
  cursor state.
- **Metadata panel** — definition list on detail pages; the readable face of
  the envelope.
- **Status badges** — textual tags (no colored pills without CSS): algorithm,
  `type@major`, `verified`/`corrupt` result, `empty store`. Colors arrive
  with the gated CSS step, if it is approved.
- **Fragment targets (htmx)** — fixed ids the current code already swaps:
  `#object-table`, `#hexdump`, `#stats-panel`, `#sample-table`; mutations
  return fragments, never full pages.

---

## 5. htmx Interaction Map (no JS)

- Overview: stats panel refreshes out-of-band (`hx-trigger="load"` + OOB) —
  already present.
- Search: results swap `#object-table`; the URL updates so the state is
  shareable.
- Object detail: hexdump lazy-loads on `revealed`; verify POSTs and swaps an
  inline result; delete is a CSRF POST behind `hx-confirm`.
- GC page: admin POST with `hx-confirm`; result swaps a status fragment.
- Every navigation link is a plain `<a>` — with htmx absent or disabled the
  viewer still works via full navigation (progressive enhancement).

---

## 6. Color Palette (Step 2 — deferred, gated)

- Background `#ffffff`; text dark gray `#1f2328`; secondary gray for
  metadata.
- One blue accent `#0969da` for links, focus, active states.
- Monospace stack for hashes/hexdump.
- **Gate**: current specs forbid CSS in `internal/web` (viewer-design,
  coding-guidelines). Step 2 requires an explicit decision to relax that rule
  (viewer-security is unaffected). Step 1 is structure-only regardless.

---

## 7. Step Plan

1. **Structure-only pass** (this brief; no CSS/JS): normalize each page to
   the component grammar above, reusing existing templates and fragment ids.
2. **CSS step** (after the explicit rule relaxation): neutral light theme
   from §6, desktop-first, badges/tables/panels styled.
3. **Polish**: empty/error states, focus states, responsive behavior.

---

## 8. Checklist

- [ ] Pages in §3 map 1:1 to routes already registered in `internal/web`
      (login, dashboard, objects, object, graph, stats, gc)
- [ ] No JS beyond the vendored htmx runtime; no CSS in step 1
- [ ] Fragment ids from §4 reused; mutations return fragments only
- [ ] Terminology matches AGENT §6 (the viewer, hash, envelope) — no
      bucket/file-store vocabulary
- [ ] Security requirements of viewer-security unchanged and honored
- [ ] CSS step (§6) not started until the no-CSS rule is explicitly relaxed
