---
type: Design
title: Viewer Mockup Parity Audit — go-cask
description: Visual and behavioral comparison of the object-browser mockup and server-rendered viewer.
version: v1
---

# Viewer Mockup Parity Audit — go-cask

This audit compares [`go-cask-viewer.html`](go-cask-viewer.html) with the
server-rendered viewer. The mockup is a visual reference; normative constraints
remain in [`../specs/viewer-design.md`](../specs/viewer-design.md).

## 1. Parity outcomes

| Area | Status | Server-rendered implementation |
|---|---|---|
| Top and filter bars | Matched | 46px and 47px rows, shared surface/border tokens, compact controls, search glyph, and reset action. |
| Desktop workspace | Matched | Fixed-height list, 4px divider, 440px inspector, and pane-local scrolling. |
| Table geometry | Matched | Fixed layout, 30% digest column, dense mono rows, sticky header, selection inset, hover, and scroll surface. |
| Pager | Matched | Pinned footer, count, row-size control, and compact navigation controls. |
| Integrity tags | Matched | Compact semantic pills with truthful session-scoped states. |
| Empty inspector | Matched | Explicit empty selection message with no invented values. |
| Inspector tabs and bytes | Matched | URL-addressable panels and lazy server-rendered hexdump. |
| Search/filter/sort/page | Matched | URL state plus htmx enhancement and full HTML fallback. |

## 2. Intentional differences

| Mockup feature | Decision | Reason |
|---|---|---|
| Reference-count column | Excluded | Byte-layer backend has no truthful reference count. |
| Written-time column | Excluded | Backend does not expose persisted object creation time. |
| References tab and rows | Excluded | Viewer does not resolve typed object graphs. |
| Copy button | Excluded | Clipboard access needs client JavaScript. |
| Resizable splitter | Excluded | Persistent sizing needs client state or custom JavaScript. |
| Global Verify button | Excluded | Global verification needs an authorized bounded server operation. |
| Mockup’s generated data | Replaced | `cask seed-preview` creates valid stored envelopes instead of browser-only data. |

## 3. Verification

- Start a preview store with `cask -store {path} seed-preview`.
- Open `cask web` against that store and sign in.
- Confirm desktop workspace has 500 objects, 25 visible rows, pinned pager,
  local table scroll, and an empty inspector before selection.
- Select an object; confirm the inspector uses only digest, envelope type,
  exact stored size, session integrity status, and actual bounded bytes.
