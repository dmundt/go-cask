// Tests for the object rows, the inspector, and the raw-byte view.

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
	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

func TestObjectRawIsLimitedTo256Bytes(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte{0xab}, previewLimit+1)
	h := mustParse(t, "sha256:"+strings.Repeat("ef", 32))
	if err := srv.store.Put(ctx, h, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	viewer := login(t, ts, "viewer-tok")
	resp, err := viewer.Get(ts.URL + "/viewer/objects/" + h.String() + "/dump")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "preview truncated at 256 B of 257 B") {
		t.Fatalf("raw preview = (%d, %.400q), want formatted truncation note", resp.StatusCode, page)
	}
	if strings.Contains(page, "00000100") {
		t.Fatalf("raw preview rendered bytes beyond offset 255: %.400q", page)
	}
}

func TestObjectInspectorRendersInboundReferenceCount(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	target := mustParse(t, "sha256:"+strings.Repeat("a1", 32))
	if err := raw.Put(ctx, target, bytes.NewReader(tlvEnvelope("blob@1", nil))); err != nil {
		t.Fatal(err)
	}
	references := newTestReferenceIndex()
	references.Record(mustParse(t, "sha256:"+strings.Repeat("b2", 32)), []cas.Digest{target})
	references.Record(mustParse(t, "sha256:"+strings.Repeat("c3", 32)), []cas.Digest{target})
	srv, err := New(raw, Config{StartupToken: testStartupToken, References: references})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	resp, err := viewer.Get(ts.URL + "/viewer/objects?selected=" + target.String())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	// The count is "Inbound", not "References": the reference column carries
	// the reachability verdict, so reusing the name would collide with it.
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "<dt>Inbound</dt><dd>2</dd>") ||
		!strings.Contains(page, `<td class="viewer-references"><a`) || !strings.Contains(page, `>2</a></td>`) {
		t.Fatalf("selected object = (%d, %.400q), want inbound count", resp.StatusCode, page)
	}
	// The count belongs to the reference axis, so it sits in State beside the
	// reachability verdict rather than among the storage facts.
	state := strings.Index(page, "<h2>State</h2>")
	if state < 0 || state > strings.Index(page, "<dt>Inbound</dt>") {
		t.Fatalf("inbound count must render inside the State section: %.900q", page)
	}
}

func TestObjectInspectorRendersReferencesTab(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := mustParse(t, "sha256:"+strings.Repeat("a1", 32))
	inbound := mustParse(t, "sha256:"+strings.Repeat("b2", 32))
	outbound := mustParse(t, "sha256:"+strings.Repeat("c3", 32))
	for _, object := range []struct {
		digest cas.Digest
		typ    string
	}{
		{target, "blob@1"},
		{inbound, "commit@1"},
		{outbound, "tree@1"},
	} {
		if err := raw.Put(ctx, object.digest, bytes.NewReader(tlvEnvelope(object.typ, nil))); err != nil {
			t.Fatal(err)
		}
	}
	references := newTestReferenceIndex()
	references.Record(inbound, []cas.Digest{target})
	references.Record(target, []cas.Digest{outbound})
	srv, err := New(raw, Config{StartupToken: testStartupToken, References: references})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	resp, err := viewer.Get(ts.URL + "/viewer/objects?selected=" + target.String() + "&tab=references")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{
		`aria-current="page"`,
		"<h2>Inbound</h2>",
		"commit",
		shortDigest(inbound),
		"<h2>Outbound</h2>",
		"tree",
		shortDigest(outbound),
		`tab=references`,
		`selected=` + inbound.String(),
	} {
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
			t.Fatalf("references tab missing %q: (%d, %.600q)", want, resp.StatusCode, page)
		}
	}
}

func TestObjectInspectorRendersZeroReferencesWithoutIndex(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	target := mustParse(t, "sha256:"+strings.Repeat("d4", 32))
	if err := srv.store.Put(ctx, target, bytes.NewReader(tlvEnvelope("blob@1", nil))); err != nil {
		t.Fatal(err)
	}
	viewer := login(t, ts, testStartupToken)

	resp, err := viewer.Get(ts.URL + "/viewer/objects?selected=" + target.String() + "&tab=references")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "<h2>Inbound</h2>") ||
		!strings.Contains(page, "No objects reference this object.") ||
		strings.Contains(page, "unavailable") {
		t.Fatalf("zero references = (%d, %.400q)", resp.StatusCode, body)
	}
}

func TestObjectsListAndRaw(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	h := mustParse(t, "sha256:"+strings.Repeat("cd", 32))
	if err := srv.store.Put(ctx, h, bytes.NewReader(tlvEnvelope("blob@1", []byte("object body")))); err != nil {
		t.Fatal(err)
	}

	written, err := srv.store.ModTime(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	viewer := login(t, ts, "viewer-tok")

	for _, request := range []struct {
		path string
		hx   bool
		want string
	}{
		{path: "/viewer/objects", want: h.String()},
		{path: "/viewer/objects?q=blob@1", hx: true, want: h.String()},
		{path: "/viewer/objects/" + h.String() + "/dump", want: "00000000"},
	} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+request.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if request.hx {
			req.Header.Set("HX-Request", "true")
		}
		resp, err := viewer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), request.want) {
			t.Fatalf("GET %s = (%d, %.200q), want 200 containing %q", request.path, resp.StatusCode, body, request.want)
		}
		if request.path == "/viewer/objects" {
			page := string(body)
			for _, want := range []string{
				"<!doctype html>",
				`<caption class="viewer-sr-only">Objects</caption>`,
				`for="q"`,
				`aria-sort="ascending"`,
				// A populated list auto-selects its first row.
				`class="viewer-selected" aria-current="true"`,
			} {
				if !strings.Contains(page, want) {
					t.Fatalf("object browser missing %q: %.400q", want, page)
				}
			}
		}
	}

	t.Run("selection page state", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(h.String()))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			`class="viewer-selected" aria-current="true"`,
			h.String(),
			"Metadata",
			"not-verified",
			formatTimestamp(written),
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("selection page missing %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
		}
	})

	t.Run("inspector tabs preserve selected row", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(h.String()) + "&tab=references")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			`class="viewer-selected" aria-current="true"`,
			`tab=references`,
			`aria-current="page"`,
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("inspector tab lost selected row or tab state %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
		}
		// The tabs are navigation links, not an ARIA tab widget: that pattern
		// promises arrow-key roving the viewer cannot implement without JS.
		for _, forbidden := range []string{`role="tab"`, `role="tablist"`, `aria-selected=`} {
			if strings.Contains(page, forbidden) {
				t.Fatalf("inspector tabs still claim the ARIA tab pattern (%q)", forbidden)
			}
		}
	})

	t.Run("inspector header offers a copy control", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(h.String()))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			`class="viewer-hash-row"`,
			`class="viewer-digest" type="text"`,
			`readonly`,
			`aria-label="Full object digest, select and copy"`,
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("inspector header missing %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
		}
	})

	t.Run("htmx inspector selection", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/viewer/objects?selected="+url.QueryEscape(h.String())+"&tab=bytes", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "object-inspector")
		req.Header.Set("X-Viewer-Selection", "true")
		resp, err := viewer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(page, "Loading bytes") ||
			!strings.Contains(page, `hx-trigger="revealed"`) || strings.Contains(page, "<!doctype html>") {
			t.Fatalf("inspector fragment = (%d, %.400q), want bytes-only fragment", resp.StatusCode, page)
		}
		if !strings.Contains(page, `id="object-list" hx-swap-oob="true"`) ||
			!strings.Contains(page, `hx-get="/viewer/objects?selected=`) ||
			!strings.Contains(page, `class="viewer-selected" aria-current="true"`) {
			t.Fatalf("inspector selection must swap the whole list with a selected refresh URL: %.400q", page)
		}
	})

	t.Run("actions panel respects role", func(t *testing.T) {
		admin := login(t, ts, testStartupToken)
		resp, err := admin.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(h.String()) + "&tab=metadata")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			"Verify",
			`hx-target="#integrity"`,
			`id="integrity"`,
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("admin actions panel missing %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
		}
		// Verification is the only action the inspector offers.
		if strings.Contains(page, "Delete") {
			t.Fatalf("inspector must not offer a delete action: %.400q", page)
		}
	})

	t.Run("metadata tab absorbs the actions panel", func(t *testing.T) {
		admin := login(t, ts, testStartupToken)
		// A stale Actions URL must land on Metadata rather than 400.
		page := getBody(t, admin, ts.URL+"/viewer/objects?selected="+url.QueryEscape(h.String())+"&tab=actions")
		if strings.Contains(page, ">Actions<") {
			t.Fatalf("inspector must no longer offer an Actions tab: %.400q", page)
		}
		if !strings.Contains(page, `hx-target="#integrity"`) || !strings.Contains(page, `<div id="integrity">`) {
			t.Fatalf("metadata tab must carry the verify form and its result panel: %.800q", page)
		}
	})

	resp, err := viewer.Get(ts.URL + "/viewer/objects/not-a-digest")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid object digest = %d, want 400", resp.StatusCode)
	}
}

// TestLargeObjectDetailAndRaw covers objects above the preview limit: the type
// is sniffed from the envelope header only, the detail page reports the real
// size, and the raw view says the preview is truncated.
func TestLargeObjectDetailAndRaw(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := tlvEnvelope("blob@1", bytes.Repeat([]byte("x"), previewLimit+10))
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := raw.Put(ctx, h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken, RoleTokens: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	resp, err := admin.Get(ts.URL + "/viewer/objects/" + h.String())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "blob@1") {
		t.Fatalf("large-object type missing from detail page: %.200q", page)
	}
	if want := formatBytes(int64(len(env))); !strings.Contains(string(page), want) {
		t.Fatalf("detail page does not report the formatted size %q: %.200q", want, page)
	}

	resp, err = admin.Get(ts.URL + "/viewer/objects/" + h.String() + "/dump")
	if err != nil {
		t.Fatal(err)
	}
	rawBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(rawBody), "preview truncated") {
		t.Fatalf("raw view does not mark the truncated preview: %.200q", rawBody)
	}
}

// TestColdObjectLinkSelectsInTheBrowser covers the cold-load route
// (viewer-design §3): a bookmarked object link opens the one object view the
// viewer has — the browser inspector — with that object selected.
func TestColdObjectLinkSelectsInTheBrowser(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := raw.Put(ctx, h, bytes.NewReader(tlvEnvelope("blob@1", []byte("cold")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	admin.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := admin.Get(ts.URL + "/viewer/objects/" + h.String())
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("cold object link = %d, want 303", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), "/viewer/objects?selected="+h.String(); got != want {
		t.Fatalf("cold object link redirects to %q, want %q", got, want)
	}
	admin.CheckRedirect = nil

	// An object that is not in the store has nothing to select.
	missing := mustParse(t, "sha256:"+strings.Repeat("cd", 32))
	if code := statusCode(t, admin, ts.URL+"/viewer/objects/"+missing.String()); code != http.StatusNotFound {
		t.Fatalf("cold link to an absent object = %d, want 404", code)
	}
}

// TestEveryRowCellCarriesTheSelectionLink guards the shared row-link
// attributes: every cell in an object row must be the same selecting link, so
// a cell that lost the htmx target or the selection header cannot pass
// unnoticed just because it still looks right.
func TestEveryRowCellCarriesTheSelectionLink(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := raw.Put(ctx, h, bytes.NewReader(tlvEnvelope("blob@1", []byte("row")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)

	page := getBody(t, login(t, ts, testStartupToken), ts.URL+"/viewer/objects")
	start := strings.Index(page, "<tbody")
	end := strings.Index(page, "</tbody>")
	if start < 0 || end < start {
		t.Fatalf("object table has no body: %.400q", page)
	}
	body := page[start:end]
	cells := strings.Count(body, "<td")
	for _, attr := range []string{
		`class="viewer-row-link"`,
		`hx-target="#object-inspector"`,
		`X-Viewer-Selection`,
		`hx-push-url="true"`,
	} {
		if got := strings.Count(body, attr); got != cells {
			t.Fatalf("row carries %q %d times, want once per cell (%d)", attr, got, cells)
		}
	}
}

// TestUnreadableObjectRowSaysSo guards the distinction between an object with
// no type and an object whose bytes could not be read: an empty type cell
// reads as the former, so the row must name the failure instead.
func TestUnreadableObjectRowSaysSo(t *testing.T) {
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.render(rec, "object-row", objectRow{
		Digest:     "sha256:" + strings.Repeat("cd", 32),
		Short:      "cdcdcdcd",
		Unreadable: true,
		SelectURL:  "/viewer/objects?selected=x",
	})
	body := rec.Body.String()
	if !strings.Contains(body, `<span class="viewer-unreadable">unreadable</span>`) {
		t.Fatalf("unreadable row must say so: %.400q", body)
	}

	rec = httptest.NewRecorder()
	srv.render(rec, "object-row", objectRow{
		Digest:    "sha256:" + strings.Repeat("ef", 32),
		Short:     "efefefef",
		TypeLabel: "blob@1",
		SelectURL: "/viewer/objects?selected=y",
	})
	if body := rec.Body.String(); !strings.Contains(body, "blob@1") || strings.Contains(body, "viewer-unreadable") {
		t.Fatalf("readable row must show its type: %.400q", body)
	}
}

// TestObjectListSnapshotFailureIsA500NotAPanic guards the fast path's error
// handling. When the metadata snapshot cannot be built the handler used to keep
// the default path's empty page and look the selection up in rows that were
// never produced, which panicked on a nil slice. A canceled request context is
// an error the store walk reports immediately, and the handler is called
// directly rather than through a server so a panic fails this test instead of
// being recovered and logged by net/http.
func TestObjectListSnapshotFailureIsA500NotAPanic(t *testing.T) {
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The default path must not hand back a page naming a row it never
	// produced: that invariant is what makes the caller's selection lookup
	// safe even though it runs after the error check.
	result, err := srv.defaultObjectPage(ctx, "", defaultObjectBrowserState())
	if err == nil {
		t.Fatal("canceled snapshot build must report an error")
	}
	if result.Page.Selected >= 0 || len(result.Rows) != 0 {
		t.Fatalf("failed default page = %+v, want no selection over no rows", result.Page)
	}

	rec := httptest.NewRecorder()
	srv.objects(rec, httptest.NewRequest(http.MethodGet, "/viewer/objects", nil).WithContext(ctx))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot failure = %d %.200q, want 500", rec.Code, rec.Body.String())
	}
}
