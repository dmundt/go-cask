---
type: Design Document
title: Object Browser Logic — go-cask
description: Formal server-side state, transition, rendering, and invariants contract for the viewer object browser.
version: v1
---

# Object Browser Logic — go-cask

This document translates the interaction logic in
[`go-cask-viewer.html`](go-cask-viewer.html) into the server-rendered viewer
architecture. It complements the visual brief and records the behavior that
the HTML mockup expresses through JavaScript. The normative viewer requirements
remain in `docs/specs/viewer-design.md`.

## 1. Model and authority

The viewer derives each object-browser response from the authoritative
`fs.Backend` at request time. It MUST NOT keep a browser-side copy of objects,
store view state in browser storage, or infer typed application references.

The server creates a normalized record for each listed digest:

| Field | Source | Rule |
|---|---|---|
| Digest | `Backend.List` | Full lowercase hexadecimal identity |
| Short digest | `Digest.Prefix(8)` | Display only; links retain full digest |
| Type | TLV envelope prefix | Best effort; empty when unreadable |
| Size | `Backend.Size` | Exact byte count |
| Integrity | session-scoped verification result | `not verified` until verified |

The viewer MUST NOT expose the mockup's generated reference counts, object
ages, timestamps, reference rows, or deterministic digest-derived byte preview.
Those values are not byte-layer backend facts.

## 2. URL state

The object browser has one canonical state representation:

```text
/viewer/objects?q={text}&type={type}&size={bucket}&status={integrity}
  &sort={hash|type|size}&dir={asc|desc}&limit={25|50|100|250}
  &offset={non-negative}&selected={digest}&tab={metadata|bytes|actions}
```

| Key | Default | Valid values | Effect |
|---|---|---|---|
| `q` | empty | trimmed text | Case-insensitive digest/type substring |
| `type` | empty | type present in the current result domain | Exact envelope type |
| `size` | empty | `small`, `medium`, `large` | `<1 KiB`, `1 KiB–1 MiB`, `>1 MiB` |
| `status` | empty | `not-verified`, `verified`, `corrupt` | Session integrity result |
| `sort` | `hash` | `hash`, `type`, `size` | Primary order |
| `dir` | `asc` | `asc`, `desc` | Sort direction |
| `limit` | `25` | `25`, `50`, `100`, `250` | Maximum rows per response |
| `offset` | `0` | non-negative integer | First result position |
| `selected` | empty | valid listed digest | Inspector target |
| `tab` | `metadata` | `metadata`, `bytes`, `actions` | Inspector panel |

Malformed values return HTTP 400. The server MUST NOT silently substitute a
different enum, limit, offset, or digest. A valid offset beyond the matched
set returns the empty result page with a valid pager state.

## 3. Derivation pipeline

Each `GET /viewer/objects` response applies the following deterministic steps:

1. Parse and validate URL state.
2. List digests and form normalized records.
3. Apply `q`, `type`, `size`, and `status` filters.
4. Sort records using the requested key/direction. Digest/type comparisons use
   lexical order; size comparisons use exact integer bytes.
5. Calculate matched count and total matched bytes.
6. Take the offset/limit slice.
7. Resolve `selected`: retain it only if it is in the matched set; otherwise
   no inspector selection exists.
8. Render the object-list component containing table, result summary, and
   pager. Render the inspector component independently from the selected
   normalized record.

The result summary reports the shown range, matched count, and total bytes over
the complete matched set, never only the visible page.

## 4. State transitions

| Trigger | State update | Response |
|---|---|---|
| Search/type/size/status filter | Set filter; set `offset=0`; preserve legal sort/limit/tab | Object-list fragment |
| Sort header | Toggle `dir` for same `sort`; otherwise set sort and `asc`; set `offset=0` | Object-list fragment |
| Page-size control | Set `limit`; set `offset=0` | Object-list fragment |
| First/previous/next/last pager link | Set bounded offset from matched count | Object-list fragment |
| Object-row link | Set `selected`; retain legal list state | Inspector fragment or full detail |
| Inspector panel link | Set `tab`; retain selection/list state | Inspector fragment |
| Verify form | Recompute selected object with injected hasher; store result only in server session | Integrity fragment |
| Delete/GC form | Perform existing authorized mutation; audit log | Existing result fragment |

All filter/sort/pager/row/panel controls are ordinary GET links or forms first.
htmx enhances them through `hx-get`, `hx-target`, and `hx-push-url`; the
destination URL always reconstructs the same state without htmx.

## 5. Rendering boundaries

### 5.1 Composition tree

The viewer follows a composable-template philosophy. Pages own document
assembly only; components own one semantic responsibility and are reused by
both full pages and htmx fragments.

```text
viewer-page
├── head
├── top-bar
└── object-browser
    ├── filter-bar
    │   ├── search-control
    │   └── filter-controls
    └── browser-workspace
        ├── object-list
        │   ├── object-table
        │   │   ├── sort-header
        │   │   └── object-row
        │   └── pager
        └── object-inspector
            ├── inspector-header
            ├── inspector-panels
            │   ├── metadata-panel
            │   ├── bytes-panel
            │   │   └── hexdump-table
            │   └── actions-panel
            └── integrity
```

`object-list` and `object-inspector` are the only independently swappable
workspace components. A child component MUST NOT issue a duplicate request for
data owned by its parent. Each component receives a typed, pre-shaped view
model rather than reaching into request state or storage.

### 5.2 Component boundaries

| Component | Input | Swap boundary |
|---|---|---|
| `viewer-page` | page title and browser view model | Full document only |
| `top-bar` | navigation/action model | Child of full page |
| `filter-bar` | durable query state and available filter choices | Child of full page |
| `object-list` | rows, summary, pager, query links | `#object-list` |
| `object-table` | visible rows and active sort | Child of object list |
| `sort-header` | label, active direction, complete state URL | Child of object table |
| `object-row` | normalized object, selection state, detail URL | Child of object table |
| `pager` | total/matched/offset/limit/query links | Child of object list |
| `object-inspector` | selected object and active panel | `#object-inspector` |
| `inspector-panels` | selected object, active panel, state URLs | Child of inspector |
| `integrity` | session verification state | `#integrity` |
| `hexdump-table` | bounded raw preview | `#hexdump` |

A direct browser request receives a complete document. An `HX-Request: true`
request receives only the named component appropriate to its target. The same
template components produce both forms.

## 6. Prototype adaptation

| Mockup behavior | Server-rendered implementation |
|---|---|
| In-memory 512-object collection | Backend list for each request |
| Mutable JavaScript selection | `selected` URL state |
| JavaScript pagination clamp | Valid offset is preserved; empty page is explicit |
| Client tab keyboard model | Panel links with standard focus/navigation |
| Draggable/keyboard splitter | Fixed 440px desktop column and responsive breakpoint |
| Clipboard copy | Selectable full digest text |
| Reference history and graph | Excluded from byte-layer viewer |
| Verify all | Excluded pending an authorized bounded server operation |
| Digest-derived hexdump | Actual bounded object bytes |

## 7. Invariants

- GET requests are side-effect free.
- Every URL state is validated before storage access.
- No response fabricates backend facts.
- Table, result count, and pager derive from one filtered/sorted sequence.
- A selected object is never rendered when it is absent from the matched set.
- A missing selection renders explicit empty inspector content.
- 401/403 behavior, roles, CSRF, and audit logging remain governed by
  `viewer-security.md`.
- Raw bytes remain bounded by the existing 256 KiB preview limit.

## 8. Verification matrix

| Requirement | Test |
|---|---|
| URL defaults/invalid values | Table-driven handler tests for every state key |
| Filter/sort pipeline | Seeded backend with known envelope types/sizes |
| Pagination | First/middle/last/empty offset and query-retention cases |
| Progressive enhancement | Links/forms return complete documents without htmx |
| Fragment boundaries | htmx requests return only named list/inspector fragments |
| Selection/panels | Direct URLs and htmx swaps preserve legal state |
| Integrity | Session result is scoped, verified/corrupt states are truthful |
| Accessibility | Headers, labels, sort state, selected row, and empty state |
