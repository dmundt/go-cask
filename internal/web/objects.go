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
	// SelectURL opens this row in the browser inspector.
	SelectURL string
}

// --- objects list ---

type objectBrowserData struct {
	State           objectBrowserState
	RefreshURL      string
	HasReachability bool
	StatusOptions   []filterOption
	LimitOptions    []filterOption
	Objects         []objectRow
	Types           []filterOption
	HasAny          bool
	Total           int
	Matched         int
	TotalSize       int64
	RangeStart      int
	RangeEnd        int
	CurrentPage     int
	PageCount       int
	FirstURL        string
	PreviousURL     string
	NextURL         string
	LastURL         string
	// SortColumns is the table header: one entry per rendered column, already
	// resolved into the link, the arrow, and the accessible name it needs.
	SortColumns []sortColumn
	HasPrevious bool
	HasNext     bool
	Inspector   *browserInspector
	CSRF        string
	Role        string
}

type filterOption struct {
	Value    string
	Label    string
	Selected bool
}

type browserInspector struct {
	Digest         string
	Type           string
	Size           int64
	Integrity      string
	IntegrityLabel string
	// Report is the finding of the last check of this object in this session,
	// replayed so the inspector states the outcome and its age on every visit
	// rather than only in the response to the click that produced it. It is nil
	// when the object has not been checked.
	Report              *actionOutcome
	Orphaned            bool
	WrittenLabel        string
	InboundReferences   int
	ReferencesAvailable bool
	Inbound             []referenceRow
	Outbound            []referenceRow
	Timestamp           string
	HexdumpURL          string
	MetadataURL         string
	BytesURL            string
	ReferencesURL       string
	// PrevURL and NextURL step through the objects this session already
	// inspected; an empty one disables that control.
	PrevURL string
	NextURL string
}

type referenceRow struct {
	Digest    string
	Short     string
	Type      string
	SelectURL string
}

// objects renders the object browser. It is a pipeline: read the request into
// a state, turn the store into the rows that state matches, page and select
// among them, then render whichever part of the page the request asked for.
func (s *Server) objects(w http.ResponseWriter, r *http.Request) {
	state, err := parseObjectBrowserState(r.URL.Query())
	if err != nil {
		http.Error(w, "invalid object browser query", http.StatusBadRequest)
		return
	}
	if state.Reach != "" && s.cfg.Reachability == nil {
		http.Error(w, "reachability filter unavailable", http.StatusBadRequest)
		return
	}
	id := sessionID(r)
	rows, types, typeFound, total, err := s.objectRows(r.Context(), id, state)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	if !typeFound {
		http.Error(w, "invalid object type", http.StatusBadRequest)
		return
	}
	sortObjectRows(rows, state)
	// The refresher reloads from the URL the request named, so that URL is
	// captured before paging: a page the browser chose on the operator's behalf
	// must not become one they are pinned to.
	refreshURL := state.url()
	page, state := pageObjects(rows, state)
	if page.Defaulted {
		// A defaulted selection is the exception — it has to be named, because
		// the original URL would resurrect the row the filter dropped.
		refreshURL = state.url()
	}
	linkRows(page.Rows, state)
	s.recordTrail(id, state)

	data := objectBrowserData{
		State:           state,
		RefreshURL:      refreshURL,
		HasReachability: s.cfg.Reachability != nil,
		StatusOptions:   statusOptions(state.Status),
		LimitOptions:    limitOptions(state.Limit),
		Types:           typeOptions(types, state.Type),
		Objects:         page.Rows,
		HasAny:          len(page.Rows) > 0,
		Total:           total,
		Matched:         len(rows),
		TotalSize:       totalSize(rows),
		RangeStart:      page.RangeStart,
		RangeEnd:        page.RangeEnd,
		CurrentPage:     state.Offset/state.Limit + 1,
		PageCount:       max((len(rows)+state.Limit-1)/state.Limit, 1),
		FirstURL:        paginationURL(state, 0),
		PreviousURL:     paginationURL(state, max(state.Offset-state.Limit, 0)),
		NextURL:         paginationURL(state, min(state.Offset+state.Limit, max(len(rows)-1, 0))),
		LastURL:         paginationURL(state, max(len(rows)-1, 0)/state.Limit*state.Limit),
		SortColumns:     sortColumns(state, s.cfg.Reachability != nil),
		HasPrevious:     state.Offset > 0 && len(rows) > 0,
		HasNext:         state.Offset+state.Limit < len(rows),
		CSRF:            s.csrfFor(r),
		Role:            s.roleFor(r),
	}
	if page.Selected >= 0 {
		data.Inspector = s.inspectorFor(r.Context(), id, state, rows[page.Selected])
	}
	s.renderObjects(w, r, data)
}

// objectRows builds one row per stored object and keeps the ones state matches.
// It also reports every type present in the store, which the type filter
// offers, and whether the requested type was among them: a filter naming a type
// no object carries is a malformed request, not an empty page.
func (s *Server) objectRows(ctx context.Context, id string, state objectBrowserState) (rows []objectRow, types []string, typeFound bool, total int, err error) {
	digests, err := s.store.List(ctx)
	if err != nil {
		return nil, nil, false, 0, err
	}
	present := make(map[string]bool)
	typeFound = state.Type == ""
	for _, h := range digests {
		row := s.objectRowFor(ctx, id, h)
		if row.Type != "" {
			present[row.Type] = true
		}
		if state.Type == row.Type {
			typeFound = true
		}
		if matchesObjectRow(row, state) {
			rows = append(rows, row)
		}
	}
	return rows, slices.Sorted(maps.Keys(present)), typeFound, len(digests), nil
}

// objectRowFor renders one stored object as a table row.
func (s *Server) objectRowFor(ctx context.Context, id string, h cas.Digest) objectRow {
	meta := s.objectMetaFor(ctx, h)
	row := objectRow{
		hash:   h,
		Digest: h.String(),
		Short:  shortDigest(h),
		// An unreadable object keeps an empty Type so a type filter can never
		// match it — the type is unknown, not blank — while the cell still says
		// what happened.
		Type:              meta.Type,
		TypeLabel:         meta.Type,
		Unreadable:        meta.Unreadable,
		Size:              meta.Size,
		Integrity:         s.sessions.verification(id, h.String()),
		Written:           meta.Written,
		WrittenLabel:      formatWritten(meta.Written),
		ReachabilityKnown: s.cfg.Reachability != nil,
	}
	if meta.Unreadable {
		row.TypeLabel = "unreadable"
	}
	row.IntegrityLabel = integrityLabel(row.Integrity)
	row.Orphaned = row.ReachabilityKnown && !s.cfg.Reachability.IsReachable(h)
	if s.cfg.References != nil {
		row.References = len(s.cfg.References.Inbound(h))
		row.ReferencesAvailable = true
	}
	return row
}

// objectPage is the slice of rows one page shows, and where that slice sits in
// the full result.
type objectPage struct {
	Rows       []objectRow
	RangeStart int
	RangeEnd   int
	// Selected indexes the inspected row within the full result, -1 when the
	// inspector stays empty.
	Selected int
	// Defaulted reports that the browser picked the selection rather than the
	// request naming it, which the refresh URL has to account for.
	Defaulted bool
}

// pageObjects cuts the page out of rows and resolves which row the inspector
// shows, returning the state that describes the result. Paging and selection
// decide each other, so they are resolved together rather than in sequence.
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
	// default is a rendering choice, not navigation.
	if page.Selected < 0 && len(page.Rows) > 0 && !state.Deselected {
		state.Selected = page.Rows[0].Digest
		page.Selected = state.Offset
		page.Defaulted = true
	}
	return page, state
}

// indexOfDigest locates a row by its rendered digest, or -1. An empty digest
// never matches, so a deselected browser finds nothing.
func indexOfDigest(rows []objectRow, digest string) int {
	if digest == "" {
		return -1
	}
	return slices.IndexFunc(rows, func(row objectRow) bool { return row.Digest == digest })
}

// linkRows marks the selected row and points every row at the state its own
// click produces.
func linkRows(rows []objectRow, state objectBrowserState) {
	for i := range rows {
		rowState := state
		rowState.Nav = ""
		rows[i].Selected = rows[i].Digest == state.Selected
		// Clicking the selected row again clears the inspector, so its link
		// points at the deselected state instead of at itself.
		if rows[i].Selected {
			rowState.Selected = ""
			rowState.Deselected = true
		} else {
			rowState.Selected = rows[i].Digest
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
		Type:                row.Type,
		Size:                row.Size,
		Integrity:           row.Integrity,
		IntegrityLabel:      row.IntegrityLabel,
		Report:              storedReport(s.sessions, id, row.Digest),
		Orphaned:            row.Orphaned,
		WrittenLabel:        row.WrittenLabel,
		ReferencesAvailable: s.cfg.References != nil,
		Timestamp:           formatTimestamp(row.Written),
		HexdumpURL:          "/viewer/objects/" + row.Digest + "/hexdump",
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

// totalSize sums the matched rows, which the pager reports beside their count.
func totalSize(rows []objectRow) int64 {
	var total int64
	for _, row := range rows {
		total += row.Size
	}
	return total
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
	h, ok := parseDigest(w, r)
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

// objectHexdump renders the inspector's Bytes tab: a hexdump table of the
// object's leading bytes, lazily fetched once the tab is revealed. It serves
// HTML, not the stored bytes — the CLI is where raw content is read.
func (s *Server) objectHexdump(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigest(w, r)
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
