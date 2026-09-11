---
type: Specification
title: Frontend Architecture — go-cask
description: How the browser-facing frontend is architected — hypermedia-driven server-side rendering with nested Go templates, htmx-only interactivity, fragment-based updates, URL-as-state navigation, and the no-CSS/no-JS embedding model.
version: v5
---

# Frontend Architecture — go-cask

Governs the browser-facing architecture of go-cask (applies to the viewer and any future frontend). Concrete screens/routes/wireframe are defined by `viewer-design.md`. Related: viewer-design, viewer-security, coding-guidelines (no CSS/JS, templates+htmx), api-design.

## 1. Purpose and scope

- The frontend is everything the browser receives: **HTML pages and htmx fragments**, all server-rendered by `html/template`.
- Deliberately **not** an SPA: no client framework, no client-side state, no JSON between browser and server, no JS-generated DOM. The browser is a hypermedia client (links, form submits, htmx fragment swaps).

## 2. Rendering model (hypermedia-driven)

- **HTML is the application** (HATEOAS): every state transition is an HTTP request returning HTML — a full page for navigation, a fragment for updates.
- **Progressive enhancement:** with htmx disabled the frontend still works (real links/forms); htmx only upgrades (fragment swaps, active search, lazy loading).
- **One source of truth:** the server renders; the browser displays; no duplicated client rendering.

## 3. Template architecture

- `html/template` only — auto-escaping is the XSS boundary; never `text/template` for HTML, never HTML built by Go string concatenation (coding-guidelines §5–§6).
- Embedded via `embed.FS` + `template.ParseFS` — no runtime file I/O, no build step.
- **Nested composition** via `{{define}}`/`{{template}}`/`{{block}}`. Concrete template tree defined once in `viewer-design.md` §4 (not duplicated here); any frontend follows the same nesting pattern.
- **Fragments are the same partials rendered standalone:** an htmx endpoint returns a named template; the identical partial serves full-page composition and swaps (one source of truth).
- Minimal template logic (`{{if}}`/`{{range}}`/`{{with}}` + pipelines); all computation in Go; registered pure `FuncMap` helpers.

## 4. Interaction architecture (htmx)

| Concern | Mechanism |
|---|---|
| Navigation | real links; `hx-push-url` keeps URLs as state when a fragment is loaded |
| Search/filter | active search: `hx-get="/viewer/objects"`, `hx-trigger="input changed delay:300ms"`, `hx-target="#object-list"` |
| Partial updates | `hx-get`/`hx-post` + `hx-target` + `hx-swap` into semantic containers |
| Lazy loading | `hx-trigger="revealed"` loads the hexdump table into `#hexdump` |
| Paging | none in the viewer: the dashboard shows a fixed 10-object sample and the objects page lists all (filtered) digests |
| Long-running ops | no polling: verify/delete/gc answer with a `result` fragment |
| Cross-panel update | none: every swap targets the panel that asked for it |
| Destructive actions | POST forms + `hx-confirm` + CSRF token |

- GET endpoints are side-effect free; every mutation is a POST form with CSRF (viewer-security).
- `hx-target`/`hx-swap` always target a semantic container — `#object-list` (search), `#object-table`, `#hexdump`, `#object-meta`, `#action-result` (verify/delete), `#gc-result` — never the whole page.
- No custom events, no `_hyperscript`, no Alpine, no hand-written JS — htmx attributes only (coding-guidelines §4).

## 5. Navigation and state

- **URLs are the state:** `hx-push-url` keeps navigation in the address bar; refresh and back/forward work; no client-side state to lose or rehydrate.
- Identity from the server session cookie (`HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS — viewer-security); the browser never holds tokens/secrets.
- Fragments reachable both standalone and as parts of full pages — the URL always identifies the resource, not a client-side view.

## 6. Assets and embedding

- Single binary: templates + vendored htmx embedded via `embed.FS`.
- No npm, no build step, no static asset pipeline (coding-guidelines §10). Only script in the runtime is **htmx** (one pinned, vendored file).

## 7. Semantics and accessibility

- Raw semantic HTML: `<main>`, `<nav>`, `<table>` with `<caption>`/`<th scope>`, `<dl>` for metadata, `<pre>` for bytes, `<form>`/`<label>` for input — no `<div>` soup, no inline `style`.
- Accessibility: labels on all inputs, `alt` text, logical heading order, keyboard-operable links/forms. htmx keeps native elements native (progressive enhancement), so focus/semantics survive.
- Elegance without CSS comes from structure, whitespace, consistent layout (viewer-design §2).

## 8. The viewer (reference frontend)

Reference implementation of this architecture: dashboard-first, low-level technical inspection (viewer-design §7). Any new frontend MUST follow this architecture and reuse template/htmx conventions; concrete screen design lives in `viewer-design.md`.

## 9. Security

- Nothing sensitive reaches the browser: no tokens, no secrets, no storage internals — only rendered HTML (viewer-security).
- Sessions are cookies (`HttpOnly`, `SameSite=Strict`); CSRF tokens protect every mutation; 401/403 responses are empty bodies never disclosing existence.
- htmx requests carry the same session cookie as full-page navigation — the backend cannot distinguish and MUST NOT need to.

## 10. Checklist

- [x] All HTML via `html/template`; templates nested via `{{define}}`/`{{template}}`/`{{block}}`; embedded with `embed.FS`
- [x] Fragments reuse the same partials as full pages (one source of truth)
- [x] Interactivity via htmx attributes only; no hand-written JS/CSS
- [x] GET side-effect free; mutations = POST + CSRF
- [x] URLs are the state (`hx-push-url`); refresh/back work
- [x] Semantic HTML + accessibility per §7
- [x] Single binary, no build step; htmx vendored and pinned
- [x] Security per §9 (cookies, CSRF, empty-body 401/403)
- [x] New frontends follow this architecture; viewer screens per `viewer-design.md`
