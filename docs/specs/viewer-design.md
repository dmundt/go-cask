---
type: Specification
title: Viewer Design — go-cask
description: Design of the embedded technical viewer — simple, elegant, and usable; dashboard-first hypermedia UI with nested Go templates + htmx only (no JS/CSS), exposing the object store at a low technical level (objects, blobs, stats). The viewer is a byte-layer tool: it shows objects, bytes, and integrity, never typed reference graphs.
version: v10
---

# Viewer Design — go-cask

The embedded technical browser UI in `internal/web/`, for developers/admins browsing the CAS. Defines **how** (hypermedia, nested Go templates + htmx only, raw HTML) and **what** (dashboard hub → objects/blobs/stats at a technical level). MUST be **simple, elegant, usable** — elegance from clean semantic structure/layout/hierarchy, not CSS. Read with `viewer-security.md` (all requirements apply unchanged), `coding-guidelines.md` (§4 no CSS/JS, §5 templates+htmx, §6 raw HTML, §10 viewer boundary), `cas-core.md` (data model: `Hash`, `Object[T]`, `Backend.Stats`, `Verify`, `GC`). Design reference: hypermedia.systems. `docs/design/viewer-brief.md` is non-normative next-iteration input; changes nothing here until folded back.

## 1. Purpose & persona

- Persona: developer/operator answering "what is stored? how much space? what does this point to? is it intact? which algorithms?".
- Hub: a **dashboard** (landing) with storage stats, algorithm breakdown, a sample of objects, and search — one click to every detail.
- Drill-down: dashboard → object list → object detail → raw blob/hexdump.
- Aesthetic: simple, elegant, usable — dense but scannable, plain semantic HTML, no decoration.
- Out of scope: object editing (objects are immutable), JSON/data APIs, client-side state, charting.

## 2. Design principles

Hypermedia-driven: (1) **HTML is the application** — every transition is an HTTP request (GET nav, POST mutation) returning HTML (full page or fragment); (2) server renders all HTML, client has no app logic; (3) **htmx is the only extension**, expressed purely via htmx attributes; (4) **progressive enhancement** — works with htmx disabled (real links/forms); (5) zero hand-written JS/CSS; the htmx script is the single vendored exception.

Simple/elegant/usable: (6) **dashboard-first** landing (numbers that matter, sample, search; every screen answers "what am I looking at, where next?"); (7) **information hierarchy** overview → list → detail → raw; detail pages start with a summary block then go deeper; (8) **no dead ends** — every hash is a link, every panel has a "see all", always back to the dashboard; (9) **elegance without CSS** — semantic tables with `<caption>`/`<th scope>`, `<dl>` metadata, `<pre>` bytes, whitespace/grouping; no `<div>` soup or inline `style`; (10) **restraint** — one purpose/page, scannable tables, human-readable sizes on the dashboard, exact bytes in details.

## 3. Security alignment

`viewer-security.md` applies verbatim; design consequences:
- Runs only when invoked (`cask web`; no `enabled` switch). Localhost default; non-loopback requires HTTPS or `allow_insecure_bind: true` + startup warning.
- Auth: startup admin token + session cookie (`HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS); idle 30 min / max 8 h.
- Roles: `viewer` (dashboard, list, metadata, download raw — all GET); `operator` (+ run `verify`, POST); `admin` (+ `delete`, `GC`, maintenance, POST).
- Every mutation is a POST with server-validated CSRF token (hidden form field; htmx forms are ordinary forms).
- Audit-log all admin actions (delete, GC, verify); never log tokens/secrets.
- Missing/expired session → 401 empty on data endpoints; insufficient role → 403 empty; never disclose object existence. The dashboard landing (`/viewer/`) alone redirects (303) to `/viewer/login` when unauthenticated and also accepts a direct `?token=` login (viewer-security §5).
- Browser talks only to the backend API; backend to the store. Validate every query param/header/hash (`ParseHash`); reject malformed before touching storage.

## 4. Rendering architecture — nested Go templates

Only `html/template` (auto-escaping = XSS boundary), embedded via `embed.FS` + `template.ParseFS`. Composition via `{{define}}`/`{{template}}`/`{{block}}`. **A fragment is a named template rendered standalone** — the same partial serves full-page composition and htmx swaps (e.g. `stats-panel` on the dashboard and as OOB target).

Template tree (partials in `internal/web/templates/`):
- `base` (html shell + nav) → `dashboard` (stat-card, stats-panel [OOB], sample-table, quick-nav), `login` (standalone), `objects` (object-row), `object` (object-meta, hexdump [lazy]); `fragments` reuse the same partials; `_error` (minimal; 401/403 empty).

Conventions: one template per view + small partials; minimal logic (`{{if}}`/`{{range}}`/`{{with}}`/pipelines), all computation in Go handlers passing pre-shaped data; registered pure `template.FuncMap` helpers — `shortHash` (first 8 hex chars), `hashWithType` (`<shorthash> (<type>)` for generic lists), `humanSize` (overviews), `byteSize` (exact), `hexdump` (format bytes); raw semantic HTML only.

## 5. Hypermedia interaction model (htmx)

| Pattern | Viewer use |
|---|---|
| Plain links + forms | base navigation; every link/button is real HTML first |
| `hx-boost` | optional whole-page nav boost so fragments share the shell |
| Active search | `hx-get="/viewer/objects?q=…"`, `hx-trigger="input changed delay:300ms"`, `hx-target="#object-table"` |
| Click-to-load | paging: "next page" button `hx-get` appends rows |
| Lazy loading | hexdump/raw `hx-trigger="revealed"` on the `<pre>` |
| Mutations as POSTs | delete/verify/GC via `hx-post` forms, `hx-confirm` for destructive, CSRF included |
| Polling | long GC progress fragment `hx-trigger="every 2s"` until done |
| Out-of-band swaps | dashboard `stats-panel` refreshes alongside content (`hx-swap-oob`) |
| 204/errors | mutation success → 204 or swapped fragment; 401/403 → empty body |

- GET endpoints side-effect free; every state change is a POST form.
- `hx-target`/`hx-swap` always target a semantic container (`#content`, `#object-table`, `#hexdump`, `#stats-panel`) — never whole page unless intended.
- htmx fragments render the **same partials** as full pages (one source of truth).
- Dashboard panels refresh via one `hx-get="/viewer/dashboard"` (fragment with OOB swaps); no timers beyond GC progress polling.
- No custom events, `_hyperscript`, or Alpine — htmx attributes only.

## 6. Pages & routes

All under `/viewer` (configurable via the `viewer:` config block). `{hash}` values validated with `ParseHash` before storage access.

| Route | Method | Content | Role |
|---|---|---|---|
| `/viewer/login` | GET/POST | startup-token login → session cookie | — |
| `/viewer/?token=<token>` | GET | direct `?token=` login → session cookie → 303 to `/viewer/` (throttled; `Referrer-Policy: no-referrer`) | — |
| `/viewer/` | GET | dashboard: stat cards, algorithm table, sample, search, quick nav; unauthenticated → 303 to `/viewer/login` | viewer |
| `/viewer/dashboard` | GET | dashboard panels (stats + sample), htmx refresh fragment | viewer |
| `/viewer/objects` | GET | object list: filter + table (search fragment target) | viewer |
| `/viewer/objects/{hash}` | GET | object detail: meta + actions | viewer |
| `/viewer/objects/{hash}/raw` | GET | raw serialized bytes + hexdump (lazy `<pre>`) | viewer |
| `/viewer/objects/{hash}/verify` | POST | integrity check → result fragment | operator |
| `/viewer/objects/{hash}/delete` | POST | delete (hx-confirm) → updated list | admin |
| `/viewer/gc` | POST | mark-and-sweep GC with polling progress | admin |

## 7. Data views

**Dashboard (landing):** stat cards (total objects, total size, algorithms in use); algorithm breakdown (`stats-panel` from `Backend.Stats`, OOB-refreshed — the dashboard is the stats view); sample objects (`sample-table`, first N from `Backend.List`, rows as `<shorthash> (<type>)` linking to details via the full hash); prominent search (swaps the object table — the entry point for "find this hash"); quick nav line (Objects · Verify · GC).

**Objects:** UI links ALWAYS show the **8-char short hash** (`shortHash`, e.g. `9f86d081`); the link `href` always carries the full `algo:hexdigest` — short form is display-only, identity never lost; the full hash is always on the detail page (`object-meta`). Generic lists render `<shorthash> (<type>)` (`hashWithType`); tables with a dedicated type column MAY show the plain short hash. List columns: hash (short+type), algorithm, `Type()`, size; filter by algorithm and hash/type substring (active search). Detail order: summary (`object-meta` `<dl>`: full hash, algorithm, type, exact size) → actions (verify/delete per role) → raw bytes (hexdump, lazy).

**References are out of scope:** the viewer is a **byte-layer** tool and MUST NOT interpret typed references (resolving `References()` needs an app object model; the product ships none; `internal/` and `cas/` MUST NOT import `examples/`). Reference graphs belong to app layers (`gitlike`). The viewer shows objects, bytes, and integrity, not typed structure.

**Blobs:** the `raw` view shows exact serialized bytes — a classic hex dump in a `<pre>` (16-byte rows: offset, hex, ASCII columns) plus exact total size. Hexdump is **lazy-loaded** via htmx (`revealed`) so large objects don't block the page (streaming reads; never buffer megabytes). Type shown per `Type()`; raw JSON visible as-is.

**Integrity & maintenance:** `verify` recomputes the stored-bytes hash and reports match/mismatch (`Verify` contract). `gc` runs mark-and-sweep with a polling progress fragment; only objects not in the reachable set are removed; every deletion audit-logged (admin only).

## 8. Out of scope

- No JSON/data API (hypermedia only; no `/api/...`, no JSON responses). The product ships no programmatic data API; an app needing one copies the `examples/api` pattern.
- No CSS/JS (no stylesheets, `<style>`, custom `<script>`; htmx is the only script, vendored/pinned).
- No charting/dashboard libraries, no CSS frameworks, no build step.
- No client-side rendering; no HTML string concatenation in Go.
- No object editing — objects are immutable; the viewer only inspects, verifies, and (admin) deletes/GCs.
- No typed references/graph (byte-layer tool).

## 9. Acceptance checklist

- [x] Dashboard is the landing page: stat cards, algorithm breakdown, sample objects, search, quick nav — all with drill-down links
- [x] Simple/elegant/usable: consistent layout, scannable tables, one purpose per page, no dead ends
- [x] Every `viewer-security.md` requirement implemented
- [x] No CSS, no hand-written JS in `internal/web/`
- [x] HTML only via `html/template`, nested `{{define}}`/`{{template}}`/`{{block}}`, embedded `embed.FS`
- [x] Full pages and fragments share partials (incl. `stats-panel` as OOB target)
- [x] Objects viewable: 8-char short-hash links (full hash on detail + link targets), algorithm, type, size; generic lists show `<shorthash> (<type>)`
- [x] No typed references/graph in `internal/web/` (byte-layer)
- [x] Blobs viewable: raw bytes + hex dump, lazy-loaded for large objects
- [x] Mutations (verify/delete/GC) are POST + CSRF + role-checked + audit-logged; GET side-effect free
- [x] Works with htmx disabled (links/forms still function)
- [x] `{hash}` validated with `ParseHash`; malformed → 400, missing session → 401 empty, insufficient role → 403 empty
