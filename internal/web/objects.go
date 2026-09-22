// The object browser's rows and handlers: what one stored object looks like in
// the table and the inspector, and the requests that render them. The query
// state those requests are read into lives in browser.go.

package web

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/internal/index"
)

type objectRow struct {
	// hash is the row's address as the store listed it. Keeping it spares the
	// inspector from parsing the rendered Digest back into a digest.
	hash cas.Digest
	// Digest is the full object digest.
	Digest string
	// Short is the abbreviated digest.
	Short string
	// Type is the decoded object type.
	Type string
	// TypeLabel is what the type cell shows. It differs from Type only when the
	// bytes could not be read: the cell says so instead of rendering an empty
	// cell that reads like an untyped object.
	TypeLabel string
	// Unreadable reports that the object's bytes could not be read.
	Unreadable bool
	// Size is the stored payload size.
	Size int64
	// References is the number of host-indexed inbound references, when known.
	References int
	// ReferencesAvailable reports whether References came from the configured source.
	ReferencesAvailable bool
	// Integrity is the session-scoped integrity result, independent of reachability.
	Integrity string
	// IntegrityLabel is the human-readable integrity result.
	IntegrityLabel string
	// Orphaned reports that no configured root reaches this object.
	Orphaned bool
	// Detached reports that an orphaned object has no inbound references.
	Detached bool
	// Root reports that a reachable object has no inbound references — the
	// entry point of a reachable subtree, structurally consistent with being
	// a configured root (the viewer has no direct view of the root list, so
	// this is an inferred structural fact, not an assertion about host
	// configuration).
	Root bool
	// ReachabilityKnown reports whether Orphaned was computed at all. The
	// reference column is a row-level decision because the row template only
	// ever sees the row.
	ReachabilityKnown bool
	// Written is physical object write metadata from the filesystem backend.
	Written time.Time
	// WrittenLabel is the formatted physical write time.
	WrittenLabel string
	// Selected reports whether this row backs the visible inspector.
	Selected bool
	// SelectURL opens this row in the browser inspector. The row templates
	// render it through the shared selection-link attributes.
	SelectURL string
}

// --- objects list ---

type objectBrowserData struct {
	// State is the parsed browser request state.
	State objectBrowserState
	// RefreshURL reloads the current browser result.
	RefreshURL string
	// HasReachability reports whether reachability filtering is available.
	HasReachability bool
	// HasDetached reports whether detached-object filtering is available.
	HasDetached bool
	// HasRoot reports whether root-object filtering is available.
	HasRoot bool
	// StatusOptions contains integrity filter choices.
	StatusOptions []filterOption
	// LimitOptions contains page-size choices.
	LimitOptions []filterOption
	// Objects contains rows visible on the current page.
	Objects []objectRow
	// Types contains available type filter choices.
	Types []filterOption
	// HasAny reports whether the store contains any objects.
	HasAny bool
	// Total is the number of indexed objects.
	Total int
	// Matched is the number of objects matching current filters.
	Matched int
	// TotalSize is the aggregate size of matching objects.
	TotalSize int64
	// RangeStart is the one-based first visible row number.
	RangeStart int
	// RangeEnd is the one-based last visible row number.
	RangeEnd int
	// CurrentPage is the one-based page number.
	CurrentPage int
	// PageCount is the number of result pages.
	PageCount int
	// FirstURL navigates to the first page.
	FirstURL string
	// PreviousURL navigates to the preceding page.
	PreviousURL string
	// NextURL navigates to the following page.
	NextURL string
	// LastURL navigates to the final page.
	LastURL string
	// SortColumns is the table header: one entry per rendered column, already
	// resolved into the link, the arrow, and the accessible name it needs.
	SortColumns []sortColumn
	// HasPrevious reports whether a preceding page exists.
	HasPrevious bool
	// HasNext reports whether a following page exists.
	HasNext bool
	// Inspector contains selected-object details.
	Inspector *browserInspector
	// CSRF is the session's CSRF token.
	CSRF string
	// Role is the current session role.
	Role string
}

type filterOption struct {
	// Value is the query-string value.
	Value string
	// Label is the visible choice text.
	Label string
	// Selected reports whether this choice is active.
	Selected bool
}

type browserInspector struct {
	// Digest is the selected object's printable digest.
	Digest string
	// HashAlgorithm identifies the configured hash algorithm.
	HashAlgorithm string
	// Type is the selected object's envelope type.
	Type string
	// Size is the selected object's stored byte count.
	Size int64
	// Integrity is the selected object's integrity state.
	Integrity string
	// IntegrityLabel is the human-readable integrity state.
	IntegrityLabel string
	// Report is the finding of the last check of this object in this session,
	// replayed so the inspector states the outcome and its age on every visit
	// rather than only in the response to the click that produced it. It is nil
	// when the object has not been checked.
	Report *actionOutcome
	// Orphaned reports whether the object is unreachable.
	Orphaned bool
	// Detached reports whether the object is orphaned with no inbound references.
	Detached bool
	// Root reports whether the object is reachable with no inbound references.
	Root bool
	// WrittenLabel is the formatted backend modification time.
	WrittenLabel string
	// InboundReferences counts inbound graph edges.
	InboundReferences int
	// ReferencesAvailable reports whether graph data is available.
	ReferencesAvailable bool
	// Inbound contains objects that refer to this object.
	Inbound []referenceRow
	// Outbound contains objects referred to by this object.
	Outbound []referenceRow
	// Timestamp is the formatted stored timestamp.
	Timestamp string
	// DumpURL opens the byte dump.
	DumpURL string
	// MetadataURL opens the metadata tab.
	MetadataURL string
	// BytesURL opens the bytes tab.
	BytesURL string
	// ReferencesURL opens the references tab.
	ReferencesURL string
	// PrevURL and NextURL step through the objects this session already
	// inspected; an empty one disables that control.
	PrevURL string
	// NextURL advances through the session inspection trail.
	NextURL string
}

type referenceRow struct {
	// Digest is the referenced object's printable digest.
	Digest string
	// Short is the abbreviated digest display.
	Short string
	// Type is the referenced object's envelope type.
	Type string
	// SelectURL opens the referenced object.
	SelectURL string
}

// objects renders the object browser. It is a pipeline: read the request into
// a state, turn the store into the rows that state matches, page and select
// among them, then render whichever part of the page the request asked for.
func (s *Server) objects(w http.ResponseWriter, r *http.Request) {
	state, err := parseObjectBrowserState(r.URL.Query(), s.cfg.Hasher)
	if err != nil {
		http.Error(w, "invalid object browser query", http.StatusBadRequest)
		return
	}
	if state.Reach != "" && s.cfg.Reachability == nil {
		http.Error(w, "reachability filter unavailable", http.StatusBadRequest)
		return
	}
	if state.Reach == "detached" && s.cfg.References == nil {
		http.Error(w, "detached filter unavailable", http.StatusBadRequest)
		return
	}
	if state.Reach == "root" && s.cfg.References == nil {
		http.Error(w, "root filter unavailable", http.StatusBadRequest)
		return
	}
	id := sessionID(r)
	result, err := s.defaultObjectPage(r.Context(), id, state)
	if err != nil {
		// The default path's rows and page are only meaningful when its
		// snapshot walk succeeded, so the error is checked before either is
		// read. A page whose rows were never produced must never reach the
		// selection lookup below.
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	rows, types, typeFound := result.Rows, result.Types, result.TypeFound
	total, matchedSize := result.Total, result.TotalSize
	page := result.Page
	if result.Fast {
		state = result.State
	} else {
		rows, types, typeFound, total, matchedSize, err = s.objectRows(r.Context(), id, state)
		if err != nil {
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		if !typeFound {
			http.Error(w, "invalid object type", http.StatusBadRequest)
			return
		}
		sortObjectRows(rows, state)
		page, state = pageObjects(rows, state)
	}
	// The refresher reloads the result the operator is looking at, so its URL
	// is built from the resolved state rather than from the request: paging
	// and selection decide each other, and once they have, the state names
	// the selection in effect — including one the browser defaulted to — so
	// the refresh reloads exactly this result.
	refreshURL := state.url()
	linkRows(page.Rows, state)
	prepareObjectRows(page.Rows)
	s.recordTrail(id, state)
	matched := len(rows)
	if result.Fast {
		matched = total
	}

	data := objectBrowserData{
		State:           state,
		RefreshURL:      refreshURL,
		HasReachability: s.cfg.Reachability != nil,
		HasDetached:     s.cfg.Reachability != nil && s.cfg.References != nil,
		HasRoot:         s.cfg.Reachability != nil && s.cfg.References != nil,
		StatusOptions:   statusOptions(state.Status),
		LimitOptions:    limitOptions(state.Limit),
		Types:           typeOptions(types, state.Type),
		Objects:         page.Rows,
		HasAny:          len(page.Rows) > 0,
		Total:           total,
		Matched:         matched,
		TotalSize:       matchedSize,
		RangeStart:      page.RangeStart,
		RangeEnd:        page.RangeEnd,
		CurrentPage:     state.Offset/state.Limit + 1,
		PageCount:       max((matched+state.Limit-1)/state.Limit, 1),
		FirstURL:        paginationURL(state, 0),
		PreviousURL:     paginationURL(state, max(state.Offset-state.Limit, 0)),
		NextURL:         paginationURL(state, min(state.Offset+state.Limit, max(matched-1, 0))),
		LastURL:         paginationURL(state, max(matched-1, 0)/state.Limit*state.Limit),
		SortColumns:     sortColumns(state, s.cfg.Reachability != nil),
		HasPrevious:     state.Offset > 0 && matched > 0,
		HasNext:         state.Offset+state.Limit < matched,
		CSRF:            s.csrfFor(r),
		Role:            s.roleFor(r),
	}
	if selected, ok := page.selectedRow(rows); ok {
		data.Inspector = s.inspectorFor(r.Context(), id, state, selected)
	}
	s.renderObjects(w, r, data)
}

// objectPageResult is what the default hash-ascending browser path resolved:
// the page it cut, the store-wide totals behind it, and the state the rows
// were selected against. Fast reports that the default path could answer the
// request at all; when it is false the caller resolves the general path from
// the same state.
type objectPageResult struct {
	// Rows contains the current page's object rows.
	Rows []objectRow
	// Types lists every object type in the store.
	Types []string
	// TypeFound reports whether the requested type filter matched an object.
	TypeFound bool
	// Total is the number of stored objects.
	Total int
	// TotalSize is the aggregate stored size.
	TotalSize int64
	// Page is the resolved page and its selection.
	Page objectPage
	// State is the state the rows were selected against, with a defaulted
	// selection named.
	State objectBrowserState
	// Fast reports whether the default path answered the request.
	Fast bool
}

// defaultObjectPage keeps the common unfiltered hash-ascending browser path
// proportional to its visible page. BuildSnapshot preserves Backend.List's
// digest order, so constructing and sorting one row per stored object adds no
// information before pagination.
//
// Every result it returns carries Selected: -1 unless it also carries the rows
// that selection indexes, including the error results: the caller reads the
// page only after checking the error, and a page that names no selection is
// what makes that safe.
func (s *Server) defaultObjectPage(ctx context.Context, id string, state objectBrowserState) (objectPageResult, error) {
	// The rows and the page they belong to are resolved together. An empty
	// page is the starting point of every return, so an error or a request
	// this path cannot answer yields no selection to index.
	result := objectPageResult{Page: objectPage{Selected: -1}, State: state}
	if state.Query != "" || state.Type != "" || state.Size != "" || state.Status != "" ||
		state.Reach != "" || state.Sort != "hash" || state.Direction != "asc" ||
		state.Selected != "" || state.Deselected {
		return result, nil
	}
	snapshot, err := s.metadataSnapshot(ctx)
	if err != nil {
		return objectPageResult{Page: objectPage{Selected: -1}}, err
	}
	result.Total, result.TotalSize = snapshot.Total, snapshot.Bytes
	result.Types = snapshot.Types
	result.TypeFound, result.Fast = true, true
	if state.Offset >= len(snapshot.Entries) {
		return result, nil
	}
	end := min(state.Offset+state.Limit, len(snapshot.Entries))
	rows := make([]objectRow, 0, end-state.Offset)
	hasVerifications := s.sessions.hasVerifications(id)
	for _, entry := range snapshot.Entries[state.Offset:end] {
		rows = append(rows, s.objectRowFromMeta(id, entry, hasVerifications))
	}
	result.Rows = rows
	result.Page = objectPage{
		Rows:       rows,
		RangeStart: state.Offset + 1,
		RangeEnd:   end,
		Selected:   -1,
	}
	if len(rows) > 0 {
		state.Selected = rows[0].digestString()
		result.Page.Selected = 0
	}
	result.State = state
	return result, nil
}

// objectRows builds one row per stored object and keeps the ones state matches.
// It also reports every type present in the store, which the type filter
// offers, and whether the requested type was among them: a filter naming a type
// no object carries is a malformed request, not an empty page.
func (s *Server) objectRows(ctx context.Context, id string, state objectBrowserState) (rows []objectRow, types []string, typeFound bool, total int, matchedSize int64, err error) {
	snapshot, err := s.metadataSnapshot(ctx)
	if err != nil {
		return nil, nil, false, 0, 0, err
	}
	present := make(map[string]bool)
	typeFound = state.Type == ""
	hasVerifications := s.sessions.hasVerifications(id)
	for _, entry := range snapshot.Entries {
		row := s.objectRowFromMeta(id, entry, hasVerifications)
		if row.Type != "" {
			present[row.Type] = true
		}
		if state.Type == row.Type {
			typeFound = true
		}
		if matchesObjectRow(&row, state) {
			rows = append(rows, row)
			matchedSize += row.Size
		}
	}
	return rows, slices.Sorted(maps.Keys(present)), typeFound, snapshot.Total, matchedSize, nil
}

// objectRowFromMeta fills one row from the snapshot's metadata, keeping the
// per-session integrity and the host-supplied reachability verdicts beside it.
func (s *Server) objectRowFromMeta(id string, entry index.Entry, hasVerifications bool) objectRow {
	h := entry.Digest
	row := objectRow{
		hash: h,
		// An unreadable object keeps an empty Type so a type filter can never
		// match it — the type is unknown, not blank — while the cell still says
		// what happened.
		Type:              entry.Type,
		Unreadable:        entry.Unreadable,
		Size:              entry.Size,
		Integrity:         "not-verified",
		Written:           entry.Written,
		ReachabilityKnown: s.cfg.Reachability != nil,
	}
	if hasVerifications {
		row.Integrity = s.sessions.verification(id, h.String())
	}
	row.Orphaned = row.ReachabilityKnown && !s.cfg.Reachability.IsReachable(h)
	if s.cfg.References != nil {
		row.References = len(s.cfg.References.Inbound(h))
		row.ReferencesAvailable = true
	}
	row.Detached = row.Orphaned && row.ReferencesAvailable && row.References == 0
	row.Root = row.ReachabilityKnown && !row.Orphaned && row.ReferencesAvailable && row.References == 0
	return row
}

// prepareObjectRows adds template-only values after filtering, sorting, and
// paging. Rendering a 25-row page must not format 100,000 off-page rows.
func prepareObjectRows(rows []objectRow) {
	for i := range rows {
		prepareObjectRow(&rows[i])
	}
}

func prepareObjectRow(row *objectRow) {
	row.digestString()
	row.Short = shortDigest(row.hash)
	row.TypeLabel = row.Type
	if row.Unreadable {
		row.TypeLabel = "unreadable"
	}
	row.IntegrityLabel = integrityLabel(row.Integrity)
	row.WrittenLabel = formatWritten(row.Written)
}

func (row *objectRow) digestString() string {
	if row.Digest == "" {
		row.Digest = row.hash.String()
	}
	return row.Digest
}

// objectPage is the slice of rows one page shows, and where that slice sits in
// the full result.
type objectPage struct {
	// Rows contains the current page's object rows.
	Rows []objectRow
	// RangeStart is the zero-based start index in the full result.
	RangeStart int
	// RangeEnd is the exclusive end index in the full result.
	RangeEnd int
	// Selected indexes the inspected row within the full result, -1 when the
	// inspector stays empty.
	Selected int
}

// selectedRow returns the row the inspector shows, and whether the page names
// one that exists. Selected indexes the full result, so a page whose rows were
// never produced — a failed snapshot walk, say — reads as no selection instead
// of indexing a nil slice: reading the selection through here is what makes
// the caller's lookup total.
func (page objectPage) selectedRow(rows []objectRow) (objectRow, bool) {
	if page.Selected < 0 || page.Selected >= len(rows) {
		return objectRow{}, false
	}
	return rows[page.Selected], true
}

// pageObjects cuts the page out of rows and resolves which row the inspector
// shows, returning the state that describes the result. Paging and selection
// decide each other, so they are resolved together rather than in sequence.
// The returned state names the resolved selection, so a URL built from it
// reloads the result the operator sees.
func pageObjects(rows []objectRow, state objectBrowserState) (objectPage, objectBrowserState) {
	if !state.OffsetSet && state.Selected != "" {
		// Following a reference selects a row the current page may not hold, so
		// the browser pages to it. An explicit pager click stays put.
		if i := indexOfDigest(rows, state.Selected); i >= 0 {
			state.Offset = i / state.Limit * state.Limit
		}
	}
	page := objectPage{Selected: -1}
	if state.Offset < len(rows) {
		end := min(state.Offset+state.Limit, len(rows))
		page.RangeStart = state.Offset + 1
		page.RangeEnd = end
		page.Rows = rows[state.Offset:end]
	}
	page.Selected = indexOfDigest(rows, state.Selected)
	// An empty inspector beside a populated list is wasted space, so the first
	// visible row stands in — both before the operator picks one and after a
	// filter drops the one they had picked. A selection that merely sits on
	// another page still stands, so paging never steals it, and an explicit
	// deselect is honoured rather than undone. The URL is left alone: the
	// default is a rendering choice, not navigation — but the state names the
	// stand-in, so a URL built from it reloads the row actually shown.
	if page.Selected < 0 && len(page.Rows) > 0 && !state.Deselected {
		state.Selected = page.Rows[0].digestString()
		page.Selected = state.Offset
	}
	return page, state
}

// indexOfDigest locates a row by its rendered digest, or -1. An empty digest
// never matches, so a deselected browser finds nothing.
func indexOfDigest(rows []objectRow, digest string) int {
	if digest == "" {
		return -1
	}
	for i := range rows {
		if rows[i].digestString() == digest {
			return i
		}
	}
	return -1
}

// linkRows marks the selected row and points every row at the state its own
// click produces.
func linkRows(rows []objectRow, state objectBrowserState) {
	for i := range rows {
		rowState := state
		rowState.Nav = ""
		digest := rows[i].digestString()
		rows[i].Selected = digest == state.Selected
		// Clicking the selected row again clears the inspector, so its link
		// points at the deselected state instead of at itself.
		if rows[i].Selected {
			rowState.Selected = ""
			rowState.Deselected = true
		} else {
			rowState.Selected = digest
			rowState.Deselected = false
		}
		rows[i].SelectURL = rowState.url()
	}
}

// recordTrail updates the session trail for the selection state describes. The
// trail records how the operator browsed references: following a reference
// extends it, the Prev/Next controls only move its cursor, and picking a row in
// the table starts over — a table pick is a new point of departure, not a step
// in the chain that led here.
func (s *Server) recordTrail(id string, state objectBrowserState) {
	if state.Selected == "" {
		return
	}
	switch state.Nav {
	case navStay:
		// A tab switch re-renders the same object: nothing to record.
	case navTrail:
		s.sessions.seek(id, state.Selected)
	case navReference:
		s.sessions.visit(id, state.Selected)
	default:
		s.sessions.restart(id, state.Selected)
	}
}

// inspectorFor renders the selected row in the inspector. The row carries the
// digest the store listed, so the reference lookups reuse it rather than
// parsing the rendered form back — a digest that round-trips through the page
// is the same digest, and treating the trip as fallible only produced an error
// path nothing could reach.
func (s *Server) inspectorFor(ctx context.Context, id string, state objectBrowserState, row objectRow) *browserInspector {
	tabURL := func(tab string) string {
		tabbed := state
		tabbed.Tab = tab
		return tabbed.navURL(navStay)
	}
	prevDigest, nextDigest := s.sessions.trailNeighbors(id)
	inspector := &browserInspector{
		Digest:              row.Digest,
		HashAlgorithm:       s.cfg.HashAlgorithm,
		Type:                row.Type,
		Size:                row.Size,
		Integrity:           row.Integrity,
		IntegrityLabel:      row.IntegrityLabel,
		Report:              storedReport(s.sessions, id, row.Digest),
		Orphaned:            row.Orphaned,
		Detached:            row.Detached,
		Root:                row.Root,
		WrittenLabel:        row.WrittenLabel,
		ReferencesAvailable: s.cfg.References != nil,
		Timestamp:           formatTimestamp(row.Written),
		DumpURL:             "/viewer/objects/" + row.Digest + "/dump",
		MetadataURL:         tabURL("metadata"),
		BytesURL:            tabURL("bytes"),
		ReferencesURL:       tabURL("references"),
		PrevURL:             trailURL(state, prevDigest),
		NextURL:             trailURL(state, nextDigest),
	}
	if inspector.ReferencesAvailable {
		inbound := s.cfg.References.Inbound(row.hash)
		inspector.InboundReferences = len(inbound)
		inspector.Inbound = s.referenceRows(ctx, state, inbound)
		inspector.Outbound = s.referenceRows(ctx, state, s.cfg.References.Outbound(row.hash))
	}
	return inspector
}

// renderObjects emits the part of the browser the request asked for: a cold
// load renders the whole page, while htmx asks for the inspector alone, the
// inspector plus the list refresh a selection triggers, or the list.
func (s *Server) renderObjects(w http.ResponseWriter, r *http.Request, data objectBrowserData) {
	if r.Header.Get("HX-Request") != "true" {
		s.renderPage(w, "objects", data)
		return
	}
	if r.Header.Get("HX-Target") == "object-inspector" {
		if r.Header.Get("X-Viewer-Selection") == "true" {
			s.render(w, "object-selection", data)
			return
		}
		s.render(w, "object-inspector", data)
		return
	}
	s.render(w, "object-list-swap", data) // htmx search/refresh swap
}

// typeOptions renders the type filter's choices.
func typeOptions(types []string, selected string) []filterOption {
	options := make([]filterOption, 0, len(types))
	for _, typ := range types {
		options = append(options, filterOption{Value: typ, Label: typ, Selected: typ == selected})
	}
	return options
}

func (s *Server) referenceRows(ctx context.Context, state objectBrowserState, digests []cas.Digest) []referenceRow {
	rows := make([]referenceRow, 0, len(digests))
	for _, digest := range digests {
		rowState := state
		rowState.Query = ""
		rowState.Type = ""
		rowState.Size = ""
		rowState.Status = ""
		rowState.Reach = ""
		rowState.Offset = 0
		rowState.OffsetSet = false
		rowState.Selected = digest.String()
		rowState.Deselected = false
		rowState.Nav = ""
		rows = append(rows, referenceRow{
			Digest:    digest.String(),
			Short:     shortDigest(digest),
			Type:      strings.TrimSuffix(s.objectMetaFor(ctx, digest).Type, "@1"),
			SelectURL: rowState.navURL(navReference),
		})
	}
	return rows
}

// objectPageSizes lists the offered page sizes. A URL may still request any
// size up to maxObjectLimit, which limitOptions surfaces as an extra choice so
// the control never misreports the page it is showing.
var objectPageSizes = []int{25, 50, 100, maxObjectLimit}

// statusOptions renders the integrity filter's choices. Integrity states are
// alternatives on one axis, so exactly one of them can be selected.
func statusOptions(selected string) []filterOption {
	options := make([]filterOption, 0, len(objectStates))
	for _, state := range objectStates {
		options = append(options, filterOption{
			Value:    state,
			Label:    integrityLabel(state),
			Selected: state == selected,
		})
	}
	return options
}

// limitOptions renders the page-size control. A size the URL asked for but the
// control does not offer is inserted in order, so a clamped or hand-edited
// limit shows the page size actually in effect instead of silently reading as
// one of the presets.
func limitOptions(selected int) []filterOption {
	sizes := objectPageSizes
	if index, found := slices.BinarySearch(sizes, selected); !found {
		sizes = slices.Insert(slices.Clone(sizes), index, selected)
	}
	options := make([]filterOption, 0, len(sizes))
	for _, size := range sizes {
		label := strconv.Itoa(size)
		options = append(options, filterOption{
			Value:    label,
			Label:    label,
			Selected: size == selected,
		})
	}
	return options
}

// integrityLabel renders the byte-integrity axis. Reachability is a separate
// axis with its own column and filter, so Orphaned is never an integrity
// verdict and never appears here.
func integrityLabel(status string) string {
	switch status {
	case "not-verified":
		return "Unverified"
	case "verified":
		return "Verified"
	case "corrupt":
		return "Corrupt"
	default:
		return status
	}
}

// objectPermalink is the cold-load entry point for a single object
// (viewer-design §3): a bookmark or a shared link. The viewer has exactly one
// object view — the browser's inspector — so this route selects the object
// there rather than rendering a second, divergent detail page.
func (s *Server) objectPermalink(w http.ResponseWriter, r *http.Request) {
	h, ok := s.parseDigest(w, r)
	if !ok {
		return
	}
	if s.objectMetaFor(r.Context(), h).Unreadable {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	state := defaultObjectBrowserState()
	state.Selected = h.String()
	http.Redirect(w, r, state.url(), http.StatusSeeOther)
}

// objectDump renders the inspector's Bytes tab: a hexdump table of the
// object's leading bytes, lazily fetched once the tab is revealed. It serves
// HTML, not the stored bytes — the CLI is where raw content is read.
func (s *Server) objectDump(w http.ResponseWriter, r *http.Request) {
	h, ok := s.parseDigest(w, r)
	if !ok {
		return
	}
	data, truncated, err := s.readPreview(r.Context(), h)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	note := ""
	if truncated {
		note = fmt.Sprintf("preview truncated at %s of %s", formatBytes(previewLimit), formatBytes(s.objectMetaFor(r.Context(), h).Size))
	}
	s.render(w, "hexdump-table", struct {
		Rows []dumpRow
		Note string
	}{hexdump(data), note})
}
