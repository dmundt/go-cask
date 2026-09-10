---
type: Specification
title: Viewer Design — go-cask
description: Design of the embedded technical viewer — simple, elegant, and usable; dashboard-first hypermedia UI with nested Go templates + htmx only (no JS/CSS), exposing the object store at a low technical level (objects, blobs, stats). The viewer is a byte-layer tool: it shows objects, bytes, and integrity, never typed reference graphs.
version: v14
---

# Viewer Design — go-cask

The embedded technical browser UI in `internal/web/`, for developers/admins browsing the CAS. Defines **how** (hypermedia, nested Go templates + htmx only, raw HTML) and **what** (dashboard hub → objects/blobs/stats at a technical level). MUST be **simple, elegant, usable** — elegance from clean semantic structure/layout/hierarchy, not CSS. Read with `viewer-security.md` (all requirements apply unchanged), `coding-guidelines.md` (§4 no CSS/JS, §5 templates+htmx, §6 raw HTML, §10 viewer boundary), `cas-core.md` (data model: `Digest`, `Object[T]`, `Backend.Stats`, `Verify`, `GC`). Design reference: hypermedia.systems. `docs/design/viewer-brief.md` is non-normative next-iteration input; changes nothing here until folded back.

## 1. Purpose & persona

- Persona: developer/operator answering "what is stored? how much space? what does this point to? is it intact?".
- Hub: a **dashboard** (landing) with storage stats, an addressing note, a sample of objects, and search — one click to every detail.
- Drill-down: dashboard → object list → object detail → raw blob/hexdump.
- Aesthetic: simple, elegant, usable — dense but scannable, plain semantic HTML, no decoration.
- Out of scope: object editing (objects are immutable), JSON/data APIs, client-side state, charting.

## 2. Design principles

Hypermedia-driven: (1) **HTML is the application** — every transition is an HTTP request (GET nav, POST mutation) returning HTML (full page or fragment); (2) server renders all HTML, client has no app logic; (3) **htmx is the only extension**, expressed purely via htmx attributes; (4) **progressive enhancement** — works with htmx disabled (real links/forms); (5) zero hand-written JS/CSS; the htmx script is the single vendored exception.

Simple/elegant/usable: (6) **dashboard-first** landing (numbers that matter, sample, search; every screen answers "what am I looking at, where next?"); (7) **information hierarchy** overview → list → detail → raw; detail pages start with a summary block then go deeper; (8) **no dead ends** — every hash is a link, every panel has a "see all", always back to the dashboard; (9) **elegance without CSS** — semantic tables with `<caption>`/`<th scope>`, `<dl>` metadata, hex-dump tables for bytes, whitespace/grouping; no `<div>` soup or inline `style`; (10) **restraint** — one purpose/page, scannable tables, exact byte counts on the dashboard, exact bytes in details.

## 3. Security alignment

`viewer-security.md` applies verbatim; design consequences:
- Runs only when invoked (`cask web`; no `enabled` switch). Localhost default; non-loopback requires HTTPS or `allow_insecure_bind: true` + startup warning.
- Auth: startup admin token + session cookie (`HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS); idle 30 min / max 8 h.
- Roles: `viewer` (dashboard, list, metadata, download raw — all GET); `operator` (+ run `verify`, POST); `admin` (+ `delete`, `GC`, maintenance, POST).
- Every mutation is a POST with server-validated CSRF token (hidden form field; htmx forms are ordinary forms).
- Audit-log all admin actions (delete, GC, verify); never log tokens/secrets.
- Missing/expired session → 401 empty on data endpoints; insufficient role → 403 empty; never disclose object existence. The dashboard landing (`/viewer/`) alone redirects (303) to `/viewer/login` when unauthenticated and also accepts a direct `?token=` login (viewer-security §5).
- Browser talks only to the backend API; backend to the store. Validate every query param/header/hash (`sha256.Parse`); reject malformed before touching storage.

## 4. Rendering architecture — nested Go templates

Only `html/template` (auto-escaping = XSS boundary), embedded via `embed.FS` + `template.ParseFS`. The whole set is parsed once in `web.New` into a single template named `viewer`, and `Server.render` executes the named template into a buffer first, so a template error yields a clean 500 instead of a half-written 200. Composition is `{{define}}`/`{{template}}` only — there is no `{{block}}`. **A fragment is a named template rendered standalone**: the same partial serves full-page composition and an htmx swap (`object-table` inside `objects`, and `object-table-fragment` alone for the live search swap).

`{{define}}` blocks (`internal/web/templates/*.html`):

| File | Blocks |
|---|---|
| `partials.html` | `head`, `stat-cards`, `digest-note`, `sample-table`, `object-table`, `hexdump-table`, `result` |
| `login.html` | `login` (standalone shell) |
| `dashboard.html` | `dashboard` (full page), `dashboard_panels` (fragment) |
| `objects.html` | `objects` (full page), `object-table-fragment` (fragment) |
| `object.html` | `object` (full page), `hexdump` (fragment) |
| `gc.html` | `gc` (standalone shell) |

There is no `base` shell, no `stat-card`/`stats-panel`/`quick-nav`/`object-row`/`_error`/`fragments` block, and no out-of-band target: every full page repeats its own `<html>`/`<head>`/`{{template "head" .}}`/`<main>`, `login` and `gc` carry no nav at all, and the dashboard sample has no OOB refresh.

Real element ids (markup hooks and htmx swap targets): `#object-list` (list page: the `<div>` wrapping the table), `#object-table` (`<table id="object-table">`, never a swap target itself), `#hexdump` (detail page: the lazy `<div>` placeholder, and the id of the `<table id="hexdump">` that replaces it), `#object-meta` (detail `<dl>`), `#action-result` (detail page: the empty result `<div>`), `#gc-result` (GC page: the empty result `<div>`), plus `#sample-table`, `#q`, `#roots`, `#token` as markup hooks.

Conventions: one template per view + small partials; minimal logic (built-ins `{{if}}`/`{{range}}`/`{{eq}}`/`{{or}}`/`{{not}}` and pipelines only), all computation in Go handlers passing pre-shaped data; raw semantic HTML only.

**Registered `template.FuncMap` helper: `shortDigest` only (it renders `cas.Digest.Prefix(8)`).** `web.New` registers exactly that one function — `shortDigest(d cas.Digest) string`, the first 8 hex chars via `sha256.Short` (`""` for the absent digest). Nothing else is registered: there is no `digestWithType`, `humanSize`, `byteSize`, or `hexdump` `FuncMap` entry (`digestWithType`/`parseDigestOrNil` exist only as unreferenced Go helpers, called by no template; `humanSize`/`byteSize` exist in no Go file). The templates do not call `shortDigest` either: the handlers precompute `objectRow{Digest, Short, Type, Size}` with it, and `sample-table`/`object-table` render the precomputed `.Short` field. `hexdump(data []byte) []dumpRow` is a **Go** helper, not a `FuncMap` entry: `objectRaw` calls it and passes `Rows` (offset/hex/ASCII per 16-byte row) plus an optional truncation `Note`, and `hexdump-table` ranges over them. Sizes render as exact byte counts (`.Size`, the store totals) — never humanized units.

## 5. Hypermedia interaction model (htmx)

htmx attributes are the only interactivity; the single vendored script is served at `/viewer/static/htmx.min.js`. Every htmx attribute in `internal/web/templates/`:

| View | Element | Attributes |
|---|---|---|
| `objects` | search `<input id="q">` | `hx-get="/viewer/objects"`, `hx-trigger="input changed delay:300ms"`, `hx-target="#object-list"`, `hx-push-url="true"` |
| `object` | verify `<form>` (operator/admin) | `hx-post="/viewer/objects/{hash}/verify"`, `hx-target="#action-result"` |
| `object` | delete `<form>` (admin) | `hx-post="/viewer/objects/{hash}/delete"`, `hx-target="#action-result"`, `hx-confirm="Delete this object?"` |
| `object` | lazy `<div id="hexdump">` | `hx-get="/viewer/objects/{hash}/raw"`, `hx-trigger="revealed"`, `hx-target="#hexdump"`, `hx-swap="outerHTML"` |
| `gc` | GC `<form>` | `hx-post="/viewer/gc"`, `hx-target="#gc-result"`, `hx-swap="innerHTML"` |

- Everything else is plain HTML: the nav links, the login form, and the dashboard's search form are ordinary `method="get"`/`method="post"` forms; the htmx forms keep their `action`/`method` too, so they still work with htmx disabled.
- Live search replaces the **inside** of `#object-list` (the default `innerHTML` swap) with `object-table-fragment`; the handler selects that fragment when `HX-Request: true` and the full `objects` page otherwise. There is **no** paging: the list renders every object in one table, and the dashboard sample is capped at 10 (`index.Paginate(digests, 0, 10)`).
- The raw view replaces its own placeholder (`hx-target="#hexdump"` + `hx-swap="outerHTML"`), so `<div id="hexdump">` becomes `<table id="hexdump">` — the lazy-loaded hexdump.
- Mutations swap the returned `result` fragment (`<p class="result">…</p>`) into `#action-result` or `#gc-result`; `verify`/`delete` do not re-render the list, and destructive delete asks `hx-confirm` first. Every mutation is a POST carrying the session's CSRF token in a hidden `csrf` field.
- GET endpoints are side-effect free; every state change is a POST form. Mutation responses are the result fragment (never 204); an unauthenticated request gets 401 with an empty body and an insufficient role 403 with an empty body.
- No `hx-boost`, no `hx-swap-oob`, no polling (`hx-trigger="every …"`), no click-to-load/paging, and no custom events, `_hyperscript`, or Alpine — only the attributes above.
- `GET /viewer/dashboard` exists and returns the `dashboard_panels` fragment, but no template issues that request, so nothing refreshes the dashboard in place today.

## 6. Pages & routes

All under `/viewer`. The surface is fixed by `Server.Handler()`: there is no config block and no `-config` file yet (cli §2 defers it) — `cask web` selects only store, bind address, role tokens and the insecure-bind acknowledgement. `{hash}` values are parsed with the client's `sha256.Parse` (printable `sha256:hexdigest` or bare hex) before storage access.

| Route | Method | Content | Role |
|---|---|---|---|
| `/viewer/login` | GET/POST | startup-token login → session cookie | — |
| `/viewer/?token=<token>` | GET | direct `?token=` login → session cookie → 303 to `/viewer/` (throttled; `Referrer-Policy: no-referrer`) | — |
| `/viewer/` | GET | dashboard: store-overview stats, addressing note, 10-object sample, search, nav (Objects · GC); unauthenticated → 303 to `/viewer/login` | viewer |
| `/viewer/dashboard` | GET | the dashboard panels (stats + addressing note + sample) as one `dashboard_panels` fragment; no template requests it today | viewer |
| `/viewer/objects` | GET | object list: search box + one unpaged table (fragment target `#object-list`) | viewer |
| `/viewer/objects/{hash}` | GET | object detail: `object-meta` + role-gated verify/delete actions + lazy hexdump placeholder | viewer |
| `/viewer/objects/{hash}/raw` | GET | hexdump fragment: `<table id="hexdump">`, preview truncated at 256 KiB with a note | viewer |
| `/viewer/objects/{hash}/verify` | POST | integrity check → `result` fragment | operator |
| `/viewer/objects/{hash}/delete` | POST | delete (hx-confirm) → `result` fragment | admin |
| `/viewer/gc` | GET | GC page: root-hash form + `#gc-result` area | admin |
| `/viewer/gc` | POST | mark-and-sweep GC from the submitted root hashes → `result` fragment | admin |

## 7. Data views

**Dashboard (landing):** a store-overview line (`stat-cards`: object count · total bytes); an **addressing note** (`digest-note`: digests are raw digest bytes rendered as lowercase hex — the core names no algorithm; this viewer digests and validates with `sha256`); sample objects (`sample-table`: the first 10 of `Backend.List` via `index.Paginate(digests, 0, 10)`, rows as `<shorthash> (<type>)` linking to details via the full digest, then a "see all objects" link — there is no paging); a search form (`GET /viewer/objects?q=…`) as the entry point for "find this hash"; a `<nav>` line (Objects · GC).

**Objects:** UI links ALWAYS show the **8-char short hash** (`cas.Digest.Prefix(8)` via the `shortDigest` FuncMap entry, e.g. `9f86d081`); the link `href` always carries the **full digest** (lowercase hex, e.g. `/viewer/objects/9f86d081…`, no algorithm prefix) — short form is display-only, identity never lost; the full digest is always on the detail page (`object-meta`). Generic lists render `<shorthash> (<type>)`; tables with a dedicated type column MAY show the plain short hash. List columns: hash (short), `Type()`, size; search filters by hash or type substring (active search, `q`). Detail order: summary (`object-meta` `<dl>`: full digest, the client's constant algorithm name `sha256`, type, exact size) → actions (verify/delete per role) → raw bytes (hexdump, lazy).

**References are out of scope:** the viewer is a **byte-layer** tool and MUST NOT interpret typed references (resolving `References()` needs an app object model; the product ships none; `internal/` and `cas/` MUST NOT import `examples/`). Reference graphs belong to app layers (`gitlike`). The viewer shows objects, bytes, and integrity, not typed structure.

**Blobs:** the `raw` view shows exact serialized bytes — a hex dump table (`hexdump-table`: 16-byte rows, offset/hex/ASCII columns) plus the exact total size on the detail page. Hexdump is **lazy-loaded** via htmx (`revealed`) so large objects don't block the page (the handler streams at most `previewLimit` = 256 KiB and reports truncation; never buffer megabytes). The stored type comes from the TLV envelope; raw JSON payload is visible as-is.

**Integrity & maintenance:** `verify` recomputes the stored-bytes digest with the client's hasher and reports match/mismatch (`Verify(ctx, d, hasher)` contract) as a `result` fragment. `gc` runs mark-and-sweep from the root hashes submitted in the form and returns a `result` fragment reporting the object count delta; only objects not in the reachable set are removed, and the GC run is audit-logged (admin only). There is no progress polling.

## 8. Out of scope

- No JSON/data API (hypermedia only; no `/api/...`, no JSON responses). The product ships no programmatic data API; an app needing one copies the `examples/api` pattern.
- No CSS/JS (no stylesheets, `<style>`, custom `<script>`; htmx is the only script, vendored/pinned).
- No charting/dashboard libraries, no CSS frameworks, no build step.
- No client-side rendering; no HTML string concatenation in Go.
- No object editing — objects are immutable; the viewer only inspects, verifies, and (admin) deletes/GCs.
- No typed references/graph (byte-layer tool).

## 9. Acceptance checklist

- [x] Dashboard is the landing page: store-overview stats, addressing note, 10-object sample, search, nav — all with drill-down links
- [x] Simple/elegant/usable: consistent layout, scannable tables, one purpose per page, no dead ends
- [x] Every `viewer-security.md` requirement implemented
- [x] No CSS, no hand-written JS in `internal/web/`
- [x] HTML only via `html/template`, nested `{{define}}`/`{{template}}`, embedded `embed.FS`
- [x] Full pages and fragments share partials (`object-table` reused by `object-table-fragment`; `hexdump-table` used by the `hexdump` fragment)
- [x] Objects viewable: 8-char short-hash links (`objectRow.Short`; full digest on detail + link targets), the client's constant algorithm name `sha256`, type, size; the dashboard sample shows `<shorthash> (<type>)`
- [x] No typed references/graph in `internal/web/` (byte-layer)
- [x] Blobs viewable: hex dump table, lazy-loaded for large objects
- [x] Mutations (verify/delete/GC) are POST + CSRF + role-checked + audit-logged; GET side-effect free
- [x] Works with htmx disabled (links/forms still function)
- [x] `{hash}` parsed with `sha256.Parse`; malformed → 400, missing session → 401 empty, insufficient role → 403 empty
