package web

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestUnknownViewerPathsAnswerUniformly pins the catch-all: the viewer
// serves a fixed set of routes, and every other path under the prefix — a
// route that was removed, one that never existed, or one the viewer refuses
// to offer at all, such as deleting an object — answers the same way. The
// reply turns on the caller's session, not on the path, so an anonymous
// caller cannot map the surface by probing it.
func TestUnknownViewerPathsAnswerUniformly(t *testing.T) {
	ts, _ := newTestServer(t)
	authed := login(t, ts, testStartupToken)
	for _, path := range []string{
		"/viewer/dashboard",
		"/viewer/dashboard/",
		"/viewer/gc",
		"/viewer/nothing-here",
		"/viewer/static/viewer.js",
	} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, resp.StatusCode)
		}
		if got := statusCode(t, authed, ts.URL+path); got != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, got)
		}
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

// TestResponsesCarryHardeningHeaders pins the response hardening: the viewer
// serves its stylesheet and its one script from its own origin, so anything
// else is denied, and no page may be framed or content-sniffed.
func TestResponsesCarryHardeningHeaders(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/viewer/login", "/viewer/objects", "/viewer/static/viewer.css"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s X-Content-Type-Options = %q, want nosniff", path, got)
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("%s X-Frame-Options = %q, want DENY", path, got)
		}
		// A token may appear in the landing URL, so no response may pass its
		// own URL on as a Referer.
		if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("%s Referrer-Policy = %q, want no-referrer", path, got)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'",
			// htmx swaps are XHRs to the viewer's own routes; without this the
			// policy blocks every interaction the viewer has.
			"connect-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Fatalf("%s CSP = %q, want it to contain %q", path, csp, want)
			}
		}
	}
}

// TestEveryResponseIsUncacheable pins the cache policy: the viewer renders
// digests, object bytes, and the session's verification state, and a remote
// deployment reaches it through a TLS-terminating proxy (viewer-security §12).
// A shared cache in that path must neither retain a response nor answer a later
// caller with a page rendered for someone else's session, so every viewer
// response — page, fragment, static asset, and rejection alike — is no-store
// and varies on the session cookie.
func TestEveryResponseIsUncacheable(t *testing.T) {
	ts, _ := newTestServer(t)
	viewer := login(t, ts, "viewer-tok")

	anonymous := &http.Client{Transport: ts.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	rejected, err := anonymous.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {"x"}})
	if err != nil {
		t.Fatal(err)
	}
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous verify = %d, want 401", rejected.StatusCode)
	}

	for _, tc := range []struct {
		name string
		resp *capturedResponse
	}{
		{name: "login page", resp: getResponse(t, ts.Client(), ts.URL+"/viewer/login")},
		{name: "object page", resp: getResponse(t, viewer, ts.URL+"/viewer/objects")},
		{name: "fragment", resp: getResponse(t, viewer, ts.URL+"/viewer/objects?tab=bytes")},
		{name: "stylesheet", resp: getResponse(t, ts.Client(), ts.URL+"/viewer/static/viewer.css")},
		{name: "script", resp: getResponse(t, ts.Client(), ts.URL+"/viewer/static/htmx.min.js")},
		{name: "401", resp: &capturedResponse{status: rejected.StatusCode, header: rejected.Header}},
	} {
		if got := tc.resp.header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", tc.name, got)
		}
		if vary := tc.resp.header.Values("Vary"); !slices.Contains(vary, "Cookie") {
			t.Errorf("%s Vary = %v, want it to name Cookie", tc.name, vary)
		}
	}
}

// TestRejectionsCarryNoBody keeps the viewer's refusals silent: a caller
// without a session, a session without the role, and a mutation without CSRF
// all answer with the status alone. Nothing in the response tells a prober
// whether the target exists or why the request was refused.
func TestRejectionsCarryNoBody(t *testing.T) {
	ts, _ := newTestServer(t)
	anonymous := &http.Client{Transport: ts.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	viewer := login(t, ts, "viewer-tok")
	csrf := csrfFromPage(getBody(t, viewer, ts.URL+"/viewer/objects"))

	unauthorized, err := anonymous.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {"x"}})
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Body.Close()
	forbidden, err := viewer.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	defer forbidden.Body.Close()
	csrfRejected, err := viewer.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	defer csrfRejected.Body.Close()

	for _, tc := range []struct {
		name   string
		resp   *http.Response
		status int
	}{
		{"missing session", unauthorized, http.StatusUnauthorized},
		{"insufficient role", forbidden, http.StatusForbidden},
		{"missing CSRF", csrfRejected, http.StatusForbidden},
	} {
		body, err := io.ReadAll(tc.resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if tc.resp.StatusCode != tc.status {
			t.Errorf("%s = %d, want %d", tc.name, tc.resp.StatusCode, tc.status)
		}
		if len(body) != 0 {
			t.Errorf("%s answered with a body: %.120q", tc.name, body)
		}
	}
}

// TestErrorBodiesCarryOnlyOwnedProse checks the other half of response
// hygiene: a failure the viewer answers itself — a query it cannot parse, an
// object it does not have — says what the viewer decided in the viewer's own
// words. Interpreter text (a wrapped backend error, a filesystem path)
// describes the layer underneath, and an operator reading the page must not
// receive it.
func TestErrorBodiesCarryOnlyOwnedProse(t *testing.T) {
	ts, _ := newTestServer(t)
	viewer := login(t, ts, "viewer-tok")
	missing := strings.Repeat("11", 32)

	for _, tc := range []struct {
		name   string
		target string
		status int
	}{
		{"unparsable query", ts.URL + "/viewer/objects?limit=ten", http.StatusBadRequest},
		{"missing object", ts.URL + "/viewer/objects/" + missing + "/dump", http.StatusNotFound},
	} {
		resp := getResponse(t, viewer, tc.target)
		if resp.status != tc.status {
			t.Errorf("%s = %d, want %d", tc.name, resp.status, tc.status)
		}
		for _, leak := range []string{"cas: ", "open ", "/tmp/", "/home/", `C:\`, "syscall", "runtime error"} {
			if strings.Contains(resp.body, leak) {
				t.Errorf("%s body carries interpreter text %q: %.200q", tc.name, leak, resp.body)
			}
		}
	}
}

// capturedResponse is a finished response: its status, its headers, and the
// body already read, so a caller can assert on all three after the exchange.
type capturedResponse struct {
	status int
	header http.Header
	body   string
}

// getResponse performs a GET and reads the whole body into a
// capturedResponse.
func getResponse(t *testing.T, client *http.Client, target string) *capturedResponse {
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
	return &capturedResponse{status: resp.StatusCode, header: resp.Header, body: string(body)}
}
