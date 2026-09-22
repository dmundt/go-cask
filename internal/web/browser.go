// The object browser's view state: the query parameters that describe which
// objects are shown and in what order, the URLs that move between such
// states, and the filtering and ordering they ask for.

package web

import (
	"cmp"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

const (
	defaultObjectLimit = 25
	maxObjectLimit     = 250
)

type objectBrowserState struct {
	Query     string
	Type      string
	Size      string
	Status    string
	Reach     string
	Sort      string
	Direction string
	Limit     int
	Offset    int
	// OffsetSet records an explicitly requested page. Without it the browser
	// is free to page to whichever rows the selection lives on.
	OffsetSet bool
	Selected  string
	// Deselected records an explicitly empty selection: the operator clicked
	// the selected row again. It is distinct from an absent one, which still
	// auto-selects the first visible row.
	Deselected bool
	// Nav records how the operator arrived at this selection, which decides
	// what happens to the session trail. It describes a single click, so it
	// is never carried by url(): only navURL emits it.
	Nav string
	Tab string
}

// Navigation markers. An absent marker means the selection did not come from
// the inspector, so the trail restarts at it.
const (
	navReference = "ref"
	navTrail     = "trail"
	// navStay marks a click that re-renders the same selection — switching
	// inspector tabs. The selection did not move, so the trail must not.
	navStay = "stay"
)

func defaultObjectBrowserState() objectBrowserState {
	return objectBrowserState{
		Sort:      "hash",
		Direction: "asc",
		Limit:     defaultObjectLimit,
		Tab:       "metadata",
	}
}

// The object browser's closed value sets. Each axis names its own so the parser
// checks every one the same way, and so the filter controls and the parser can
// never offer and accept different things.
var (
	// objectSizeBands lists the size filter's buckets, which matchesObjectRow
	// turns into byte ranges.
	objectSizeBands = []string{"small", "medium", "large"}
	// objectReachStates lists the reachability filter's choices. Reachability
	// is a separate axis from integrity and has its own filter.
	objectReachStates = []string{"reachable", "orphaned"}
	// objectDirections lists the sort orders.
	objectDirections = []string{"asc", "desc"}
	// objectNavModes lists the navigation markers a selection may carry.
	objectNavModes = []string{navReference, navTrail, navStay}
	// objectTabs lists the inspector's panels.
	objectTabs = []string{"metadata", "references", "bytes"}
)

func parseObjectBrowserState(values url.Values) (objectBrowserState, error) {
	state := defaultObjectBrowserState()
	var err error
	if state.Query, err = queryValue(values, "q"); err != nil {
		return state, err
	}
	state.Query = strings.ToLower(strings.TrimSpace(state.Query))
	if state.Type, err = queryValue(values, "type"); err != nil {
		return state, err
	}
	if state.Size, err = enumValue(values, "size", "", objectSizeBands); err != nil {
		return state, err
	}
	if state.Status, err = enumValue(values, "status", "", objectStates); err != nil {
		return state, err
	}
	if state.Reach, err = enumValue(values, "reach", "", objectReachStates); err != nil {
		return state, err
	}
	if state.Sort, err = enumValue(values, "sort", "hash", objectSortKeys); err != nil {
		return state, err
	}
	if state.Direction, err = enumValue(values, "dir", "asc", objectDirections); err != nil {
		return state, err
	}
	if state.Limit, err = queryInt(values, "limit", defaultObjectLimit); err != nil {
		return state, err
	}
	// A page size is a preference, not an assertion about the store, so an
	// out-of-range one is clamped rather than rejected: a hand-edited or
	// stale URL should still render a page. Only a non-numeric limit is a
	// malformed query, and queryInt has already rejected that.
	state.Limit = min(max(state.Limit, 1), maxObjectLimit)
	if state.Offset, err = queryInt(values, "offset", 0); err != nil || state.Offset < 0 {
		return state, fmt.Errorf("invalid offset")
	}
	_, state.OffsetSet = values["offset"]
	if state.Selected, err = queryValue(values, "selected"); err != nil {
		return state, err
	}
	if state.Selected != "" {
		digest, parseErr := sha256.Parse(state.Selected)
		if parseErr != nil {
			return state, fmt.Errorf("invalid selected object: %w", parseErr)
		}
		state.Selected = digest.String()
	} else if _, present := values["selected"]; present {
		state.Deselected = true
	}
	if state.Nav, err = enumValue(values, "nav", "", objectNavModes); err != nil {
		return state, err
	}
	if state.Tab, err = queryValue(values, "tab"); err != nil {
		return state, err
	}
	// "actions" was a tab of its own before the metadata panel absorbed it. A
	// bookmark naming it is honoured rather than rejected, so it is rewritten
	// before the tab is checked.
	if state.Tab == "actions" {
		state.Tab = "metadata"
	}
	if state.Tab, err = checkEnum("tab", state.Tab, "metadata", objectTabs); err != nil {
		return state, err
	}
	return state, nil
}

// objectSortKeys lists every sortable column. It is derived from the header
// description below so a key the URL accepts and a column the table renders
// can never disagree.
var objectSortKeys = func() []string {
	keys := make([]string, 0, len(objectSortColumns))
	for _, column := range objectSortColumns {
		keys = append(keys, column.Key)
	}
	return keys
}()

// objectSortColumns describes the object table's header, in display order. It
// is the single source for both the sortable keys and the markup: seven
// hand-written copies of the same header is how the Hash column's accessible
// name drifted out of step with its own arrow.
var objectSortColumns = []struct {
	// Key is the sort key this column requests, and the value parsed back out
	// of the "sort" query parameter.
	Key string
	// Label is the visible column heading.
	Label string
	// Name is the column as the accessible name says it, which is not always
	// the heading: "Inbound" counts inbound references and says so.
	Name string
	// Class is the <th> class, empty when the column needs none.
	Class string
	// NeedsReachability marks a column the viewer can only render when the
	// host supplies a reachability index.
	NeedsReachability bool
}{
	{Key: "hash", Label: "Hash", Name: "hash"},
	{Key: "type", Label: "Type", Name: "type"},
	{Key: "size", Label: "Size", Name: "size"},
	{Key: "inbound", Label: "Inbound", Name: "inbound references", Class: "viewer-references"},
	{Key: "status", Label: "Integrity", Name: "integrity", Class: "viewer-status-cell"},
	{Key: "reach", Label: "References", Name: "references", Class: "viewer-status-cell", NeedsReachability: true},
	{Key: "written", Label: "Written", Name: "written"},
}

// sortColumn is one rendered table header. Every decision the markup would
// otherwise make is resolved here, so the template carries no per-column logic.
type sortColumn struct {
	Label string
	Name  string
	Class string
	// URL sorts by this column: it flips the direction when the column already
	// owns the sort and starts ascending otherwise.
	URL string
	// Active reports that this column owns the current sort.
	Active bool
	// AriaSort is the active column's current order, empty for the rest.
	AriaSort string
	// NextDirection names the order the next click applies. An inactive column
	// always starts ascending, whatever the active column is doing.
	NextDirection string
	// Glyph is the direction indicator: the active column's current order, and
	// the ascending arrow everywhere else.
	Glyph string
}

// sortColumns renders the table header for state, dropping the reachability
// column when the host supplies no index for it.
func sortColumns(state objectBrowserState, hasReachability bool) []sortColumn {
	columns := make([]sortColumn, 0, len(objectSortColumns))
	for _, def := range objectSortColumns {
		if def.NeedsReachability && !hasReachability {
			continue
		}
		column := sortColumn{
			Label:         def.Label,
			Name:          def.Name,
			Class:         def.Class,
			URL:           sortURL(state, def.Key),
			Active:        state.Sort == def.Key,
			NextDirection: "ascending",
			Glyph:         "▲",
		}
		if column.Active {
			column.AriaSort = state.Direction + "ending"
			if state.Direction == "asc" {
				column.NextDirection = "descending"
			} else {
				column.Glyph = "▼"
			}
		}
		columns = append(columns, column)
	}
	return columns
}

// objectStates lists the selectable integrity filters in display order.
// Reachability is the other, independent axis and has its own filter.
var objectStates = []string{"verified", "not-verified", "corrupt"}

func queryValue(values url.Values, key string) (string, error) {
	all, ok := values[key]
	if !ok {
		return "", nil
	}
	if len(all) != 1 {
		return "", fmt.Errorf("repeated %s", key)
	}
	return all[0], nil
}

func queryInt(values url.Values, key string, fallback int) (int, error) {
	value, err := queryValue(values, key)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

// enumValue reads a query parameter whose values form a closed set.
func enumValue(values url.Values, key, fallback string, allowed []string) (string, error) {
	value, err := queryValue(values, key)
	if err != nil {
		return "", err
	}
	return checkEnum(key, value, fallback, allowed)
}

// checkEnum resolves one closed-set value. An absent value reads as the
// fallback, and so does an empty one: a filter control reset to "All" submits
// an empty string. Anything else outside the set is a malformed query rather
// than a filter that happens to match nothing — the viewer says so instead of
// rendering a plausible but wrong empty page.
func checkEnum(key, value, fallback string, allowed []string) (string, error) {
	if value == "" {
		return fallback, nil
	}
	if !slices.Contains(allowed, value) {
		return "", fmt.Errorf("invalid %s", key)
	}
	return value, nil
}

func (state objectBrowserState) url() string {
	values := url.Values{}
	if state.Query != "" {
		values.Set("q", state.Query)
	}
	if state.Type != "" {
		values.Set("type", state.Type)
	}
	if state.Size != "" {
		values.Set("size", state.Size)
	}
	if state.Status != "" {
		values.Set("status", state.Status)
	}
	if state.Reach != "" {
		values.Set("reach", state.Reach)
	}
	if state.Sort != "hash" {
		values.Set("sort", state.Sort)
	}
	if state.Direction != "asc" {
		values.Set("dir", state.Direction)
	}
	if state.Limit != defaultObjectLimit {
		values.Set("limit", strconv.Itoa(state.Limit))
	}
	if state.Offset != 0 || state.OffsetSet {
		values.Set("offset", strconv.Itoa(state.Offset))
	}
	if state.Selected != "" {
		values.Set("selected", state.Selected)
	} else if state.Deselected {
		values.Set("selected", "")
	}
	if state.Tab != "metadata" {
		values.Set("tab", state.Tab)
	}
	encoded := values.Encode()
	if encoded == "" {
		return "/viewer/objects"
	}
	return "/viewer/objects?" + encoded
}

// navURL renders the state and marks how the operator arrived at it. The
// marker lives outside url() on purpose: it describes one click, so it must
// never survive into the filter, sort, or pager links built from the same
// state.
func (state objectBrowserState) navURL(mode string) string {
	raw := state.url()
	separator := "?"
	if strings.Contains(raw, "?") {
		separator = "&"
	}
	return raw + separator + "nav=" + mode
}

// trailURL selects a neighbouring trail entry. An empty digest yields an empty
// URL, which renders the control disabled rather than as a dead link.
func trailURL(state objectBrowserState, digest string) string {
	if digest == "" {
		return ""
	}
	state.Selected = digest
	state.Deselected = false
	// The trail entry may live on another page, so the jump that follows a
	// reference link applies here too.
	state.Offset = 0
	state.OffsetSet = false
	return state.navURL(navTrail)
}

func matchesObjectRow(row objectRow, state objectBrowserState) bool {
	if state.Query != "" && !strings.Contains(row.Digest, state.Query) && !strings.Contains(strings.ToLower(row.Type), state.Query) {
		return false
	}
	if state.Type != "" && row.Type != state.Type {
		return false
	}
	// Integrity and reachability are independent axes, so each narrows the
	// match on its own: a corrupt orphan needs both facts to match.
	if state.Status != "" && row.Integrity != state.Status {
		return false
	}
	switch state.Reach {
	case "orphaned":
		if !row.Orphaned {
			return false
		}
	case "reachable":
		if row.Orphaned {
			return false
		}
	}
	switch state.Size {
	case "small":
		return row.Size < 1<<10
	case "medium":
		return row.Size >= 1<<10 && row.Size <= 1<<20
	case "large":
		return row.Size > 1<<20
	default:
		return true
	}
}

func sortObjectRows(rows []objectRow, state objectBrowserState) {
	slices.SortFunc(rows, func(left, right objectRow) int {
		var comparison int
		switch state.Sort {
		case "type":
			comparison = strings.Compare(left.Type, right.Type)
		case "size":
			comparison = cmp.Compare(left.Size, right.Size)
		case "inbound":
			comparison = cmp.Compare(left.References, right.References)
		case "status":
			// Ascending puts the sound state first, matching the other axes,
			// so the key is ranked rather than compared as text: alphabetical
			// order would lead with "corrupt".
			comparison = cmp.Compare(integrityOrder(left.Integrity), integrityOrder(right.Integrity))
		case "reach":
			// Ascending puts the sound state first, matching the other axes.
			comparison = cmp.Compare(boolOrder(left.Orphaned), boolOrder(right.Orphaned))
		case "written":
			comparison = left.Written.Compare(right.Written)
		default:
			comparison = strings.Compare(left.Digest, right.Digest)
		}
		if comparison == 0 {
			comparison = strings.Compare(left.Digest, right.Digest)
		}
		if state.Direction == "desc" {
			return -comparison
		}
		return comparison
	})
}

// integrityOrder ranks an integrity key so an ascending sort reads from sound
// to suspect, the order the status filter lists them in. Comparing the keys as
// text would order them corrupt, not-verified, verified — backwards.
func integrityOrder(integrity string) int {
	switch integrity {
	case "verified":
		return 0
	case "corrupt":
		return 2
	default:
		return 1
	}
}

// boolOrder ranks a flag so the false state sorts first, which keeps an
// ascending sort on a two-state axis reading "sound before suspect" like the
// other columns.
func boolOrder(flag bool) int {
	if flag {
		return 1
	}
	return 0
}

func paginationURL(state objectBrowserState, offset int) string {
	state.Offset = offset
	// A pager link is an explicit page request, so it must survive even when it
	// lands on page 1 and even when the selection lives elsewhere.
	state.OffsetSet = true
	return state.url()
}

func sortURL(state objectBrowserState, key string) string {
	if state.Sort == key {
		if state.Direction == "asc" {
			state.Direction = "desc"
		} else {
			state.Direction = "asc"
		}
	} else {
		state.Sort = key
		state.Direction = "asc"
	}
	state.Offset = 0
	return state.url()
}

// --- object detail ---
