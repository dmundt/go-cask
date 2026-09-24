---
type: Design
title: Viewer Template Index — go-cask
description: Concrete component relationships and composition rules for the embedded viewer templates.
version: v4
---

# Viewer Template Index — go-cask

Maps the viewer's named Go templates to their component responsibilities and
composition relationships. Implementation guide;
[`../specs/viewer-design.md`](../specs/viewer-design.md) remains normative.

## 1. Ownership rules

- `shell` is the only template that owns document structure: document type,
  language, head, and body.
- A page template is a content component. It MUST NOT emit document, head, or
  body structure.
- A component renders one semantic responsibility and accepts only its
  pre-shaped view model. Components MUST NOT query request state or storage.
- Fragments reuse components already composed by a full page. A fragment MUST
  NOT recreate shell structure.

## 2. Template inventory

| File | Template | Kind | Responsibility |
|---|---|---|---|
| `partials.html` | `shell` | global shell | Owns document chrome and selects one page content component. |
| `partials.html` | `head` | shell child | Metadata, local stylesheet, vendored htmx, and divider script. |
| `partials.html` | `top-bar` | shell child | Brand and global navigation. |
| `objects.html` | `objects-content` | page | Composes filter bar and object browser workspace. |
| `objects.html` | `object-browser` | composite | Composes list and inspector columns. |
| `objects.html` | `object-list` | fragment boundary | Owns `#object-list` and composes table fragment. |
| `objects.html` | `object-table-fragment` | fragment | Composes table, empty state, and pager. |
| `object.html` | `object-content` | page | Composes detailed-object workspace. |
| `object.html` | `object-detail-backlink` | leaf | Return navigation. |
| `object.html` | `object-detail-header` | leaf | Object heading and full digest. |
| `object.html` | `object-detail-metadata` | leaf | Detailed metadata. |
| `object.html` | `object-detail-actions` | leaf | Role-gated forms and action result. |
| `object.html` | `object-detail-bytes` | leaf | Lazy bytes boundary. |
| `gc.html` | `gc-content` | page | Composes maintenance form. |
| `gc.html` | `gc-form` | leaf | GC mutation form and result boundary. |
| `login.html` | `login-content` | page | Composes login form without global navigation. |
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

`Server.renderPage` wraps each full-page data model in `shellData`, then
executes `shell`. `shellData.View` selects exactly one page content component;
`shellData.Data` is passed unchanged to that component.

The server executes components directly only for htmx fragments:

| Route behavior | Template |
|---|---|
| Object-list filter, sort, or paging | `object-table-fragment` |
| Object selection or inspector tab | `object-inspector` |
| Raw bytes | `hexdump` then `hexdump-table` |
| Verify, delete, or GC result | `result` |

## 5. Change procedure

1. Add or change a leaf component before changing its parent composite.
2. Keep a page content component thin: compose children, never duplicate child
   markup.
3. Add a new `shell` branch only for a new full page. Do not add another
   document shell.
4. Preserve fragment targets and progressive full-page links/forms.
5. Extend focused viewer tests for changed composition or fragment boundaries.
