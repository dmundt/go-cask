package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/index"
	"github.com/dmundt/go-cask/internal/test"
)

// seedHeaderCensus renders one store holding the three header shapes a census
// must tell apart: a current-format frame with a codec tag, a version 1 frame
// whose codec is unspecified, and raw bytes with no envelope at all.
func seedHeaderCensus(t *testing.T) (ts *httptest.Server, admin *http.Client, tagged, v1, raw cas.Digest) {
	t.Helper()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(frame []byte) cas.Digest {
		t.Helper()
		d := sha256.Of(frame)
		if err := backend.Put(ctx, d, bytes.NewReader(frame)); err != nil {
			t.Fatal(err)
		}
		return d
	}
	tagged = put(test.V2Envelope("json", "blob@1", []byte("tagged")))
	v1 = put(test.TLVEnvelope("note@1", []byte("v1")))
	raw = put([]byte("not an envelope"))
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts = httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, login(t, ts, testStartupToken), tagged, v1, raw
}

// TestHeaderCensusInTheViewer pins the viewer's half of the census: the table
// carries an envelope and a codec column, the cells render the version marked as
// one (`v1`, `v2`) and a version 1 frame explicitly unnamed rather than blank,
// raw bytes show the viewer's not-read marker instead of an invented version, and
// the inspector's Identity block names and marks the same field the same way
// (viewer-design §3).
func TestHeaderCensusInTheViewer(t *testing.T) {
	ts, admin, _, _, raw := seedHeaderCensus(t)
	page := getBody(t, admin, ts.URL+"/viewer/objects")
	for _, want := range []string{
		`viewer-sort-head">Envelope<a class="viewer-sort-button"`, // the column heading, not "Envelope version"
		`aria-label="Sort envelope version ascending"`,            // the heading's accessible name
		">v2</a></td>", ">json<", // the current-format frame
		">v1</a></td>", ">" + index.UnspecifiedCodec + "<", // the version 1 frame
		">—</a></td>", // the headerless object
		// The Identity block reads Algorithm, Type, Envelope, Codec in that
		// order, with the selected object's version marked as one.
		`<h2>Identity</h2><dl class="viewer-meta"><dt>Algorithm</dt><dd>sha256</dd><dt>Type</dt><dd>blob@1</dd><dt>Envelope</dt><dd>v2</dd><dt>Codec</dt><dd>json</dd></dl>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("object browser is missing %s: %.800q", want, page)
		}
	}
	// A version 0 object — bytes with no walkable header — keeps the not-read
	// marker in the inspector as well as in its cells: there is no frame whose
	// version could be stated, so `v0` would assert one that does not exist.
	rawPage := getBody(t, admin, ts.URL+"/viewer/objects?selected="+raw.String())
	if !strings.Contains(rawPage, "<dt>Envelope</dt><dd>—</dd>") {
		t.Fatalf("the inspector lost the not-read marker for headerless bytes: %.800q", rawPage)
	}
	if strings.Contains(rawPage, "<dd>v0</dd>") {
		t.Fatalf("the inspector invented a version 0 frame: %.800q", rawPage)
	}
}

// TestVersionFilterKeepsItsDecimalValue pins the one route the display marker
// could have leaked into: the version axis carries the frame's leading byte in
// decimal form (viewer-design §3) even though its cells now render `vN`, so
// `?version=2` selects the version 2 frame, `?version=99` and the marked form
// are still the 400 an absent value earns, `?sort=version` still sorts, and the
// filter's own option keeps the value the URL carries. Both strings come out of
// one decision point (versionFilterValue), so this fails the moment the marker
// is pushed back into the filter.
func TestVersionFilterKeepsItsDecimalValue(t *testing.T) {
	ts, admin, tagged, v1, raw := seedHeaderCensus(t)
	page := getBody(t, admin, ts.URL+"/viewer/objects?version=2")
	if !strings.Contains(page, shortDigest(tagged)) {
		t.Fatalf("?version=2 did not list the version 2 frame: %.600q", page)
	}
	for _, digest := range []cas.Digest{v1, raw} {
		if strings.Contains(page, shortDigest(digest)) {
			t.Fatalf("?version=2 listed %s, which carries no version 2 frame", digest)
		}
	}
	if !strings.Contains(page, ">v2</a></td>") {
		t.Fatalf("the version 2 cell does not render vN: %.600q", page)
	}
	if !strings.Contains(page, `<option value="2" selected>2</option>`) {
		t.Fatalf("the version filter's option no longer carries the decimal query value: %.600q", page)
	}
	// The sort key is the sort's identity, not a display string: `sort=version`
	// still owns the sort, and its announced name now reads envelope version.
	sorted := getBody(t, admin, ts.URL+"/viewer/objects?sort=version&dir=asc")
	for _, want := range []string{`aria-sort="ascending"`, `aria-label="Sort envelope version descending"`} {
		if !strings.Contains(sorted, want) {
			t.Fatalf("?sort=version no longer sorts the envelope column (%s missing): %.600q", want, sorted)
		}
	}
	for _, query := range []string{"?version=99", "?version=v2"} {
		if got := statusCode(t, admin, ts.URL+"/viewer/objects"+query); got != http.StatusBadRequest {
			t.Fatalf("/viewer/objects%s = %d, want 400", query, got)
		}
	}
}

// TestHeaderCensusFiltersAreExact pins the two new axes: each lists exactly the
// objects whose cells carry the value, `codec=unspecified` selects the version 1
// frame and not the headerless object, and a value the store does not hold is a
// malformed query at 400 rather than an empty page.
func TestHeaderCensusFiltersAreExact(t *testing.T) {
	ts, admin, tagged, v1, raw := seedHeaderCensus(t)
	for _, tc := range []struct {
		query   string
		want    cas.Digest
		notWant []cas.Digest
	}{
		{"/viewer/objects?version=2", tagged, []cas.Digest{v1, raw}},
		{"/viewer/objects?version=1", v1, []cas.Digest{tagged, raw}},
		{"/viewer/objects?codec=json", tagged, []cas.Digest{v1, raw}},
		{"/viewer/objects?codec=unspecified", v1, []cas.Digest{tagged, raw}},
		{"/viewer/objects?version=2&codec=json", tagged, []cas.Digest{v1, raw}},
	} {
		page := getBody(t, admin, ts.URL+tc.query)
		if !strings.Contains(page, shortDigest(tc.want)) {
			t.Fatalf("%s did not list %s: %.600q", tc.query, tc.want, page)
		}
		for _, digest := range tc.notWant {
			if strings.Contains(page, shortDigest(digest)) {
				t.Fatalf("%s listed %s, which does not match", tc.query, digest)
			}
		}
	}
	for _, query := range []string{"/viewer/objects?codec=nope", "/viewer/objects?version=9", "/viewer/objects?version=x"} {
		if got := statusCode(t, admin, ts.URL+query); got != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", query, got)
		}
	}
}

// TestCodecDifferenceIsNotCorruption pins the axis the census exists to keep
// honest: an object written with a codec tag whose bytes still hash to their
// address verifies, so the integrity axis stays address-based while the codec
// cell names the format (viewer-design §3, cas-core §4.8).
func TestCodecDifferenceIsNotCorruption(t *testing.T) {
	ts, admin, tagged, _, _ := seedHeaderCensus(t)
	csrf := csrfFromPage(getBody(t, admin, ts.URL+"/viewer/objects"))
	resp, err := admin.PostForm(ts.URL+"/viewer/objects/"+tagged.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(string(body), "Corrupt") {
		t.Fatalf("an intact frame carrying a codec tag was reported corrupt: %.400q", body)
	}
	page := getBody(t, admin, ts.URL+"/viewer/objects?codec=json")
	if !strings.Contains(page, ">json<") {
		t.Fatalf("the codec cell no longer names the format: %.400q", page)
	}
}
