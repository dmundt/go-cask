package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

const testStartupToken = "AAAA-BBBB-CCCC"

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		RoleTokens: map[string]string{
			"viewer-tok":   RoleViewer,
			"operator-tok": RoleOperator,
			"admin-tok":    RoleAdmin,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// login performs the startup-token login and returns an authed client.
func login(t *testing.T, ts *httptest.Server, token string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	// Do not follow the 303 to the dashboard: the login response itself is
	// what carries the session cookie.
	c := &http.Client{Transport: ts.Client().Transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.PostForm(ts.URL+"/viewer/login", url.Values{"token": {token}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", resp.StatusCode)
	}
	c.CheckRedirect = nil // follow redirects from here on
	return c
}

func TestLoginFlow(t *testing.T) {
	ts, _ := newTestServer(t)

	// Unauthenticated viewer landing redirects to the login page.
	c := &http.Client{Transport: ts.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/viewer/login" {
		t.Fatalf("unauthenticated viewer landing = %d, location=%q, want 303 /viewer/login", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Wrong token → 401.
	resp, err = ts.Client().PostForm(ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", resp.StatusCode)
	}

	// Startup token → 303 + session cookie, then object browser renders.
	authClient := login(t, ts, testStartupToken)
	resp, err = authClient.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<caption>Objects</caption>`) {
		t.Fatalf("viewer landing = %d, %.80q", resp.StatusCode, body)
	}
}

func TestRoleTokensLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	// A configured role token logs in with that role: viewer-role session
	// cannot delete.
	viewer := login(t, ts, "viewer-tok")
	resp, err := viewer.Get(ts.URL + "/viewer/gc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer gc page = %d, want 403 empty", resp.StatusCode)
	}
}

func TestDirectTokenLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Transport: ts.Client().Transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := c.Get(ts.URL + "/viewer/?token=" + testStartupToken)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("direct token login status = %d, want 303", resp.StatusCode)
	}
	if resp.Header.Get("Location") != "/viewer/" {
		t.Fatalf("direct token redirect = %q, want /viewer/", resp.Header.Get("Location"))
	}

	resp, err = c.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<caption>Objects</caption>`) {
		t.Fatalf("authed viewer landing = %d, %.80q", resp.StatusCode, body)
	}
}

func TestLoginThrottle(t *testing.T) {
	ts, _ := newTestServer(t)
	for i := 0; i < 5; i++ {
		resp, err := ts.Client().PostForm(ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	resp, err := ts.Client().PostForm(ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th failed login = %d, want 429", resp.StatusCode)
	}
}

func TestCSRFEnforced(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := login(t, ts, testStartupToken)

	// A POST mutation without the CSRF token → 403.
	resp, err := admin.PostForm(ts.URL+"/viewer/gc", url.Values{"roots": {"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("gc without csrf = %d, want 403", resp.StatusCode)
	}
}

func TestVerifyAndDelete(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := raw.Put(ctx, h, strings.NewReader("view me")); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken, RoleTokens: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)

	admin := login(t, ts, testStartupToken)

	// Object detail renders with the full digest (bare hex, no algorithm name).
	resp, err := admin.Get(ts.URL + "/viewer/objects/" + h.String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), h.String()) {
		t.Fatalf("detail missing hash: %.80q", body)
	}

	// Verify with CSRF (grab the token from the page).
	csrf := csrfFromPage(string(body))
	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+h.String()+"/verify",
		url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify = %d, want 200", resp.StatusCode)
	}

	// Delete with CSRF.
	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+h.String()+"/delete",
		url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d, want 200", resp.StatusCode)
	}
}

func TestStatic(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, asset := range []struct {
		path        string
		contentType string
		want        string
	}{
		{"/viewer/static/htmx.min.js", "application/javascript", "htmx"},
		{"/viewer/static/viewer.css", "text/css", ".viewer-shell"},
	} {
		resp, err := ts.Client().Get(ts.URL + asset.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", asset.path, resp.StatusCode)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), asset.contentType) {
			t.Fatalf("%s content type = %q, want prefix %q", asset.path, resp.Header.Get("Content-Type"), asset.contentType)
		}
		if !strings.Contains(string(body), asset.want) {
			t.Fatalf("%s did not contain %q", asset.path, asset.want)
		}
	}
}

func TestShellIsOnlyDocumentOwner(t *testing.T) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	var documentTypes, htmlTags, headTags, bodyTags int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		source := string(content)
		documentTypes += strings.Count(source, "<!doctype html>")
		htmlTags += strings.Count(source, "<html")
		headTags += strings.Count(source, "<head>")
		bodyTags += strings.Count(source, "<body")
	}
	if documentTypes != 1 || htmlTags != 1 || headTags != 1 || bodyTags != 1 {
		t.Fatalf(
			"document chrome counts = doctype:%d html:%d head:%d body:%d, want exactly one shell",
			documentTypes,
			htmlTags,
			headTags,
			bodyTags,
		)
	}
}

func TestObjectsListAndRaw(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	h := mustParse(t, "sha256:"+strings.Repeat("cd", 32))
	if err := srv.store.Put(ctx, h, bytes.NewReader(tlvEnvelope("blob@1", []byte("object body")))); err != nil {
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
		{path: "/viewer/objects/" + h.String() + "/raw", want: "00000000"},
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
				`<caption>Objects</caption>`,
				`for="q"`,
				`aria-sort="ascending"`,
				"Select an object to inspect it.",
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
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("selection page missing %q: (%d, %.400q)", want, resp.StatusCode, page)
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
	})

	t.Run("actions panel respects role", func(t *testing.T) {
		admin := login(t, ts, testStartupToken)
		resp, err := admin.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(h.String()) + "&tab=actions")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			"Verify",
			"Delete",
			`hx-target="#integrity"`,
			`id="integrity"`,
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("admin actions panel missing %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
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

func TestObjectBrowserQueryState(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		typeName := "blob@1"
		if i == 0 {
			typeName = "note@1"
		}
		data := tlvEnvelope(typeName, bytes.Repeat([]byte{byte(i)}, i+1))
		digest := sha256.Of(data)
		if err := srv.store.Put(ctx, digest, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	viewer := login(t, ts, "viewer-tok")

	t.Run("filter and first page", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?type=blob%401&limit=25")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("filtered list = %d, want 200", resp.StatusCode)
		}
		page := string(body)
		if !strings.Contains(page, "1–25 of 29") {
			t.Fatalf("first page summary missing: %.400q", page)
		}
		if !strings.Contains(page, "type=blob%401") || !strings.Contains(page, "offset=25") {
			t.Fatalf("next pager did not retain query state: %.400q", page)
		}
	})

	t.Run("last page", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?type=blob%401&limit=25&offset=25")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "26–29 of 29") {
			t.Fatalf("last page = (%d, %.400q), want final summary", resp.StatusCode, body)
		}
	})

	t.Run("empty and out of range pages", func(t *testing.T) {
		for _, test := range []struct {
			query string
			want  string
		}{
			{query: "q=missing", want: "0–0 of 0"},
			{query: "type=blob%401&limit=25&offset=100", want: "0–0 of 29"},
		} {
			resp, err := viewer.Get(ts.URL + "/viewer/objects?" + test.query)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), test.want) {
				t.Fatalf("page %q = (%d, %.400q), want %q", test.query, resp.StatusCode, body, test.want)
			}
		}
	})

	t.Run("sort and filter controls", func(t *testing.T) {
		resp, err := viewer.Get(ts.URL + "/viewer/objects?sort=size&dir=desc&offset=25")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		page := string(body)
		for _, want := range []string{
			`aria-sort="descending"`,
			`name="sort" type="hidden" value="size"`,
			`name="dir" type="hidden" value="desc"`,
		} {
			if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
				t.Fatalf("sort controls missing %q: (%d, %.400q)", want, resp.StatusCode, page)
			}
		}
		if strings.Contains(page, `name="offset"`) {
			t.Fatalf("filter form must reset pagination: %.400q", page)
		}
	})

	t.Run("invalid query", func(t *testing.T) {
		for _, rawQuery := range []string{
			"limit=10",
			"offset=-1",
			"sort=written",
			"dir=sideways",
			"size=huge",
			"status=orphaned",
			"selected=not-a-digest",
			"tab=references",
			"type=missing%401",
			"q=one&q=two",
		} {
			resp, err := viewer.Get(ts.URL + "/viewer/objects?" + rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("query %q = %d, want 400", rawQuery, resp.StatusCode)
			}
		}
	})
}

func TestSortObjectRows(t *testing.T) {
	rows := []objectRow{
		{Digest: "b", Type: "tree@1", Size: 12},
		{Digest: "a", Type: "blob@1", Size: 4},
		{Digest: "c", Type: "blob@1", Size: 8},
	}
	for _, test := range []struct {
		state objectBrowserState
		want  string
	}{
		{state: objectBrowserState{Sort: "hash", Direction: "asc"}, want: "abc"},
		{state: objectBrowserState{Sort: "type", Direction: "desc"}, want: "bca"},
		{state: objectBrowserState{Sort: "size", Direction: "asc"}, want: "acb"},
	} {
		sorted := append([]objectRow(nil), rows...)
		sortObjectRows(sorted, test.state)
		var got strings.Builder
		for _, row := range sorted {
			got.WriteString(row.Digest)
		}
		if got.String() != test.want {
			t.Fatalf("sort %+v = %q, want %q", test.state, got.String(), test.want)
		}
	}
}

func mustParse(t *testing.T, s string) cas.Digest {
	t.Helper()
	h, err := sha256.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestGCPageShipsCSRF covers the GC mutation end to end: the maintenance form
// must carry a CSRF token, or every submit is rejected with 403.
func TestGCPageShipsCSRF(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := login(t, ts, testStartupToken)

	resp, err := admin.Get(ts.URL + "/viewer/gc")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := csrfFromPage(string(page))
	if csrf == "" {
		t.Fatalf("gc page has no CSRF field: %.200q", page)
	}

	roots := "sha256:" + strings.Repeat("ab", 32)
	resp, err = admin.PostForm(ts.URL+"/viewer/gc", url.Values{"roots": {roots}, "csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gc with CSRF = %d, want 200 (body %.120q)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "gc: deleted") {
		t.Fatalf("gc result missing: %.120q", body)
	}
}

// TestLoginRejectsEmptyToken pins the fail-closed rule: an empty submitted
// token never authenticates, even if a role token was misconfigured as "".
func TestLoginRejectsEmptyToken(t *testing.T) {
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		RoleTokens:   map[string]string{"": RoleAdmin},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().PostForm(ts.URL+"/viewer/login", url.Values{"token": {""}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("empty-token login = %d, want 401", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("empty-token login set cookies: %v", resp.Cookies())
	}
}

// tlvEnvelope builds a TLV envelope (cas-core §8 decision 1) for viewer tests.
func tlvEnvelope(typeName string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(1) // envelopeVersion
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	return buf.Bytes()
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
	if want := fmt.Sprintf("%d bytes", len(env)); !strings.Contains(string(page), want) {
		t.Fatalf("detail page does not report the real size %q: %.200q", want, page)
	}

	resp, err = admin.Get(ts.URL + "/viewer/objects/" + h.String() + "/raw")
	if err != nil {
		t.Fatal(err)
	}
	rawBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(rawBody), "preview truncated") {
		t.Fatalf("raw view does not mark the truncated preview: %.200q", rawBody)
	}
}

// TestThrottleExponentialBackoff pins the backoff growth: each consecutive
// exhaustion doubles the block window.
func TestThrottleExponentialBackoff(t *testing.T) {
	const window = 50 * time.Millisecond
	th := newThrottle(2, window)
	for i := 0; i < 2; i++ {
		if !th.allow("ip") {
			t.Fatalf("attempt %d blocked before the budget was spent", i+1)
		}
	}
	if th.allow("ip") {
		t.Fatal("third attempt must be blocked")
	}
	time.Sleep(window + 10*time.Millisecond)
	// The block expired after one window: two more attempts are allowed.
	if !th.allow("ip") || !th.allow("ip") {
		t.Fatal("attempts after the first backoff must be allowed")
	}
	if th.allow("ip") {
		t.Fatal("budget must be exhausted again")
	}
	// The second exhaustion doubles the backoff, so one window is not enough.
	time.Sleep(window + 10*time.Millisecond)
	if th.allow("ip") {
		t.Fatal("second backoff must outlast one window")
	}
	time.Sleep(window + 10*time.Millisecond)
	if !th.allow("ip") {
		t.Fatal("attempt after the doubled backoff must be allowed")
	}
}

// TestThrottleConcurrentBudget pins the single-critical-section behavior: a
// burst of concurrent attempts cannot slip past the failure budget.
func TestThrottleConcurrentBudget(t *testing.T) {
	th := newThrottle(5, time.Minute)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if th.allow("ip") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Fatalf("allowed %d concurrent attempts, want exactly 5", got)
	}
}

func TestSessionAndRoleHelpers(t *testing.T) {
	t.Run("roleAllows", func(t *testing.T) {
		if !roleAllows(RoleAdmin, RoleViewer) {
			t.Fatal("admin should satisfy viewer")
		}
		if roleAllows(RoleViewer, RoleAdmin) {
			t.Fatal("viewer should not satisfy admin")
		}
	})

	t.Run("resolveToken", func(t *testing.T) {
		srv, err := New(nil, Config{StartupToken: testStartupToken, RoleTokens: map[string]string{"viewer-tok": RoleViewer}})
		if err != nil {
			t.Fatal(err)
		}
		if role, ok := srv.resolveToken(testStartupToken); !ok || role != RoleAdmin {
			t.Fatalf("resolveToken startup token = (%q, %v), want (admin, true)", role, ok)
		}
		if role, ok := srv.resolveToken("viewer-tok"); !ok || role != RoleViewer {
			t.Fatalf("resolveToken role token = (%q, %v), want (viewer, true)", role, ok)
		}
		if _, ok := srv.resolveToken(""); ok {
			t.Fatal("empty token must not resolve")
		}
	})

	t.Run("csrf and session cookie helpers", func(t *testing.T) {
		sess := &Session{ID: "abc", CSRF: "csrf-token"}
		req := httptest.NewRequest(http.MethodPost, "/viewer/gc", strings.NewReader(url.Values{"csrf": {"csrf-token"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if !csrfOK(req, sess) {
			t.Fatal("csrfOK accepted matching token")
		}
		if csrfOK(req, &Session{CSRF: "other"}) {
			t.Fatal("csrfOK should reject mismatched token")
		}
		rec := httptest.NewRecorder()
		setSessionCookie(rec, sess)
		if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != sessionCookie || c[0].Value != "abc" ||
			!c[0].Secure || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
			t.Fatalf("setSessionCookie = %#v, want one secure session cookie", c)
		}
		rec = httptest.NewRecorder()
		clearSessionCookie(rec)
		if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != sessionCookie || c[0].MaxAge != -1 ||
			!c[0].Secure || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
			t.Fatalf("clearSessionCookie = %#v, want one expired secure session cookie", c)
		}
		if got := sessionID(req); got != "" {
			t.Fatalf("sessionID without cookie = %q, want empty", got)
		}
	})

	t.Run("session scoped verification", func(t *testing.T) {
		sessions := newSessions()
		first, err := sessions.create(RoleViewer)
		if err != nil {
			t.Fatal(err)
		}
		second, err := sessions.create(RoleViewer)
		if err != nil {
			t.Fatal(err)
		}
		if got := sessions.verification(first.ID, "digest"); got != "not-verified" {
			t.Fatalf("initial verification = %q, want not-verified", got)
		}
		sessions.setVerification(first.ID, "digest", "verified")
		if got := sessions.verification(first.ID, "digest"); got != "verified" {
			t.Fatalf("first session verification = %q, want verified", got)
		}
		if got := sessions.verification(second.ID, "digest"); got != "not-verified" {
			t.Fatalf("second session verification = %q, want not-verified", got)
		}
	})
}

func csrfFromPage(page string) string {
	// <input type="hidden" name="csrf" value="...">
	idx := strings.Index(page, `name="csrf" value="`)
	if idx < 0 {
		return ""
	}
	rest := page[idx+len(`name="csrf" value="`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
