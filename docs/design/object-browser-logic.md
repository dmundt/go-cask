---
type: Design Document
title: Object Browser Logic — go-cask
description: Formal server-side state, transition, rendering, and invariants contract for the viewer object browser.
version: v12
---

# Object Browser Logic — go-cask

Server-side translation of the interaction logic in
[`go-cask-viewer.html`](go-cask-viewer.html). Normative viewer requirements:
`docs/specs/viewer-design.md`; security: `viewer-security.md`; architecture:
`frontend-architecture.md`.

## 1. Model and authority

- Derived per request from the authoritative `fs.Backend`.
- No browser-side object copy, no browser storage, no inferred typed references
  (viewer-design.md §1).

| Field | Source | Rule |
|---|---|---|
| Digest | `Backend.List` | Full lowercase hexadecimal identity |
| Short digest | `Digest.Prefix(8)` + `...` | Display only; links retain full digest |
| Type | TLV envelope prefix | Best effort; empty when unreadable |
| Size | `Backend.Size` | Exact byte count |
| Integrity | session-scoped verification result | `not verified` until verified |
| Written | filesystem object modification time | Physical metadata rendered as whole `m ago`/`h ago`/`d ago`; not object creation time |
| References | optional viewer `ReferenceIndex` | Host-supplied inbound count; `0` when no source supplied; also the source for the `Root`/`Detached` refinement of Reachability below |
| Reachability | optional viewer `ReachabilityIndex` | Host-supplied root reachability; only source for Orphaned |
| Timestamp | filesystem object modification time | Same physical metadata in UTC RFC 3339; not object creation time |

- MUST NOT expose generated reference counts or object ages: viewer-design.md §3.
- MUST NOT expose a digest-derived byte preview.
- The `References` axis, its host-supplied source, and digest sort:
  viewer-design.md §3.
- One status cell, two pills: integrity (`not-verified` / `verified` / `corrupt`)
  and reference state (`Root` reachable no-inbound, `Orphaned`, `Detached`
  unreachable no-inbound).
- Interior reachable objects (reachable, inbound > 0): integrity pill only.
- Names, colors, orphan/detached Verify, check time, store-wide sweep,
  failed-action prose: viewer-design.md §3.

## 2. URL state

One canonical state representation:

```text
/viewer/objects?q={text}&type={type}&size={bucket}&status={integrity}
  &sort={hash|type|size|status|written}&dir={asc|desc}&limit={25|50|100|250}
  &offset={non-negative}&selected={digest}&tab={metadata|references|bytes|actions}
```

| Key | Default | Valid values | Effect |
|---|---|---|---|
| `q` | empty | trimmed text | Case-insensitive digest/type substring |
| `type` | empty | type present in the current result domain | Exact envelope type |
| `size` | empty | `small`, `medium`, `large` | `<1 KiB`, `1 KiB–1 MiB`, `>1 MiB` |
| `status` | empty | `not-verified`, `verified`, `corrupt` | Integrity axis; exclusive states, empty means every state |
| `reach` | empty | `reachable`, `orphaned` | Reachability axis; ANDs with `status` and requires configured reachability |
| `sort` | `hash` | `hash`, `type`, `size`, `status`, `written` | Primary order |
| `dir` | `asc` | `asc`, `desc` | Sort direction |
| `limit` | `25` | `25`, `50`, `100`, `250` | Maximum rows per response |
| `offset` | `0` | non-negative integer | First result position |
| `selected` | first matched row | valid listed digest, or empty to deselect | Inspector target |
| `tab` | `metadata` | `metadata`, `references`, `bytes`, `actions` | Inspector panel |
| `nav` | absent | `ref`, `trail` | How the selection was reached; decides whether the trail is extended, stepped, or restarted |

- Malformed values → HTTP 400; MUST NOT silently substitute a different enum,
  limit, offset, or digest (viewer-design.md §5).
- Valid offset beyond the matched set → empty result page with a valid pager
  state.

## 3. Derivation pipeline

Each `GET /viewer/objects` response applies these deterministic steps:

1. Parse and validate URL state.
2. List digests, form normalized records, include host reachability when
   configured.
3. Apply `q`, `type`, `size`, `status` filters.
4. Sort by requested key/direction: digest/type lexical, size exact integer
   bytes, written backend modification time.
5. Calculate matched count and total matched bytes.
6. `offset` absent and `selected` matched → offset set to the page holding that
   record; an explicit `offset` (every pager link supplies one) always wins.
7. Take the offset/limit slice.
8. Resolve `selected`: keep only if matched; absent → first row of the page;
   present but empty → explicit deselect, empty inspector rendered.
9. Render object-list (table, result summary, pager) and inspector independently
   from the selected normalized record.

- Visit trail: session-scoped, backs the inspector's `‹`/`›`; `trail=1` moves the
  cursor without extending it; any other selection truncates forward entries.
- Trail capped, never storage (viewer-design.md §5).
- Bytes inspector, truncation note, result summary: viewer-design.md §3.

## 4. State transitions

| Trigger | State update | Response |
|---|---|---|
| Search/type/size/status filter | Set filter; `offset=0`; preserve legal sort/limit/tab | Object-list fragment |
| Sort header | Toggle `dir` for same `sort`; else set sort and `asc`; `offset=0` | Object-list fragment |
| Page-size control | Set `limit`; `offset=0` | Object-list fragment |
| First/previous/next/last pager link | Set bounded offset from matched count | Object-list fragment |
| Object-row link | Set `selected`, or clear it when the row is already selected; retain legal list state | Inspector fragment or full detail |
| Reference link | Set `selected` to the referenced digest; page the list to its row | Inspector and object-list fragments |
| Inspector `‹`/`›` | Move the session trail cursor; set `selected` and `trail=1`; inert when the trail is exhausted | Inspector and object-list fragments |
| Inspector panel link | Set `tab`; retain selection/list state and selected-row highlight | Inspector fragment |
| Verify form | Recompute selected object with injected hasher; store result only in server session | Integrity fragment |
| Delete form | Perform existing authorized mutation; audit log | Existing result fragment |

- Controls are ordinary GET links/forms; htmx adds `hx-get`/`hx-target`/
  `hx-push-url`; the plain URL reconstructs the same state
  (frontend-architecture.md §4).

## 5. Rendering boundaries

### 5.1 Composition tree

Pages assemble documents; components own one responsibility, reused by full pages
and htmx fragments (viewer-design.md §4).

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
            │   ├── references-panel
            │   ├── bytes-panel
            │   │   └── hexdump-table
            │   └── actions-panel
            └── integrity
```

- `object-list` and `object-inspector` are the only independently swappable
  workspace components.
- A child MUST NOT issue a duplicate request for data owned by its parent.
- Each component receives a typed, pre-shaped view model, never request state or
  storage.

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

- Direct request → complete document; `HX-Request: true` → only the named
  component for its target; one component set produces both
  (frontend-architecture.md §3).

## 6. Prototype adaptation

| Mockup behavior | Server-rendered implementation |
|---|---|
| In-memory 512-object collection | Backend list for each request |
| Mutable JavaScript selection | `selected` URL state |
| JavaScript pagination clamp | Valid offset preserved; empty page explicit |
| Client tab keyboard model | Panel links with standard focus/navigation |
| Draggable/keyboard splitter | CSS `resize` bounded by `min-width`/`max-width`; no keyboard resize |
| Clipboard copy | Readonly full-digest field as a single selection target |
| Reference history and graph | Session-scoped `‹`/`›` visit trail; no graph view |
| Verify all | Excluded pending an authorized bounded server operation |
| Digest-derived hexdump | Actual bounded object bytes |

## 7. Invariants

- GET side-effect free (frontend-architecture.md §4).
- Every URL state validated before storage access.
- No response fabricates backend facts.
- Table, result count, and pager derive from one filtered/sorted sequence.
- A selected object is never rendered when absent from the matched set.
- A missing selection renders explicit empty inspector content; the first-row
  default never overrides an explicit deselection.
- 401/403 behavior, roles, CSRF, audit logging: `viewer-security.md`.
- Raw bytes bounded by the existing 256-byte preview limit.

## 8. Verification matrix

| Requirement | Test |
|---|---|
| URL defaults/invalid values | Table-driven handler tests for every state key |
| Filter/sort pipeline | Seeded backend with known envelope types/sizes |
| Pagination | First/middle/last/empty offset and query-retention cases |
| Progressive enhancement | Links/forms return complete documents without htmx |
| Fragment boundaries | htmx requests return only named list/inspector fragments |
| Selection/panels | Direct URLs and htmx swaps preserve legal state |
| Integrity | Session result scoped; verified/corrupt states truthful |
| Accessibility | Headers, labels, sort state, selected row, empty state |
