package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
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

type testReferenceIndex struct {
	inbound  map[string][]cas.Digest
	outbound map[string][]cas.Digest
}

func newTestReferenceIndex() *testReferenceIndex {
	return &testReferenceIndex{
		inbound:  make(map[string][]cas.Digest),
		outbound: make(map[string][]cas.Digest),
	}
}

func (i *testReferenceIndex) Record(source cas.Digest, targets []cas.Digest) {
	i.outbound[source.String()] = append([]cas.Digest(nil), targets...)
	for _, target := range targets {
		i.inbound[target.String()] = append(i.inbound[target.String()], source)
	}
}

func (i *testReferenceIndex) Inbound(target cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.inbound[target.String()]...)
}

func (i *testReferenceIndex) Outbound(source cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.outbound[source.String()]...)
}

type testReachabilityIndex map[string]bool

func (i testReachabilityIndex) IsReachable(digest cas.Digest) bool {
	return i[digest.String()]
}

func TestFormatWrittenAt(t *testing.T) {
	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		written time.Time
		want    string
	}{
		{name: "missing", want: ""},
		{name: "minutes", written: now.Add(-59 * time.Minute), want: "59m ago"},
		{name: "hours", written: now.Add(-23 * time.Hour), want: "23h ago"},
		{name: "day", written: now.Add(-24 * time.Hour), want: "1d ago"},
		{name: "days", written: now.Add(-72 * time.Hour), want: "3d ago"},
		{name: "future", written: now.Add(time.Hour), want: "0m ago"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatWrittenAt(test.written, now); got != test.want {
				t.Errorf("formatWrittenAt() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	timestamp := time.Date(2026, time.September, 21, 12, 10, 48, 123456789, time.FixedZone("UTC+2", 2*60*60))
	if got, want := formatTimestamp(timestamp), "2026-09-21T10:10:48.123456789Z"; got != want {
		t.Errorf("formatTimestamp() = %q, want %q", got, want)
	}
	if got := formatTimestamp(time.Time{}); got != "" {
		t.Errorf("formatTimestamp(zero) = %q, want empty", got)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1 KiB"},
		{1536, "1.5 KiB"},
		{10 * 1024, "10 KiB"},
		{1024 * 1024, "1 MiB"},
		{128 << 20, "128 MiB"},
		{1 << 30, "1 GiB"},
	}
	for _, test := range tests {
		if got := formatBytes(test.size); got != test.want {
			t.Errorf("formatBytes(%d) = %q, want %q", test.size, got, test.want)
		}
	}
}

func TestShortDigest(t *testing.T) {
	digest, err := cas.ParseDigest(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if got := shortDigest(digest); got != "abababab…" {
		t.Errorf("shortDigest() = %q, want %q", got, "abababab…")
	}
}

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
	// Do not follow the 303 to the object browser: the login response itself is
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
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<caption class="viewer-sr-only">Objects</caption>`) {
		t.Fatalf("viewer landing = %d, %.80q", resp.StatusCode, body)
	}
}

func TestDashboardRouteRemoved(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/viewer/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("dashboard route = %d, want 404", resp.StatusCode)
	}
}

func TestRoleTokensLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	viewer := login(t, ts, "viewer-tok")
	resp, err := viewer.Get(ts.URL + "/viewer/gc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("removed gc page = %d, want 404", resp.StatusCode)
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
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<caption class="viewer-sr-only">Objects</caption>`) {
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

func TestRemovedGCPostReturnsNotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := login(t, ts, testStartupToken)

	resp, err := admin.PostForm(ts.URL+"/viewer/gc", url.Values{"roots": {"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("removed gc post = %d, want 404", resp.StatusCode)
	}
}

func TestVerifyAndDeleteRemoved(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := raw.Put(ctx, h, strings.NewReader("view me")); err != nil {
		t.Fatal(err)
	}
	references := newTestReferenceIndex()
	references.Record(h, []cas.Digest{mustParse(t, "sha256:"+strings.Repeat("cd", 32))})
	srv, err := New(raw, Config{StartupToken: testStartupToken, RoleTokens: map[string]string{}, References: references})
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

	// The viewer inspects; it does not destroy. The route is gone, so even an
	// admin with a valid CSRF token cannot reach it.
	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+h.String()+"/delete",
		url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete = %d, want 404", resp.StatusCode)
	}
	rc, err := raw.Get(ctx, h)
	if err != nil {
		t.Fatalf("object must survive a delete attempt: %v", err)
	}
	rc.Close()
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

func TestObjectBrowserDividerMarkup(t *testing.T) {
	content, err := templateFS.ReadFile("templates/objects.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, unwanted := range []string{"data-viewer-divider", `role="separator"`, "data-viewer-workspace"} {
		if strings.Contains(source, unwanted) {
			t.Errorf("objects template still carries the scripted divider markup %q", unwanted)
		}
	}
	// The inspector resizes natively, so the bounds live in CSS rather than in
	// a script that has to enforce them.
	for _, want := range []string{"resize: horizontal", "min-width: 280px", "max-width: 560px"} {
		if !strings.Contains(string(viewerCSS), want) {
			t.Errorf("viewer stylesheet does not contain %q", want)
		}
	}
}

func TestTopBarShowsBuildVersion(t *testing.T) {
	// The viewer identifies the running binary, so an operator reading a bug
	// report can tell which build produced the page. It is the same string the
	// CLI prints: both call Version.
	if Version() == "" {
		t.Fatal("Version must never be empty")
	}
	ts, _ := newTestServer(t)
	viewer := login(t, ts, testStartupToken)
	body := getBody(t, viewer, ts.URL+"/viewer/objects")
	want := `<span class="viewer-version">` + Version() + `</span>`
	if !strings.Contains(body, want) {
		t.Errorf("top bar does not render the build version %q", want)
	}
}

func TestRowLinkInsetMatchesCellPadding(t *testing.T) {
	// The row link fills its cell through a negative margin. When its inset is
	// wider than the cell padding it overhangs the outer columns, and the table
	// grows a horizontal scrollbar for a few phantom pixels. The values are a
	// taste choice, so read them from the cell rules and require only that the
	// link mirrors whatever they say.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	read := func(pattern string) string {
		m := regexp.MustCompile(pattern).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("cell padding rule not found: %s", pattern)
		}
		return m[1]
	}
	inline := read(`\.viewer-table th,\n\.viewer-table td \{\n  padding: 8px (\d+px);`)
	left := read(`tr > :first-child \{\n  padding-left: (\d+px);`)
	right := read(`tr > :last-child \{\n  padding-right: (\d+px);`)
	mirrors := []string{
		"  margin: -8px -" + inline + ";\n  padding: 8px " + inline + ";",
		"  margin-left: -" + left + ";\n  padding-left: " + left + ";",
		"  margin-right: -" + right + ";\n  padding-right: " + right + ";",
	}
	for _, mirror := range mirrors {
		if !strings.Contains(css, mirror) {
			t.Errorf("row link inset no longer mirrors the cell padding: %q", mirror)
		}
	}
}

func TestInspectorDigestFontFitsFullAddress(t *testing.T) {
	// The readonly digest field replaces the copy button, so the whole 64-char
	// address has to fit the inspector.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	if !strings.Contains(css, ".viewer-digest {\n  flex: 1;") {
		t.Error("digest rule is missing")
	}
	if !strings.Contains(css, "font: 11px/1.45 var(--viewer-mono);") {
		t.Error("digest field no longer uses the reduced 11px mono font")
	}
}

func TestControlFontResetCannotBeatComponentRules(t *testing.T) {
	// The control font reset normalises the UA font onto the shell font, but it
	// must not decide the size: a plain `.viewer-shell button` selector outranks
	// every single-class component rule, so a control declaring its own size
	// silently kept the 15.4px body font. :where() drops the reset to zero
	// specificity, which is what lets the type scale below apply at all.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	if strings.Contains(css, ".viewer-shell button,\n.viewer-shell input,") {
		t.Error("control font reset is specificity-bearing again and will override component font sizes")
	}
	if !strings.Contains(css, ":where(.viewer-shell) button,\n:where(.viewer-shell) input,") {
		t.Error("control font reset is no longer wrapped in :where()")
	}
	// Same trap, different shorthand: `.viewer-meta { margin: 0 }` is declared
	// after the result panel's rules and ties them on specificity, so the
	// panel's top inset only survives with the extra qualifier.
	if !strings.Contains(css, ".viewer-result .viewer-result-meta {\n  margin-top:") {
		t.Error("result meta inset no longer out-specifies the .viewer-meta margin reset")
	}
	// The two secondary notes in the inspector — the empty-references line and
	// the hexdump truncation line — read as the same aside, so they share one
	// rule rather than drifting apart.
	if !strings.Contains(css, ".viewer-reference-empty,\n.viewer-hexdump-note {") {
		t.Error("inspector notes no longer share a single font rule")
	}
}

func TestInteractiveControlsUseTheTypeScale(t *testing.T) {
	// Every control is sized from one of the three scale steps. A control that
	// declares no font inherits the 15.4px body size, which is set for prose and
	// dwarfs a 28px control — that is the bug this guards.
	css := strings.ReplaceAll(string(viewerCSS), "\r\n", "\n")
	for _, token := range []string{"--viewer-control:", "--viewer-control-sm:", "--viewer-control-xs:"} {
		if !strings.Contains(css, token) {
			t.Errorf("control type scale is missing %s", token)
		}
	}
	// Each rule below styles an interactive control, so each must name a step.
	controls := []string{
		".viewer-filter-bar input,\n.viewer-filter-bar select,\n.viewer-filter-bar button,\n.viewer-action,\n.viewer-pager a",
		".viewer-verify-all",
		".viewer-reset",
		".viewer-sort-button",
		".viewer-pager a,\n.viewer-pager select,\n.viewer-page-button",
		".viewer-pager label",
		".viewer-inspector-tabs",
		".viewer-history-step",
	}
	scale := regexp.MustCompile(`font(?:-size)?: [^;]*var\(--viewer-control(?:-sm|-xs)?\)`)
	for _, selector := range controls {
		start := strings.Index(css, selector+" {")
		if start < 0 {
			t.Errorf("control rule not found: %q", selector)
			continue
		}
		end := strings.Index(css[start:], "}")
		if end < 0 || !scale.MatchString(css[start:start+end]) {
			t.Errorf("control %q does not size itself from the type scale", selector)
		}
	}
	// One height across the viewer: a control that stands taller than the row
	// it sits in reads as a different kind of control than it is.
	heights := []string{
		".viewer-filter-bar input,\n.viewer-filter-bar select,\n.viewer-filter-bar button,\n.viewer-action,\n.viewer-pager a",
		".viewer-verify-all",
		".viewer-reset",
		".viewer-pager a,\n.viewer-pager select,\n.viewer-page-button",
	}
	for _, selector := range heights {
		start := strings.Index(css, selector+" {")
		if start < 0 {
			t.Errorf("control rule not found: %q", selector)
			continue
		}
		end := strings.Index(css[start:], "}")
		if end < 0 || !strings.Contains(css[start:start+end], "height: 28px;") {
			t.Errorf("control %q does not use the shared 28px height", selector)
		}
	}
}

func TestViewerShipsNoOwnScript(t *testing.T) {
	// htmx is the only script the viewer serves: every other affordance is
	// server-rendered hypermedia or CSS.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") && entry.Name() != "htmx.min.js" {
			t.Errorf("viewer embeds an extra script %q", entry.Name())
		}
	}
	ts, _ := newTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/viewer/static/viewer.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// The route is gone, so the path falls through to the viewer catch-all and
	// can no longer answer as a script.
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/javascript") {
		t.Fatalf("viewer.js still serves a script: %q", resp.Header.Get("Content-Type"))
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

func TestObjectRawIsLimitedTo256Bytes(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte{0xab}, previewLimit+1)
	h := mustParse(t, "sha256:"+strings.Repeat("ef", 32))
	if err := srv.store.Put(ctx, h, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	viewer := login(t, ts, "viewer-tok")
	resp, err := viewer.Get(ts.URL + "/viewer/objects/" + h.String() + "/raw")
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

func TestFilterDroppingSelectionSwapsInspector(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blob := tlvEnvelope("blob@1", []byte("blob"))
	tree := tlvEnvelope("tree@1", []byte("tree"))
	blobDigest, treeDigest := sha256.Of(blob), sha256.Of(tree)
	for payload, digest := range map[string]cas.Digest{string(blob): blobDigest, string(tree): treeDigest} {
		if err := raw.Put(ctx, digest, strings.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// The blob is selected, then a type filter drops it. htmx swaps the list
	// only, so the fallback selection reaches the screen just in case the
	// response carries the inspector out of band.
	target := ts.URL + "/viewer/objects?selected=" + url.QueryEscape(blobDigest.String()) + "&type=tree%401"
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "object-list")
	resp, err := viewer.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filtered list = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(page, `id="object-inspector" hx-swap-oob="true"`) {
		t.Fatalf("list swap must carry the inspector out of band: %.900q", page)
	}
	if !strings.Contains(page, `value="`+treeDigest.String()+`"`) {
		t.Fatalf("inspector must show the surviving row %s: %.900q", treeDigest, page)
	}
	if strings.Contains(page, blobDigest.String()) {
		t.Fatalf("filtered-out object must not linger in the inspector: %.900q", page)
	}
}

func TestOrphanedObjectStateRequiresReachability(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reachable := sha256.Of([]byte("reachable"))
	orphaned := sha256.Of([]byte("orphaned"))
	for _, digest := range []cas.Digest{reachable, orphaned} {
		if err := raw.Put(ctx, digest, bytes.NewReader([]byte(digest.String()))); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		Reachability: testReachabilityIndex{reachable.String(): true},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	resp, err := viewer.Get(ts.URL + "/viewer/objects?reach=orphaned")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), shortDigest(orphaned)) ||
		strings.Contains(string(body), shortDigest(reachable)) || !strings.Contains(string(body), `value="orphaned" selected`) {
		t.Fatalf("orphaned filter = (%d, %.400q)", resp.StatusCode, body)
	}

	unconfigured, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	unconfiguredServer := httptest.NewTLSServer(unconfigured.Handler())
	t.Cleanup(unconfiguredServer.Close)
	unconfiguredViewer := login(t, unconfiguredServer, testStartupToken)
	resp, err = unconfiguredViewer.Get(unconfiguredServer.URL + "/viewer/objects?reach=orphaned")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfigured orphaned filter = %d, want 400", resp.StatusCode)
	}

	// Integrity is one axis with exclusive states.
	corrupt := getBody(t, viewer, ts.URL+"/viewer/objects?status=corrupt")
	if strings.Contains(corrupt, shortDigest(orphaned)) || strings.Contains(corrupt, shortDigest(reachable)) {
		t.Fatalf("unverified objects must not match status=corrupt: %.800q", corrupt)
	}
	// Reachability is the other axis, so it narrows the integrity match.
	narrowed := getBody(t, viewer, ts.URL+"/viewer/objects?status=not-verified&reach=orphaned")
	if !strings.Contains(narrowed, shortDigest(orphaned)) || strings.Contains(narrowed, shortDigest(reachable)) {
		t.Fatalf("reachability filter must narrow the integrity match: %.800q", narrowed)
	}
	// Reachability is no longer an integrity state.
	if code := statusCode(t, viewer, ts.URL+"/viewer/objects?status=orphaned"); code != http.StatusBadRequest {
		t.Fatalf("orphaned as an integrity state = %d, want 400", code)
	}
	if code := statusCode(t, viewer, ts.URL+"/viewer/objects?reach=bogus"); code != http.StatusBadRequest {
		t.Fatalf("unknown reachability = %d, want 400", code)
	}
}

func TestEmptyObjectListKeepsInspectorEmpty(t *testing.T) {
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	page := getBody(t, viewer, ts.URL+"/viewer/objects")
	if !strings.Contains(page, "Select an object to inspect it.") {
		t.Fatalf("an empty store has nothing to auto-select: %.400q", page)
	}
	if strings.Contains(page, `class="viewer-selected"`) {
		t.Fatalf("an empty store must not mark a selected row: %.400q", page)
	}
}

func TestInspectorTrailStepsThroughVisitedObjects(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	digests := make([]string, 0, 3)
	for _, word := range []string{"alpha", "beta", "gamma"} {
		payload := []byte(word)
		h := sha256.Of(payload)
		if err := raw.Put(ctx, h, bytes.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
		digests = append(digests, h.String())
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// A single visit has nowhere to step, so both controls render inert.
	first := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[0]))
	if !strings.Contains(first, `<span class="viewer-history-step" aria-disabled="true"`) {
		t.Fatalf("a fresh trail must disable both steps: %.800q", first)
	}
	if strings.Contains(first, `<a class="viewer-history-step"`) {
		t.Fatalf("a fresh trail must offer no step link: %.800q", first)
	}

	getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[1])+"&nav=ref")
	second := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[2])+"&nav=ref")
	if !strings.Contains(second, "selected="+url.QueryEscape(digests[1])+"&amp;nav=trail") {
		t.Fatalf("the previous step must point at the previously visited object: %.800q", second)
	}

	// Stepping back exposes the forward step without extending the trail.
	back := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[1])+"&nav=trail")
	if !strings.Contains(back, "selected="+url.QueryEscape(digests[0])+"&amp;nav=trail") {
		t.Fatalf("stepping back must keep walking backwards: %.800q", back)
	}
	if !strings.Contains(back, "selected="+url.QueryEscape(digests[2])+"&amp;nav=trail") {
		t.Fatalf("stepping back must expose the forward step: %.800q", back)
	}

	// Picking a row in the table is a new point of departure, so the chain the
	// operator just abandoned must not remain reachable.
	picked := getBody(t, viewer, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digests[2]))
	if strings.Contains(picked, `<a class="viewer-history-step"`) {
		t.Fatalf("selecting a row must reset the trail: %.800q", picked)
	}
}

func TestClickingSelectedRowEmptiesInspector(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("toggle")
	if err := raw.Put(ctx, sha256.Of(payload), bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// The auto-selected row links at its own deselected state, and following
	// that link must leave the inspector empty instead of re-selecting it.
	page := getBody(t, viewer, ts.URL+"/viewer/objects")
	if !strings.Contains(page, `href="/viewer/objects?selected="`) {
		t.Fatalf("the selected row must link at the deselected state: %.800q", page)
	}
	cleared := getBody(t, viewer, ts.URL+"/viewer/objects?selected=")
	if !strings.Contains(cleared, "Select an object to inspect it.") {
		t.Fatalf("an explicit deselect must empty the inspector: %.800q", cleared)
	}
	if strings.Contains(cleared, `class="viewer-selected"`) {
		t.Fatalf("an explicit deselect must leave no row marked: %.800q", cleared)
	}
}

func TestFilteredOutSelectionFallsBackToFirstRow(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := sha256.Of([]byte("kept"))
	if err := raw.Put(ctx, kept, bytes.NewReader([]byte("kept"))); err != nil {
		t.Fatal(err)
	}
	dropped := sha256.Of([]byte("dropped"))
	if err := raw.Put(ctx, dropped, bytes.NewReader(tlvEnvelope("blob@1", []byte("dropped")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	viewer := login(t, ts, testStartupToken)

	// The type filter excludes the selected object, so the inspector must fall
	// back to a visible row instead of rendering nothing.
	page := getBody(t, viewer, ts.URL+"/viewer/objects?type=blob@1&selected="+url.QueryEscape(kept.String()))
	if strings.Contains(page, "Select an object to inspect it.") {
		t.Fatalf("a filtered-out selection must fall back to the first row: %.800q", page)
	}
	if !strings.Contains(page, `class="viewer-selected" aria-current="true"`) {
		t.Fatalf("the fallback row must render as selected: %.800q", page)
	}
}

func statusCode(t *testing.T, client *http.Client, target string) int {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestIntegrityAndReachabilityAreIndependentColumns(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Stored bytes deliberately do not hash to their address, so Verify fails.
	orphaned := sha256.Of([]byte("orphaned-corrupt"))
	if err := raw.Put(ctx, orphaned, bytes.NewReader(tlvEnvelope("blob@1", []byte("tampered")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{
		StartupToken: testStartupToken,
		Reachability: testReachabilityIndex{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	resp, err := admin.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(orphaned.String()) + "&tab=metadata")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "viewer-status-orphaned") {
		t.Fatalf("unverified orphan must render as orphaned: %.400q", page)
	}
	if !strings.Contains(string(page), "viewer-status-not-verified") {
		t.Fatalf("orphan status column must report integrity, not reachability: %.400q", page)
	}
	// Verify must stay available for orphans: they are the likeliest to rot.
	if !strings.Contains(string(page), "/verify") {
		t.Fatalf("orphan actions panel must still offer Verify: %.400q", page)
	}
	csrf := csrfFromPage(string(page))
	if csrf == "" {
		t.Fatalf("actions page has no CSRF token: %.400q", page)
	}

	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+orphaned.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "corrupt") {
		t.Fatalf("verify = (%d, %.200q), want corrupt result", resp.StatusCode, body)
	}

	resp, err = admin.Get(ts.URL + "/viewer/objects")
	if err != nil {
		t.Fatal(err)
	}
	page, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "viewer-status-corrupt") {
		t.Fatalf("integrity column must report the corrupt integrity result: %.400q", page)
	}
	// The two axes are independent columns, so the orphan pill survives a
	// corrupt integrity result instead of being hidden behind it.
	if !strings.Contains(string(page), "viewer-status-orphaned") {
		t.Fatalf("a corrupt orphan must still show its orphan pill: %.400q", page)
	}
	// Each axis owns a column of its own: "Status" used to carry both, which
	// made Orphaned read as an integrity verdict.
	for _, want := range []string{">Integrity<", ">References<"} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("object table is missing the %s column header: %.900q", want, page)
		}
	}
	if strings.Contains(string(page), "viewer-status-extra") {
		t.Fatalf("the two axes must not share one cell any more: %.400q", page)
	}
	// The non-orphan verdict reads as "Resolved": "Reachable" invited the
	// reading that an object with no inbound references cannot be reachable,
	// when a root has none by definition.
	if !strings.Contains(string(page), ">Resolved<") || strings.Contains(string(page), ">Reachable<") {
		t.Fatalf("reference column must label the sound state Resolved: %.900q", page)
	}
	// The reference column sorts like every other data column.
	if !strings.Contains(string(page), "sort=reach") {
		t.Fatalf("reference column header must offer a sort link: %.1500q", page)
	}

	// Both facts stay independently filterable, each on its own axis.
	for _, query := range []string{"status=corrupt", "reach=orphaned"} {
		resp, err = admin.Get(ts.URL + "/viewer/objects?" + query)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), shortDigest(orphaned)) {
			t.Fatalf("%s must match the corrupt orphan: (%d, %.300q)", query, resp.StatusCode, body)
		}
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

// A corrupt result must name both digests instead of leaking a raw Go error,
// because "digest mismatch: <one hash>" does not say which side that hash is.
func TestCorruptVerifyResultNamesBothDigests(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Of([]byte("intended-content"))
	stored := tlvEnvelope("blob@1", []byte("tampered"))
	if err := raw.Put(ctx, h, bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	page := getBody(t, admin, ts.URL+"/viewer/objects?selected="+url.QueryEscape(h.String())+"&tab=metadata")
	csrf := csrfFromPage(page)

	resp, err := admin.PostForm(ts.URL+"/viewer/objects/"+h.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	result := string(body)

	if strings.Contains(result, "cas: digest mismatch") {
		t.Fatalf("raw sentinel error must not reach the UI: %.400q", result)
	}
	for _, want := range []string{"Corrupt", "Expected", "Actual", h.String(), sha256.Of(stored).String()} {
		if !strings.Contains(result, want) {
			t.Fatalf("corrupt result missing %q: %.500q", want, result)
		}
	}
}

// The top-bar control verifies the whole store in one request and reports the
// counts, so an operator does not click through objects one at a time.
func TestVerifyAllUpdatesEveryObject(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	good := tlvEnvelope("blob@1", []byte("sound"))
	goodDigest := sha256.Of(good)
	if err := raw.Put(ctx, goodDigest, bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	badDigest := sha256.Of([]byte("intended"))
	if err := raw.Put(ctx, badDigest, bytes.NewReader(tlvEnvelope("blob@1", []byte("tampered")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	page := getBody(t, admin, ts.URL+"/viewer/objects")
	if !strings.Contains(page, "viewer-verify-all") {
		t.Fatalf("top bar is missing the verify-all control: %.500q", page)
	}
	csrf := csrfFromPage(page)

	resp, err := admin.PostForm(ts.URL+"/viewer/objects/verify-all", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-all = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("HX-Trigger") != "object-status-updated" {
		t.Fatalf("verify-all must refresh the object list, trigger=%q", resp.Header.Get("HX-Trigger"))
	}
	if strings.Contains(string(body), "corrupt") || strings.Contains(string(body), "verified") {
		t.Fatalf("verify-all must keep a plain label, the status cells carry the counts: %.300q", body)
	}
	if !strings.Contains(string(body), ">Verify<") {
		t.Fatalf("verify-all must stay labelled Verify: %.300q", body)
	}

	refreshed := getBody(t, admin, ts.URL+"/viewer/objects")
	if !strings.Contains(refreshed, "viewer-status-verified") || !strings.Contains(refreshed, "viewer-status-corrupt") {
		t.Fatalf("verify-all must record every result in the session: %.800q", refreshed)
	}

	// The bulk sweep records its reports too, so reselecting a swept object
	// shows the finding without re-running the check.
	metadata := getBody(t, admin, ts.URL+"/viewer/objects?selected="+url.QueryEscape(goodDigest.String())+"&tab=metadata")
	if !strings.Contains(metadata, "viewer-result-ok") || !strings.Contains(metadata, "Checked ") {
		t.Fatalf("metadata tab must replay the sweep's report: %.800q", metadata)
	}
}

func getBody(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestVerificationRefreshesObjectList(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	data := tlvEnvelope("blob@1", []byte("object body"))
	h := sha256.Of(data)
	if err := srv.store.Put(ctx, h, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, testStartupToken)

	resp, err := admin.Get(ts.URL + "/viewer/objects?selected=" + url.QueryEscape(h.String()) + "&tab=metadata")
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	csrf := csrfFromPage(string(page))
	if csrf == "" {
		t.Fatalf("actions page has no CSRF token: %.400q", page)
	}
	for _, want := range []string{
		"object-status-updated from:body",
	} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("object list does not subscribe to %q: %.400q", want, page)
		}
	}
	// The refresher travels with the swapped fragment, so a filtered list
	// refreshes under the same filters instead of reverting to every object.
	filtered := getBody(t, admin, ts.URL+"/viewer/objects?status=corrupt")
	if !strings.Contains(filtered, `hx-get="/viewer/objects?status=corrupt`) {
		t.Fatalf("refresh URL must carry the active filters: %.800q", filtered)
	}

	resp, err = admin.PostForm(ts.URL+"/viewer/objects/"+h.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Trigger") != "object-status-updated" || !strings.Contains(string(body), "viewer-result-ok") {
		t.Fatalf("verify = (%d, trigger=%q, body=%.400q), want successful status refresh", resp.StatusCode, resp.Header.Get("HX-Trigger"), body)
	}
	// The action only targets the result panel, so the inspector's Status row
	// must arrive as an out-of-band swap.
	if !strings.Contains(string(body), `id="inspector-status" hx-swap-oob="true"`) {
		t.Fatalf("verify must refresh the inspector status out of band: %.500q", body)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/viewer/objects?selected="+url.QueryEscape(h.String())+"&tab=metadata", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err = admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(refreshed), "Verified") {
		t.Fatalf("refreshed object list = (%d, %.400q), want verified status", resp.StatusCode, refreshed)
	}
}

// A check result outlives the click that produced it: the inspector restates
// the stored finding, and its age, every time the object is selected again.
func TestInspectorReplaysStoredVerificationResult(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	good := tlvEnvelope("blob@1", []byte("sound"))
	goodDigest := sha256.Of(good)
	if err := raw.Put(ctx, goodDigest, bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	badDigest := sha256.Of([]byte("intended"))
	if err := raw.Put(ctx, badDigest, bytes.NewReader(tlvEnvelope("blob@1", []byte("tampered")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	selected := func(digest string) string {
		return getBody(t, admin, ts.URL+"/viewer/objects?selected="+url.QueryEscape(digest)+"&tab=metadata")
	}

	// An unchecked object states nothing: the removed "Never in this session"
	// row said only that the operator had not clicked yet.
	page := selected(goodDigest.String())
	if strings.Contains(page, "viewer-result") {
		t.Fatalf("an unchecked object must show no result panel: %.800q", page)
	}
	if strings.Contains(page, "Never in this session") {
		t.Fatalf("the Checked metadata row must be gone: %.800q", page)
	}
	csrf := csrfFromPage(page)

	for _, digest := range []string{goodDigest.String(), badDigest.String()} {
		resp, err := admin.PostForm(ts.URL+"/viewer/objects/"+digest+"/verify", url.Values{"csrf": {csrf}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("verify %s = %d, want 200", digest, resp.StatusCode)
		}
	}

	page = selected(goodDigest.String())
	for _, want := range []string{"viewer-result-ok", "Stored bytes hash to this address.", "Checked "} {
		if !strings.Contains(page, want) {
			t.Fatalf("verified object must restate %q on reselection: %.900q", want, page)
		}
	}

	// A corrupt object restates the whole finding, both digests included, so
	// the operator sees why it failed without verifying again.
	page = selected(badDigest.String())
	for _, want := range []string{"viewer-result-bad", "Corrupt", badDigest.String(), "Checked "} {
		if !strings.Contains(page, want) {
			t.Fatalf("corrupt object must restate %q on reselection: %.900q", want, page)
		}
	}
	if !strings.Contains(page, sha256.Of(tlvEnvelope("blob@1", []byte("tampered"))).String()) {
		t.Fatalf("corrupt report must keep the digest the bytes actually hash to: %.900q", page)
	}
	// The inline replay must carry no out-of-band swap: outside an htmx action
	// response the swap element is not consumed and renders as a stray pill
	// below the panel.
	if strings.Contains(page, `hx-swap-oob="true" class="viewer-status`) {
		t.Fatalf("replayed report must not emit the out-of-band status swap: %.900q", page)
	}
}

// A store-wide sweep changes the integrity of the object currently open in the
// inspector, so the inspector has to re-render like the object table does.
func TestSweepRefreshesTheOpenInspector(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := tlvEnvelope("blob@1", []byte("sound"))
	digest := sha256.Of(data)
	if err := raw.Put(ctx, digest, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	target := ts.URL + "/viewer/objects?selected=" + url.QueryEscape(digest.String()) + "&tab=metadata"
	page := getBody(t, admin, target)
	csrf := csrfFromPage(page)

	// The inspector carries its own subscription: the list refresher only swaps
	// the table, so without this the sweep would leave the inspector stale.
	if !strings.Contains(page, `hx-trigger="object-status-updated from:body" hx-target="#object-inspector"`) {
		t.Fatalf("inspector does not subscribe to store-wide status changes: %.900q", page)
	}

	resp, err := admin.PostForm(ts.URL+"/viewer/objects/verify-all", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("HX-Trigger") != "object-status-updated" {
		t.Fatalf("sweep must announce the status change, trigger=%q", resp.Header.Get("HX-Trigger"))
	}

	// The refresh htmx performs: same URL, inspector target, no selection
	// header, so only the inspector is rendered.
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "object-inspector")
	resp, err = admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(refreshed), `id="object-list"`) {
		t.Fatalf("an inspector refresh must not also swap the object list: %.500q", refreshed)
	}
	for _, want := range []string{"viewer-status-verified", "viewer-result-ok", "Checked "} {
		if !strings.Contains(string(refreshed), want) {
			t.Fatalf("refreshed inspector missing %q after the sweep: %.900q", want, refreshed)
		}
	}
}

// Switching tabs only swaps the inspector, so the table's row links would keep
// the tab they were rendered with and silently throw the operator back to
// Metadata on the next pick. Tab links therefore re-render the list too.
func TestTabSelectionSurvivesPickingAnotherRow(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	digests := make([]cas.Digest, 0, 2)
	for _, payload := range [][]byte{[]byte("first"), []byte("second")} {
		data := tlvEnvelope("blob@1", payload)
		digest := sha256.Of(data)
		if err := raw.Put(ctx, digest, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	selected := digests[0]
	page := getBody(t, admin, ts.URL+"/viewer/objects?selected="+url.QueryEscape(selected.String())+"&tab=bytes")

	// A tab click must not be mistaken for a fresh selection: it has to leave
	// the visit trail exactly where it was.
	if !strings.Contains(page, "nav="+navStay) {
		t.Fatalf("inspector tab links must carry the %q marker: %.900q", navStay, page)
	}
	// ... and it has to swap the object list out of band, which the viewer
	// only does when the request asks for the selection fragment.
	tabLink := `hx-get="/viewer/objects?selected=` + selected.String() + `&amp;tab=references&amp;nav=` + navStay +
		`" hx-target="#object-inspector" hx-headers='{"X-Viewer-Selection":"true"}'`
	if !strings.Contains(page, tabLink) {
		t.Fatalf("tab link must also refresh the object list: %.2000q", page)
	}

	// The row links rendered while Bytes is open must keep the operator there.
	other := digests[1].String()
	rowLink := `href="/viewer/objects?selected=` + other + `&amp;tab=bytes"`
	if !strings.Contains(page, rowLink) {
		t.Fatalf("row links dropped the open tab, want %s: %.2000q", rowLink, page)
	}

	// Follow the tab link the way htmx does. The marker has to survive state
	// parsing, and the response has to carry the re-rendered list.
	req, err := http.NewRequest(http.MethodGet,
		ts.URL+"/viewer/objects?selected="+url.QueryEscape(selected.String())+"&tab=references&nav="+navStay, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "object-inspector")
	req.Header.Set("X-Viewer-Selection", "true")
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tab switch rejected: status=%d body=%.300q", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `id="object-list"`) {
		t.Fatalf("tab switch must swap the object list out of band: %.900q", body)
	}
	if !strings.Contains(string(body), `href="/viewer/objects?selected=`+other+`&amp;tab=references"`) {
		t.Fatalf("re-rendered row links did not adopt the new tab: %.2000q", body)
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
			"limit=ten",
			"offset=-1",
			"sort=references",
			"dir=sideways",
			"size=huge",
			"status=orphaned",
			"selected=not-a-digest",
			"tab=unknown",
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

	// Every data column sorts: a column that shows a fact the operator cannot
	// order by is a dead end. The reference column is asserted where a
	// reachability index exists, since it is absent without one.
	t.Run("sortable columns", func(t *testing.T) {
		page := getBody(t, viewer, ts.URL+"/viewer/objects")
		for _, want := range []string{"sort=inbound", "sort=status", "sort=size"} {
			if !strings.Contains(page, want) {
				t.Fatalf("object table header is missing %q: %.1500q", want, page)
			}
		}
		sortedPage := getBody(t, viewer, ts.URL+"/viewer/objects?sort=inbound&dir=desc")
		if !strings.Contains(sortedPage, `aria-sort="descending"`) {
			t.Fatalf("sort=inbound did not mark the sorted column: %.1500q", sortedPage)
		}
	})

	// A page size is a preference, not a claim about the store, so a size the
	// control does not offer is honoured within bounds and clamped outside
	// them. Rejecting it made a hand-edited or stale URL fail outright.
	t.Run("page size outside the offered set", func(t *testing.T) {
		for _, test := range []struct{ query, want string }{
			{query: "limit=10", want: `value="10" selected`},
			{query: "limit=0", want: `value="1" selected`},
			{query: "limit=100000", want: `value="250" selected`},
		} {
			resp, err := viewer.Get(ts.URL + "/viewer/objects?" + test.query)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("query %q = %d, want 200", test.query, resp.StatusCode)
			}
			// The control must name the size actually in effect, or it would
			// silently read as one of the presets.
			if !strings.Contains(string(body), test.want) {
				t.Fatalf("query %q: rows control missing %q: %.900q", test.query, test.want, body)
			}
		}
	})
}

func TestSortObjectRows(t *testing.T) {
	rows := []objectRow{
		{Digest: "b", Type: "tree@1", Size: 12, References: 1, Orphaned: true},
		{Digest: "a", Type: "blob@1", Size: 4, References: 7},
		{Digest: "c", Type: "blob@1", Size: 8, References: 3, Orphaned: true},
	}
	for _, test := range []struct {
		state objectBrowserState
		want  string
	}{
		{state: objectBrowserState{Sort: "hash", Direction: "asc"}, want: "abc"},
		{state: objectBrowserState{Sort: "type", Direction: "desc"}, want: "bca"},
		{state: objectBrowserState{Sort: "size", Direction: "asc"}, want: "acb"},
		{state: objectBrowserState{Sort: "status", Direction: "asc"}, want: "abc"},
		{state: objectBrowserState{Sort: "inbound", Direction: "asc"}, want: "bca"},
		{state: objectBrowserState{Sort: "inbound", Direction: "desc"}, want: "acb"},
		// Ascending lists the sound state first, like the other axes; the
		// digest breaks the tie within each group.
		{state: objectBrowserState{Sort: "reach", Direction: "asc"}, want: "abc"},
		{state: objectBrowserState{Sort: "reach", Direction: "desc"}, want: "cba"},
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
	if want := formatBytes(int64(len(env))); !strings.Contains(string(page), want) {
		t.Fatalf("detail page does not report the formatted size %q: %.200q", want, page)
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
		sessions.setVerification(first.ID, "digest", "verified", actionOutcome{Headline: "Verified"})
		if got := sessions.verification(first.ID, "digest"); got != "verified" {
			t.Fatalf("first session verification = %q, want verified", got)
		}
		if got := sessions.verification(second.ID, "digest"); got != "not-verified" {
			t.Fatalf("second session verification = %q, want not-verified", got)
		}
		// A result is only as good as its age, so the check time is recorded too.
		if _, checked := sessions.verificationRecord(first.ID, "digest"); checked.IsZero() {
			t.Fatal("a recorded verification must carry its check time")
		}
		if _, checked := sessions.verificationRecord(second.ID, "digest"); !checked.IsZero() {
			t.Fatal("an unchecked object must have no check time")
		}
		// The report is kept so the inspector can restate the finding later.
		if _, _, report := sessions.verificationReport(first.ID, "digest"); report.Headline != "Verified" {
			t.Fatalf("recorded report = %q, want the stored finding", report.Headline)
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
