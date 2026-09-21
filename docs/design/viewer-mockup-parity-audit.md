---
type: Design
title: Viewer Mockup Parity Audit — go-cask
description: Visual and behavioral comparison of the object-browser mockup and server-rendered viewer.
version: v5
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
| Inbound reference count | Supported when indexed | Inspector and table expose host-supplied inbound counts; raw-only stores show zero. |
| Written-time column | Supported | Filesystem modification time is rendered as mockup-style elapsed `h ago`/`d ago` metadata; it is not persisted object creation time. |
| Timestamp metadata | Supported | Same filesystem modification time is exposed in UTC RFC 3339 for exact inspection. |
| References tab and rows | Supported when indexed | Viewer renders digest-sorted By/Out edges from a host-supplied reference source; raw-only stores show empty states. |
| Copy button | Excluded | Clipboard access needs client JavaScript. |
| Resizable splitter | Supported | Pointer and keyboard resize is bounded to 280px–560px and resets on reload. |
| Global Verify button | Excluded | Global verification needs an authorized bounded server operation. |
| Mockup’s generated data | Replaced | `cask seed-preview` creates valid stored envelopes instead of browser-only data. |

## 3. Verification

- Start a preview store with `cask -store {path} seed-preview`.
- Open `cask web` against that store and sign in.
- Confirm desktop workspace has 500 objects, 25 visible rows, pinned pager,
  local table scroll, and an empty inspector before selection.
- Select an object; confirm the inspector uses only digest, envelope type,
  exact stored size, session integrity status, and actual bounded bytes.
