---
type: Specification
title: Viewer Design — go-cask
description: Design of the embedded technical viewer — a styled, server-rendered master-detail object browser composed from Go templates, scoped CSS, and htmx-only interaction.
version: v16
---

# Viewer Design — go-cask

The embedded technical browser UI in `internal/web/` is a dense, desktop-first
object browser for developers and operators. It defines the viewer's screens,
visual system, template composition, and hypermedia interactions. Read with
`viewer-security.md`, `frontend-architecture.md`, `coding-guidelines.md`, and
`api-design.md`. The visual reference is
[`docs/design/go-cask-viewer.html`](../design/go-cask-viewer.html); its
JavaScript is prototype-only and MUST NOT ship.

## 1. Purpose and boundaries

- Persona: developer/operator answering "what is stored, how large is it, and
  is a selected object intact?"
- The viewer is a **byte-layer tool**. It shows objects, envelope types, exact
  sizes, bytes, and on-demand integrity results. It MUST NOT resolve typed
  references or import application object models.
- Object identity is a raw lowercase-hex digest. Lists show `Digest.Prefix(8)`;
  the inspector shows the full digest. The client algorithm may appear in the
  metadata view as `sha256`, but MUST NOT prefix a displayed digest.
- The object browser is the primary operational workspace. The dashboard
  remains the landing hub and links into that workspace.
- Out of scope: mutable object editing, uploads, buckets, charting, JSON APIs,
  browser storage, client-side state, and custom JavaScript.

## 2. Visual system

The viewer MUST reproduce the mockup's restrained technical-browser hierarchy
through `internal/web/viewer.css`: a white/near-white surface, dark foreground,
muted metadata, hairline borders, one green accent, system body font, and
monospace hashes/numbers/bytes. The CSS file is the only viewer stylesheet
(coding-guidelines §4).

| Token/metric | Contract |
|---|---|
| Top bar | 46px; `CA` mark, `go-cask` wordmark, right-aligned operational navigation/action |
| Filter bar | 47px; search, type, size, and integrity filters plus reset |
| Main workspace | flexible object-list column and 440px inspector column; 4px divider |
| Inspector bounds | 280px–560px visual range; fixed 440px default |
| Object table | dense mono data, sticky header, 30% digest column, remaining columns balanced |
| Controls | 30px form controls; compact bordered pager/action controls |
| Narrow view | at ≤900px, document scrolls; list precedes full-width inspector; filters scroll horizontally |

Colors, font stack, spacing, radii, status-tag colors, row hover/selection
tints, and focus indicators MUST follow the token values in
[`go-cask-object-browser.design.json`](../design/go-cask-object-browser.design.json).
CSS provides appearance only: every control, value, state label, and focusable
target MUST remain semantic HTML.

## 3. Pages and visible data

| Route | View | Role |
|---|---|---|
| `/viewer/` | dashboard: summary, search, sample, link to browser | viewer |
| `/viewer/objects` | master-detail object browser | viewer |
| `/viewer/objects/{hash}` | cold-load object detail | viewer |
| `/viewer/objects/{hash}/raw` | lazy hexdump fragment | viewer |
| `/viewer/gc` | maintenance form | admin |

The browser has a top bar, filter bar, table/pager master column, and inspector
detail column. The object table contains digest, type, exact size, integrity
status, and any optional metadata the backend can supply truthfully. It MUST
NOT fabricate reference counts, object age, incoming/outgoing references, or
stored verification state. A `not verified` status is valid until an
on-demand verification result exists in the current server session.

The inspector contains a summary header and server-selected panels:

1. **Metadata** — full digest, client algorithm, envelope type, exact size,
   and integrity result.
2. **Bytes** — lazy 16-byte-row hexdump and truncation note.
3. **Actions** — role-gated verify/delete forms; forms remain ordinary,
   CSRF-protected POSTs without htmx.

References, copy-to-clipboard, draggable resizing, and client-side history are
prototype behaviors. They are not viewer features unless a later server-side
contract supplies truthful data and URL-addressable behavior.

## 4. Rendering architecture and composition

The viewer uses `html/template`, parsed from `embed.FS`, and executes named
templates into a buffer before writing a response. Templates are deliberately
small and compose into pages and fragments:

| Component | Responsibility |
|---|---|
| `head` | metadata, `/viewer/static/viewer.css`, vendored htmx |
| `top-bar` | brand, navigation, operational action |
| `filter-bar` | one GET form for durable browser state |
| `object-table` | accessible headers, rows, empty state |
| `pager` | result summary plus first/previous/next/last links |
| `object-list` | table and pager; the primary htmx swap boundary |
| `inspector` | selected-object header and metadata/bytes/actions panels |
| `integrity` | verify result fragment |
| `hexdump-table` | lazy bytes fragment |
| `result` | mutation outcome |

Full pages compose these components; fragments execute the same named
components standalone. A component MUST receive pre-shaped data: templates may
range and branch but MUST NOT calculate filtering, sorting, pagination,
integrity, or layout.

## 5. URL state and htmx interactions

The object-browser URL owns all view state:

```text
/viewer/objects?q=<text>&type=<type>&size=<bucket>&status=<state>
  &sort=<hash|type|size>&dir=<asc|desc>&limit=<25|50|100|250>
  &offset=<non-negative>&selected=<digest>&tab=<metadata|bytes|actions>
```

- Omitted values select defaults: hash ascending, `limit=25`, `offset=0`, no
  filters, metadata panel.
- Validate every parameter. Invalid enumerations, disallowed limits, malformed
  selected digests, and negative/non-numeric offsets return 400; handlers MUST
  NOT silently clamp invalid input.
- Filtering or changing `limit` resets `offset` to zero. Sorting preserves
  filters and selection only when the selected digest remains in the filtered
  result set.
- The server filters, sorts, counts, slices, and renders. It returns
  `#object-list` (table plus pager) for htmx requests and the whole document
  otherwise.
- Search uses `hx-get`, `input changed delay:300ms`, `hx-include` of the
  filter form, `hx-target="#object-list"`, and `hx-push-url="true"`.
- Sort headers and pager are ordinary links with complete query state; htmx
  may enhance them with `hx-get`/`hx-target`/`hx-push-url`, never replacing
  their link behavior.
- Selecting a row is a real link that sets `selected`. htmx may request the
  same URL and swap `#object-inspector`; a direct request renders the complete
  object-detail document.
- Inspector panel links set `tab`; no browser-only tab state. The bytes panel
  lazy-loads the hexdump through `hx-trigger="revealed"`.
- Verify/delete/GC remain POST + CSRF + role checks + audit logging. Verify
  swaps only `#integrity`; delete/GC swap their result containers.

## 6. Security and accessibility

`viewer-security.md` applies unchanged: protected routes require a session;
401/403 have empty bodies; mutations are role-gated, CSRF-protected, and
audited; no token or secret reaches markup, logs, or browser state.

Tables require captions, scoped headers, `aria-sort` for the active sort
column, and an explicit empty row. Every form control has a label. The selected
row exposes its state, status labels contain text rather than color alone, and
all actions/links remain keyboard-operable. Focus styles in `viewer.css` MUST
meet the mockup's visible focus-ring contract.

## 7. Verification requirements

Viewer tests MUST cover:

- full-page and htmx rendering of each named component;
- URL defaults, valid filter/sort/page combinations, and invalid query 400s;
- pagination totals, boundaries, and pager links retaining query state;
- direct-link and htmx selection behavior;
- accessible table/sort/status markup and CSS asset headers;
- session/role/CSRF/error behavior and bounded raw-byte preview.

## 8. Checklist

- [x] Master-detail browser uses server-rendered HTML, htmx, and one scoped stylesheet
- [x] Component templates compose pages/fragments without duplicated rendering logic
- [x] Filter/sort/page/selection/panel state is URL-addressable and validated
- [x] Pagination is server-side and progressively enhanced
- [x] No custom JavaScript, browser storage, typed graph, or invented metadata
- [x] Security and accessibility requirements remain enforced
