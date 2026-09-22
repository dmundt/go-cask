package web

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDashboardRouteRemoved(t *testing.T) {
	ts, _ := newTestServer(t)
	// The route is gone, but it still answers through the auth gate: an
	// anonymous caller learns nothing about which paths the viewer knows.
	resp, err := ts.Client().Get(ts.URL + "/viewer/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous dashboard route = %d, want 401", resp.StatusCode)
	}
	if got := statusCode(t, login(t, ts, testStartupToken), ts.URL+"/viewer/dashboard"); got != http.StatusNotFound {
		t.Fatalf("dashboard route = %d, want 404", got)
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
