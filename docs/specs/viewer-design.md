---
type: Specification
title: Viewer Design — go-cask
description: Design of the embedded technical viewer — a styled, server-rendered master-detail object browser composed from Go templates, scoped CSS, and htmx-only interaction.
version: v45
---

# Viewer Design — go-cask

Embedded technical browser UI in `internal/web/` — dense, desktop-first object browser for developers
and operators.

- Defines: screens, visual system, template composition, hypermedia interactions.
- Read with `viewer-security.md`, `frontend-architecture.md`, `coding-guidelines.md`, `api-design.md`.
- Visual reference: [`docs/design/go-cask-viewer.html`](../design/go-cask-viewer.html) — prototype-only
  JavaScript, MUST NOT ship.
- Reading order: this file **defines** the viewer; [package README](../../internal/web/README.md) maps
  code to these contracts; [CLI README](../../cmd/cask/README.md) **explains** the result to the
  `cask web` operator, including the §3 reference states.

## 1. Purpose and boundaries

- **Persona:** developer/operator — what is stored, how large, is a selected object intact?
- **Byte layer:** objects, envelope types, exact sizes, bytes, on-demand integrity results. MUST NOT
  resolve typed references or import application object models.
- **Identity:** raw lowercase-hex digest. List: `Digest.Prefix(8)` plus `…`; inspector: full digest in
  a readonly field — one selection target, no clipboard script. `sha256` may show in metadata; MUST NOT
  prefix a displayed digest.
- **Landing:** sole operational workspace and viewer landing at `/viewer/`.
- **Loose only:** filesystem backend; size and modification time from each object's own file.
  `cask web -backend packfs` refused with `cas.ErrUnsupported` plus the remedy (open a loose store)
  [loose and pack answers differ]. Pack parity possible, not present (cli.md §1, §2; go-cask#189).
- **Out of scope:** mutable editing, uploads, buckets, charting, JSON APIs, browser storage,
  client-side application state, custom JavaScript. htmx is the only script shipped.

## 2. Visual system

- MUST use a flat VS Code-style workbench hierarchy through `internal/web/viewer.css` — near-white
  panel/header, dark foreground, muted metadata, hairline borders, one blue accent (`#007acc`), system
  body font, monospace hashes/numbers/bytes.
- Panels MUST NOT use shadows, gradients, elevation, card-like decoration.
- `viewer.css` only viewer stylesheet (coding-guidelines §4).
- Colors, font stack, spacing, radii, status-tag colors, row hover/selection tints, focus indicators
  MUST follow [`go-cask-object-browser.design.json`](../design/go-cask-object-browser.design.json).
- CSS appearance only; every control, value, state label, focusable target MUST remain semantic HTML.
- New gray values: component-specific contrast justification.
- 26px buttons, 28px inputs/selects, 0–2px radii; inspector metadata an 88px label column plus an 8px
  value gap; active tab a 1px blue bottom indicator only.
- Hover subtle, non-animated apart from short background/border transitions; selection muted blue, no
  text inversion.
- Monospace stack: hashes, identifiers, algorithm values, hexadecimal content, byte views.
- Interactive controls MUST size from the three-step control type scale, never the body font:
  `--viewer-control` 28px form controls, `--viewer-control-sm` compact 22–28px, `--viewer-control-xs`
  icon-sized.
- Status pills: faint outline from their own text colour (`currentColor`), one hue per state;
  transparent at rest, visible only through the state fill.
- Control font reset MUST stay at zero specificity (`:where(.viewer-shell) button, …`) [specificity
  would override the declared size].

| Token/metric | Contract |
|---|---|
| Top bar | 36px; `CA` mark, `go-cask` wordmark, build version as secondary text |
| Filter bar | 36px; search, type, size, integrity filters plus reset |
| Main workspace | flexible object-list column plus 440px inspector column; inspector resizes natively through CSS |
| Inspector bounds | 280px–560px visual range; fixed 440px default |
| Object table | fixed 26px dense mono rows; sticky 12px/600 muted header; content-sized digest/size/inbound/integrity/references/written columns; type fills remaining width |
| Controls | one 28px height across form controls, actions, pager; compact bordered pager/action controls; icon-sized history arrows stay 22px |
| Narrow view | at ≤900px document scrolls; list precedes full-width inspector; filters scroll horizontally |

| Neutral surface | Value |
|---|---|
| Workspace surface | white |
| Panel/header and quiet hover | `#f3f3f3` |
| Workspace/disabled surface | `#f8f8f8` |
| Divider/disabled border | `#e5e5e5` |
| Control border | `#c8c8c8` |
| Secondary control border/scrollbar hover | `#999999` |
| Pressed state | `#dddddd` |

## 3. Pages and visible data

| Route | View | Role |
|---|---|---|
| `/viewer/` | completes the `?token=` deep link, redirects to login without a session, else the object browser | viewer |
| `/viewer/objects` | the object browser — target of every filter, sort, page, and selection control | viewer |
| `/viewer/objects/{hash}` | cold-load object link: 303 redirect to the browser with that object selected | viewer |
| `/viewer/objects/{hash}/dump` | lazy hexdump fragment (HTML, not the stored bytes) | viewer |
| `POST /viewer/objects/{hash}/verify` | verifies one object; answers the result fragment | operator |
| `POST /viewer/objects/verify` | verifies every stored object; answers the summary fragment | operator |
| `/viewer/login` | login page plus token submission; a rejected attempt → `401` (empty body); the page states the reason when shown again | public |
| `/viewer/static/{viewer.css,htmx.min.js}` | the viewer's only two assets, served from its own origin | public |

- **Whole surface:** the table above; every other `/viewer/` path hits one catch-all naming no method.
  Unserved paths — object-delete and GC routes included — reply on the session, not the path: 401
  without one, 404 with.
- **Layout:** top bar, filter bar, table/pager master column, inspector detail column.
- **Table columns:** digest, type, envelope version, codec, IEC-formatted size, optional
  inbound-reference count, integrity status, optional truthful backend metadata. MUST NOT fabricate
  reference counts, object age, incoming/outgoing references, stored verification state.
- `not verified` holds until an on-demand result exists in the current server session.
- Verification (success or failure) MUST refresh the table through an htmx response event; the status
  cell reflects the session-scoped result immediately.
- **Header census reported, never inferred:** version/codec cells and inspector Identity rows come from
  one header read per object — no extra payload byte; matches `cask list -json`/`cask meta -json`
  digest by digest (cli.md §3).
- No codec identity in the frame: MUST read `unspecified`, in the cell and in the `codec` filter; never
  blank, never a guessed tag.
- No walkable envelope header (raw object from `put`): MUST read the viewer's not-read marker for both
  fields.
- A codec difference is a *format* fact, not damage: MUST NOT be labelled corrupt by the integrity
  axis, which stays address-based (cas-core §4.8).
- **Digest URLs** algorithm-agnostic: `Server` takes a `cas.Hasher`; routes parse canonical hex with
  `cas.ParseDigest`, validating the width. CLI supplies SHA-256 by default; another client may inject a
  compatible hasher without changing viewer routes.
- **Recorded verification** MUST carry its run time; the Metadata tab MUST restate the finding (state,
  explanation, mismatch digests, check time) on every selection, not only the click's response.
- Reported time MUST be the age at render time, not a label frozen at check time.
- Unchecked this session: no finding; the row states the operator has not clicked.
- **Top-bar Verify** MUST cover every stored object in one request, recording each result in the
  session and refreshing the table through the same status event. MUST NOT report the resulting counts
  (§5) — the per-object status cells carry them.
- The inspector MUST subscribe to that status event and re-render alongside the table. The sweep is
  audited as one event with counts.
- **Sweep bounded:** one at a time; a session may start `expensiveBurst`, then one per cooldown
  (numbers: defaults.md).
- Over bound: MUST answer `429` with `Retry-After` plus a fragment stating the wait (the control's
  label carries it); MUST NOT queue behind the running sweep; MUST NOT emit the status event.
- The metadata snapshot shares the session budget: a refused rebuild MUST serve the last published
  snapshot — stale, not wrong, at most one cooldown.
- `Retry-After` follows the login throttle's refusal rule (viewer-security §5).
- **Version:** the top bar MUST render the build's module version beside the wordmark, from build info
  — a pseudo-version until the first tag, `dev` with no module version (versioning §2). The CLI's
  `version` subcommand MUST print the same string from that source.
- **Failure** MUST render as structured prose, never a raw Go error string; classify with the `cas`
  sentinel errors — a state pill, a plain-language explanation, and for a digest mismatch both the
  expected address and the digest the stored bytes hash to.
- An unclassified failure renders the viewer's own sentence; the underlying error MUST NOT appear in
  the response [wrapped errors carry absolute paths]. It goes to the audit line (viewer-security §9).
- **Login page:** the same rule. Rejected token → `401`, empty body; the page, shown again, states in
  one owned sentence why. Never describes the token or the account (api-design §5–§6).
- **Filter axes:** single-choice each; empty means any.

| Control | Values | Notes |
|---|---|---|
| `status` | Verified, Unverified, Corrupt | integrity axis, exclusive alternatives; empty means every state |
| `type` | the versioned envelope type | header axis |
| `version` | the frame's leading byte, as its decimal form | header axis |
| `codec` | the identity tag; the literal `unspecified` names frames carrying none | header axis |
| `reach` | `reachable`/`orphaned`/`detached`/`root` | reachability axis; MUST be separate from the header axes |

- Unknown value the store does not hold — unknown type, absent version, unknown codec → 400, not an
  empty page; bytes with no walkable header (version 0) match no version and no codec.
- Axes combine with AND: `status=corrupt&reach=orphaned` returns corrupt orphans only. Mixing axes in
  one control is forbidden.
- `detached`: an orphaned object with zero host-supplied inbound references — a disconnected component
  entry, not a retention root.
- `root`: a reachable object with zero host-supplied inbound references — a reachable-subtree entry
  point, consistent with a root but not an assertion of the host's root list (the viewer sees only the
  two independent indexes below).
- A host MAY supply a `ReachabilityIndex` (root-based graph reachability); only then do the
  reachability filter, the `References` column, and orphaned labelling appear. MUST NOT infer
  orphanhood from zero inbound references.
- The viewer MAY label a zero-reference orphan `Detached` and a zero-reference reachable object `Root`,
  only with a host `ReferenceIndex`; without it `reach=detached` or `reach=root` returns 400, and
  without a reachability source every `reach` query returns 400.
- **Orthogonal axes:** byte integrity and root reachability MUST stay independent facts in storage and
  display, names included. `Status` is not a label either axis may use [two verdicts, not one].
- Columns: `Integrity` (Unverified, Verified, Corrupt) and `References` (Resolved, Orphaned, Detached,
  Root); the inspector names the same states.
- Pills: `Detached` muted-violet, `Root` blue, Resolved green, Orphaned amber.
- `Resolved` not `Reachable` [a root is reachable by definition]; `Root` is that state's zero-inbound
  case, so the pills never overlap on one object.
- Neither axis may be collapsed into or suppressed by the other [hides a corrupt orphan's integrity].
- The `Detached` and `Root` filter choices additionally require a `ReferenceIndex`.
- The inbound-reference count is named `Inbound`, so it never collides with them.
- Each filter matches its own axis: a corrupt orphan returns for both `status=corrupt` and
  `reach=orphaned`.
- Verification MUST remain available for orphaned objects [orphans rot unnoticed; GC reclaims them].
- The inspector and object table label the inbound-reference count `References` — source objects
  reported by `Inbound` for the selected digest. Without a viewer `ReferenceIndex` the viewer renders
  zero and MUST NOT scan or infer arbitrary raw payloads.
- **Written/Timestamp:** the filesystem backend may expose its physical modification time as `Written`
  and `Timestamp`. `Written`: minutes below one hour, hours below one day, days thereafter (`59m ago`,
  `3h ago`, `2d ago`); `Timestamp`: the same value in UTC RFC 3339. Neither is an immutable logical
  creation timestamp.
- **Inspector:** a summary header and server-selected panels.

| Panel | Contents |
|---|---|
| Metadata | full digest, client algorithm, envelope type, exact size in its Storage section, physical written/timestamp metadata when available, and a State section carrying the integrity result with its last check time, the reachability verdict, and the inbound-reference count |
| Bytes | lazy 16-byte-row hexdump of at most the first 256 bytes; truncation note when more bytes exist |
| References | with a complete host-supplied viewer `ReferenceIndex`, deterministic digest-sorted inbound (**By**) and outbound (**Out**) edges; each edge a selectable digest link showing its stored envelope type when readable (the `@1` suffix omitted in its compact pill); without an index, empty By and Out states |

- The inbound-reference count sits beside the verdict it qualifies; the Metadata section ends with the
  role-gated verify form; forms stay ordinary, CSRF-protected POSTs; no redundant cold-detail link; no
  separate Actions tab.
- **IEC units:** byte quantities outside Metadata → Storage → Size use base-1024 `B`, `KiB`, `MiB`,
  `GiB`, `TiB` — object table, pager totals, truncation notes, cold-detail size. The selected object's
  Storage size stays its exact integer byte count.
- **Copy-to-clipboard:** prototype behavior, not a viewer feature [the clipboard needs a script].
  Inspector history is server-side — the session trail above, not browser state.
- The inspector resizes through the CSS `resize` property, bounded 280–560px by `min-width`/`max-width`;
  width is visual state only, resetting on reload.

## 4. Rendering architecture and composition

- **Rendering:** `html/template` from `embed.FS`; named templates execute into a buffer before the
  response.
- `shell` is the only template that MAY emit document structure: document type, language, head, body,
  and one page content component through a typed shell view model.
- Page content components MUST NOT emit document chrome.
- Full pages compose these components; fragments execute the same named components standalone.
- A component MUST receive pre-shaped data: templates may range and branch but MUST NOT calculate
  filtering, sorting, pagination, integrity, or layout.
- Inventory and composition tree:
  [`../design/viewer-template-index.md`](../design/viewer-template-index.md).

| Component | Responsibility |
|---|---|
| `shell` | document shell only; selects one page content component |
| `head` | metadata, `/viewer/static/viewer.css`, vendored htmx |
| `top-bar` | brand, build version |
| `*-content` | page-specific composition without document chrome |
| `filter-bar` | one GET form for durable browser state |
| `object-table` | accessible headers, rows, empty state |
| `pager` | result summary plus first/previous/next/last links |
| `object-list` | table and pager; the primary htmx swap boundary |
| `inspector` | selected-object header and metadata/bytes/actions panels |
| `integrity` | verify result fragment |
| `hexdump-table` | lazy bytes fragment |
| `result` | mutation outcome; `result-swap` adds the out-of-band status refresh for action responses |

## 5. URL state and htmx interactions

- The object-browser URL owns all view state.

```text
/viewer/objects?q=<text>&type=<type>&size=<bucket>&status=<state>
  &sort=<hash|type|version|codec|size|inbound|status|reach|written>&dir=<asc|desc>
  &limit=<1..250>&offset=<non-negative>&selected=<digest>
  &tab=<metadata|references|bytes>&nav=<ref|trail|stay>
```

- Defaults: hash ascending, `limit=25`, `offset=0`, no filters, metadata panel.
- Every parameter validated: invalid enumerations, malformed selected digests, negative/non-numeric
  offsets → 400; handlers MUST NOT silently correct invalid input.
- `limit` excepted [a preference, not a store claim]: an out-of-set numeric limit is clamped into
  range, and the Rows control MUST name the size in effect.
- Filtering or changing `limit` zeroes `offset`; sorting preserves filters and selection only while the
  selected digest remains in the filtered result set.
- Every data column sorts, both reference columns included.
- The server filters, sorts, counts, slices, renders: `#object-list` (table plus pager) to htmx
  requests, the whole document otherwise.
- Search: `hx-get`, `input changed delay:300ms`, `hx-include` of the filter form,
  `hx-target="#object-list"`, `hx-push-url="true"`.
- Sort headers and pager are ordinary links carrying complete query state; htmx may enhance them with
  `hx-get`/`hx-target`/`hx-push-url`, never replacing link behavior.
- Selecting a row is a real link setting `selected`; htmx may request the same URL and swap
  `#object-inspector`, a direct request rendering the complete object-detail document.
- Selection defaults render, never navigate: no selection carried — the first visible row backs the
  inspector; a filter-dropped selection falls back the same way.
- A list swap MUST carry the inspector out of band; the embedded refresher URL MUST name the fallback,
  not the dropped digest.
- An explicitly empty `selected=` is a deselect and MUST render the empty inspector; the selected row's
  link points at it.
- An off-page selection pages the table to it; an explicit `offset` is a page request and MUST win over
  that jump.
- Inspector header `‹`/`›` controls step through the session's reference chain; a selection carries a
  `nav` marker.

| `nav` marker | Meaning |
|---|---|
| `nav=ref` | reference link: appends to the trail, dropping abandoned forward entries |
| `nav=trail` | a `‹`/`›` step: moves the cursor only, MUST NOT extend the trail |
| absent | table row, filter, pager, typed URL: restarts the trail there (a table pick is a new departure, not a step in the chain that led there) |
| `nav=stay` | tab switch: re-renders the same selection, MUST leave the trail untouched |

- An exhausted direction renders as inert, dimmed text, not a dead link. `nav` describes a single
  click, so URLs built from that state MUST NOT carry it. The trail is session state, never persisted.
- Inspector panel links set `tab`; no browser-only tab state. The panel switchers are navigation links
  marked `aria-current="page"`, not an ARIA tab widget [`role="tablist"`/`role="tab"` promise arrow-key
  roving and a linked `tabpanel`, needing the forbidden JavaScript].
- The tab lives in the URL, so row and reference links beneath it MUST carry the open tab, and a tab
  switch MUST re-render the object list out of band so those links adopt it. The bytes panel
  lazy-loads the hexdump via `hx-trigger="revealed"`.
- Verify stays POST + CSRF + role checks + audit logging, the inspector's only action. Deleting an
  object is a CLI store-lifecycle operation [scriptable, auditable, paired with the roots a sweep
  needs], so the viewer MUST NOT expose a delete route or control.
- Verify swaps only `#integrity`; that target is narrow, so verify MUST also swap the inspector's
  integrity state out of band, and MUST NOT report the sweep counts the status cells carry. The
  out-of-band swap MUST live in a wrapper template used only by action responses [inline use leaks
  stray markup].
- A refresh trigger MUST live inside the fragment it refreshes [on the wrapper an `innerHTML` swap
  leaves its URL untouched, replaying first-render filters]. The object list and the inspector each
  carry a trigger; one shared URL serves both, the response selected by the request's `HX-Target`.

## 6. Security and accessibility

- `viewer-security.md` applies unchanged: protected routes require a session; 401/403 have empty
  bodies; mutations are role-gated, CSRF-protected, audited; no token or secret reaches markup, logs,
  or browser state.
- Tables require captions, scoped headers, `aria-sort` for the active sort column, an explicit empty
  row; every form control has a label.
- The selected row exposes its state; status labels contain text rather than color alone; all
  actions/links remain keyboard-operable.
- Focus styles in `viewer.css` MUST meet the mockup's visible focus-ring contract.

## 7. Verification requirements

Viewer tests MUST cover:

- full-page and htmx rendering of each named component;
- URL defaults, valid filter/sort/page combinations, and invalid query 400s;
- pagination totals, boundaries, and pager links retaining query state;
- direct-link and htmx selection behavior;
- accessible table/sort/status markup and CSS asset headers;
- session/role/CSRF/error behavior and bounded raw-byte preview;
- response hygiene: `Retry-After` on a throttled login and on a refused expensive operation,
  `Cache-Control: no-store` (and `Vary: Cookie`) on every response, empty rejection bodies, and no
  response body carrying a Go error string or a filesystem path.

## 8. Checklist

- [x] Master-detail browser uses server-rendered HTML, htmx, and one scoped stylesheet
- [x] Component templates compose pages/fragments without duplicated rendering logic
- [x] Filter/sort/page/selection/panel state is URL-addressable and validated
- [x] Pagination is server-side and progressively enhanced
- [x] No browser storage, typed graph, or invented metadata; htmx is the only script
- [x] Security and accessibility requirements remain enforced, including response headers and error prose
