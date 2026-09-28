---
type: Design
title: Viewer Template Index — go-cask
description: Concrete component relationships and composition rules for the embedded viewer templates.
version: v5
---

# Viewer Template Index — go-cask

Map of viewer templates to component responsibilities and composition
relationships; implementation guide,
[`../specs/viewer-design.md`](../specs/viewer-design.md) remains normative.

## 1. Ownership rules

- `shell` alone owns document structure: document type, language, head, body.
- A page content component MUST NOT emit document, head, or body structure.
- Component: one semantic responsibility, pre-shaped view model only, no request
  state or storage.
- Owned by viewer-design.md §4, frontend-architecture.md §3.

## 2. Template inventory

| File | Template | Kind | Responsibility |
|---|---|---|---|
| `partials.html` | `shell` | global shell | Document chrome; selects one page content component. |
| `partials.html` | `head` | shell child | Metadata, local stylesheet, vendored htmx, divider script. |
| `partials.html` | `top-bar` | shell child | Brand and global navigation. |
| `objects.html` | `objects-content` | page | Filter bar and object browser workspace. |
| `objects.html` | `object-browser` | composite | List and inspector columns. |
| `objects.html` | `object-list` | fragment boundary | Owns `#object-list`; composes table fragment. |
| `objects.html` | `object-table-fragment` | fragment | Table, empty state, pager. |
| `object.html` | `object-content` | page | Detailed-object workspace. |
| `object.html` | `object-detail-backlink` | leaf | Return navigation. |
| `object.html` | `object-detail-header` | leaf | Object heading and full digest. |
| `object.html` | `object-detail-metadata` | leaf | Detailed metadata. |
| `object.html` | `object-detail-actions` | leaf | Role-gated forms and action result. |
| `object.html` | `object-detail-bytes` | leaf | Lazy bytes boundary. |
| `gc.html` | `gc-content` | page | Maintenance form. |
| `gc.html` | `gc-form` | leaf | GC mutation form and result boundary. |
| `login.html` | `login-content` | page | Login form without global navigation. |
| `login.html` | `login-form` | leaf | Token form. |
| `partials.html` | `filter-bar` | composite | Durable object-browser filters. |
| `partials.html` | `object-table` | composite | Accessible headers and object rows. |
| `partials.html` | `object-row` | leaf | One normalized object row. |
| `partials.html` | `pager` | leaf | Result range and page navigation. |
| `partials.html` | `object-inspector` | fragment boundary | Selected-object header and metadata, references, bytes, or action panel. |
| `partials.html` | `hexdump-table` | fragment | Bounded raw-byte view. |
| `partials.html` | `result` | fragment | Mutation outcome. |

## 3. Composition tree

```text
shell
├── head
├── top-bar, except login
└── selected page content
    ├── objects-content
    │   ├── filter-bar
    │   └── object-browser
    │       ├── object-list
    │       │   └── object-table-fragment
    │       │       ├── object-table
    │       │       │   └── object-row
    │       │       └── pager
    │       └── object-inspector
    │           └── hexdump-table, only for bytes panel
    ├── object-content
    │   ├── object-detail-backlink
    │   ├── object-detail-header
    │   ├── object-detail-metadata
    │   ├── object-detail-actions
    │   └── object-detail-bytes
    │       └── hexdump-table
    ├── gc-content
    │   └── gc-form
    └── login-content
        └── login-form
```

## 4. Rendering paths

- `Server.renderPage` wraps each full-page data model in `shellData`, then
  executes `shell`.
- `shellData.View` selects exactly one page content component.
- `shellData.Data` passes unchanged to that component.
- The server executes components directly only for htmx fragments:

| Route behavior | Template |
|---|---|
| Object-list filter, sort, or paging | `object-table-fragment` |
| Object selection or inspector tab | `object-inspector` |
| Raw bytes | `hexdump` then `hexdump-table` |
| Verify, delete, or GC result | `result` |

## 5. Change procedure

1. Change a leaf component before its parent composite.
2. Page content component thin: compose children, never duplicate child markup.
3. Add a new `shell` branch only for a new full page; never a second document
   shell.
4. Preserve fragment targets and progressive full-page links/forms.
5. Extend focused viewer tests for changed composition or fragment boundaries.
