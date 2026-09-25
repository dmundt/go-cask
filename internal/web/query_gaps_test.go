// Tests closing the uncovered branches of the object browser's request state:
// the parser's malformed-input branches, the closed-set readers, the URL
// builder, the size bands, and the comparators of the sortable columns.

package web

import (
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// TestParseObjectBrowserStateRejectsMalformedQuery pins the parser's remaining
// rejection branches, one named case per rule: a repeated single-valued
// parameter, a frame version that is not a number, a reachability state outside
// its closed set, and a selection that is not a well-formed digest. Each is a
// malformed request the route answers 400 — never a filter that quietly matches
// nothing (viewer-design §5).
func TestParseObjectBrowserStateRejectsMalformedQuery(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"repeated query term", "q=one&q=two"},
		{"repeated object type", "type=blob@1&type=tree@1"},
		{"repeated version", "version=1&version=2"},
		{"version is not a number", "version=one"},
		{"repeated codec", "codec=json&codec=cbor"},
		{"repeated size band", "size=small&size=large"},
		{"repeated integrity state", "status=verified&status=corrupt"},
		{"reachability outside its set", "reach=everywhere"},
		{"repeated reachability state", "reach=root&reach=orphaned"},
		{"repeated sort key", "sort=size&sort=type"},
		{"repeated direction", "dir=asc&dir=desc"},
		{"repeated page size", "limit=10&limit=20"},
		{"repeated offset", "offset=1&offset=2"},
		{"selected object is not hex", "selected=not-a-digest"},
		{"repeated selected object", "selected=ab&selected=cd"},
		{"repeated navigation marker", "nav=ref&nav=trail"},
		{"repeated tab", "tab=bytes&tab=metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", tc.query, err)
			}
			state, err := parseObjectBrowserState(values, sha256.New())
			if err == nil {
				t.Fatalf("parseObjectBrowserState(%q) returned state %+v, want a malformed-query error", tc.query, state)
			}
			// The route discards the state on error, but the rejected parse
			// must not have flipped a switch that silently widens the query:
			// an explicit deselect or page request changes what is rendered,
			// and neither may come out of a rejected request.
			if state.Deselected || state.OffsetSet {
				t.Fatalf("rejected query %q produced state %+v, want no deselect and no explicit page", tc.query, state)
			}
		})
	}
}

// TestParseObjectBrowserStateAcceptsEmptySelection pins the one value that is
// present but empty: an operator clicking the selected row again deselects it,
// which is a different request from an absent selection (that one auto-selects
// the first visible row).
func TestParseObjectBrowserStateAcceptsEmptySelection(t *testing.T) {
	state, err := parseObjectBrowserState(url.Values{"selected": {""}}, sha256.New())
	if err != nil {
		t.Fatalf("parseObjectBrowserState(selected=) = %v, want the explicitly empty selection", err)
	}
	if !state.Deselected || state.Selected != "" {
		t.Fatalf("parseObjectBrowserState(selected=) = %+v, want Deselected with no Selected", state)
	}
}

// TestParseObjectBrowserStateRejectsASelectionTheHasherRefuses pins the second
// half of the selection check: text the core can parse is still refused when
// the configured algorithm says it cannot name an object. The viewer says the
// request is malformed rather than rendering an inspector for an address the
// store could never hold.
func TestParseObjectBrowserStateRejectsASelectionTheHasherRefuses(t *testing.T) {
	// This hasher accepts exactly one digest — 16 bytes of 0xab — and refuses
	// every other well-formed one, which is the shape another algorithm gives.
	accepted := strings.Repeat("ab", 16)
	other := strings.Repeat("cd", 16)
	acceptedDigest, err := cas.ParseDigest(accepted)
	if err != nil {
		t.Fatal(err)
	}
	hasher := fixedWidthHasher{accepted: acceptedDigest}

	state, err := parseObjectBrowserState(url.Values{"selected": {accepted}}, hasher)
	if err != nil {
		t.Fatalf("parseObjectBrowserState(%q) = %v, want the accepted selection", accepted, err)
	}
	if state.Selected != accepted {
		t.Fatalf("state.Selected = %q, want the canonical %q", state.Selected, accepted)
	}
	if _, err := parseObjectBrowserState(url.Values{"selected": {other}}, hasher); err == nil {
		t.Fatalf("parseObjectBrowserState(%q) = nil error, want the hasher's refusal", other)
	}
}

// fixedWidthHasher accepts one digest and refuses every other, so a test can
// exercise the hasher half of a digest guard without another algorithm.
type fixedWidthHasher struct {
	accepted cas.Digest
}

func (h fixedWidthHasher) Validate(d cas.Digest) error {
	if d.Equal(h.accepted) {
		return nil
	}
	return errNoDigest
}

func (h fixedWidthHasher) Digest(r io.Reader) (cas.Digest, error) {
	return sha256.New().Digest(r)
}

// TestQueryIntRejectsNonNumeric covers the reader's own failure: a page or
// offset that is present but not a number is a malformed query, while an absent
// or empty one reads as the fallback. The presence of the error is what the
// route turns into a 400.
func TestQueryIntRejectsNonNumeric(t *testing.T) {
	if got, err := queryInt(url.Values{}, "offset", 7); err != nil || got != 7 {
		t.Fatalf("queryInt(absent) = (%d, %v), want (7, nil)", got, err)
	}
	if got, err := queryInt(url.Values{"offset": {""}}, "offset", 7); err != nil || got != 7 {
		t.Fatalf("queryInt(empty) = (%d, %v), want (7, nil)", got, err)
	}
	if _, err := queryInt(url.Values{"offset": {"x"}}, "offset", 7); err == nil {
		t.Fatal("queryInt(non-numeric) = nil error, want a parse error")
	}
}

// TestEnumValueOutsideItsSet pins the closed-set reader for a value the viewer
// does not offer, which the route answers 400 rather than rendering a plausible
// but wrong empty page.
func TestEnumValueOutsideItsSet(t *testing.T) {
	if got, err := enumValue(url.Values{"status": {"corrupt"}}, "status", "", objectStates); err != nil || got != "corrupt" {
		t.Fatalf("enumValue(offered) = (%q, %v), want (corrupt, nil)", got, err)
	}
	if got, err := enumValue(url.Values{}, "status", "verified", objectStates); err != nil || got != "verified" {
		t.Fatalf("enumValue(absent) = (%q, %v), want the fallback", got, err)
	}
	if _, err := enumValue(url.Values{"status": {"orphaned"}}, "status", "", objectStates); err == nil {
		t.Fatal("enumValue(unoffered) = nil error, want an error")
	}
}

// TestObjectBrowserStateURLCarriesEveryFilter pins the URL builder: each filter
// and the selection appear as their own parameter, and a parameter left at its
// default is omitted so two states that mean the same thing render one URL.
func TestObjectBrowserStateURLCarriesEveryFilter(t *testing.T) {
	state := defaultObjectBrowserState()
	state.Query = "blob"
	state.Type = "blob@1"
	state.Version = "2"
	state.Codec = "json"
	state.Size = "medium"
	state.Status = "verified"
	state.Reach = "orphaned"
	state.Sort = "size"
	state.Direction = "desc"
	state.Limit = 50
	state.Offset = 25
	state.OffsetSet = true
	state.Selected = "sha256:" + strings.Repeat("ab", 32)
	state.Tab = "bytes"

	got := state.url()
	for _, want := range []string{
		"q=blob",
		"type=blob%401",
		"version=2",
		"codec=json",
		"size=medium",
		"status=verified",
		"reach=orphaned",
		"sort=size",
		"dir=desc",
		"limit=50",
		"offset=25",
		"selected=sha256%3A" + strings.Repeat("ab", 32),
		"tab=bytes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("url() = %q, missing %q", got, want)
		}
	}

	// The default state names no parameters at all.
	if got := defaultObjectBrowserState().url(); got != "/viewer/objects" {
		t.Fatalf("default url() = %q, want the bare browser path", got)
	}

	// An explicit deselect travels as an empty selection; an explicit first
	// page travels as offset=0, because a pager click must survive landing on
	// page one.
	deselected := defaultObjectBrowserState()
	deselected.Deselected = true
	if got := deselected.url(); !strings.Contains(got, "selected=") {
		t.Fatalf("deselected url() = %q, want it to carry the empty selection", got)
	}
	firstPage := defaultObjectBrowserState()
	firstPage.OffsetSet = true
	if got := firstPage.url(); !strings.Contains(got, "offset=0") {
		t.Fatalf("explicit first page url() = %q, want offset=0", got)
	}
}

// TestMatchesObjectRowSizeBands pins the size filter's three buckets at their
// boundaries: a row belongs to exactly one of small (< 1 KiB), medium (1 KiB to
// 1 MiB inclusive), and large (> 1 MiB).
func TestMatchesObjectRowSizeBands(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
		band string
	}{
		{"just below one KiB is small", 1<<10 - 1, "small"},
		{"one KiB is medium", 1 << 10, "medium"},
		{"one MiB is medium", 1 << 20, "medium"},
		{"just above one MiB is large", 1<<20 + 1, "large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := objectRow{Size: tc.size}
			for _, band := range objectSizeBands {
				want := band == tc.band
				if got := matchesObjectRow(&row, objectBrowserState{Size: band}); got != want {
					t.Errorf("size %d matched band %q = %v, want %v", tc.size, band, got, want)
				}
			}
			// No size filter matches every row.
			if !matchesObjectRow(&row, objectBrowserState{}) {
				t.Errorf("size %d matched no filter = false, want true", tc.size)
			}
		})
	}
}

// TestSortObjectRowsOrdersEveryColumn pins the orderings the table has but the
// other sort tests do not cover: the frame version (numeric, so version 10
// follows version 9), the codec tag (textual), and the written time (a time
// comparison, not a string one). Rows are identified by their digest so a
// result that ignores the key is visible.
func TestSortObjectRowsOrdersEveryColumn(t *testing.T) {
	base := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	rows := []objectRow{
		{Digest: "a", Version: 10, Codec: "json", Written: base.Add(2 * time.Hour)},
		{Digest: "b", Version: 2, Codec: "cbor", Written: base.Add(-time.Hour)},
		{Digest: "c", Version: 1, Codec: "zstd", Written: base},
	}
	for _, tc := range []struct {
		name  string
		state objectBrowserState
		want  string
	}{
		{"version ascending is numeric, not textual", objectBrowserState{Sort: "version", Direction: "asc"}, "cba"},
		{"version descending", objectBrowserState{Sort: "version", Direction: "desc"}, "abc"},
		{"codec ascending is the tag order", objectBrowserState{Sort: "codec", Direction: "asc"}, "bac"},
		{"written ascending is the oldest first", objectBrowserState{Sort: "written", Direction: "asc"}, "bca"},
		{"written descending is the newest first", objectBrowserState{Sort: "written", Direction: "desc"}, "acb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sorted := append([]objectRow(nil), rows...)
			sortObjectRows(sorted, tc.state)
			var got strings.Builder
			for _, row := range sorted {
				got.WriteString(row.Digest)
			}
			if got.String() != tc.want {
				t.Fatalf("sortObjectRows(%+v) = %q, want %q", tc.state, got.String(), tc.want)
			}
		})
	}
}

// TestCompareObjectRowDigestUsesTheAddressWhenKnown pins the comparator behind
// every tie: a row built from the store carries the raw address and is compared
// as bytes, while a row that only has its rendered digest still compares as
// text.
func TestCompareObjectRowDigestUsesTheAddressWhenKnown(t *testing.T) {
	low := mustParse(t, "sha256:"+strings.Repeat("11", 32))
	high := mustParse(t, "sha256:"+strings.Repeat("ff", 32))
	if got := compareObjectRowDigest(objectRow{hash: low}, objectRow{hash: high}); got >= 0 {
		t.Fatalf("byte order of 0x11… against 0xff… = %d, want a negative comparison", got)
	}
	if got := compareObjectRowDigest(objectRow{Digest: "aaa"}, objectRow{Digest: "bbb"}); got >= 0 {
		t.Fatalf("text order of aaa against bbb = %d, want a negative comparison", got)
	}
}

// TestIntegrityLabelFallsBackToItsValue pins the rendering of a state the
// viewer does not know: the label is the state itself rather than a blank cell
// that would read as no result at all.
func TestIntegrityLabelFallsBackToItsValue(t *testing.T) {
	for state, want := range map[string]string{
		"not-verified": "Unverified",
		"verified":     "Verified",
		"corrupt":      "Corrupt",
		"rechecking":   "rechecking",
		"":             "",
	} {
		if got := integrityLabel(state); got != want {
			t.Errorf("integrityLabel(%q) = %q, want %q", state, got, want)
		}
	}
}
