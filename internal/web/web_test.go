package web

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/internal/storage"
)

const testStartupToken = "AAAA-BBBB-CCCC"

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	st, err := storage.New(context.Background(), storage.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, Config{
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
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// login performs the startup-token login and returns an authed client.
func login(t *testing.T, ts *httptest.Server, token string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	// Do not follow the 303 to the dashboard: the login response itself is
	// what carries the session cookie.
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
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

	// Unauthenticated dashboard → 401 empty body.
	resp, err := http.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dashboard = %d, want 401", resp.StatusCode)
	}

	// Wrong token → 401.
	resp, err = http.PostForm(ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", resp.StatusCode)
	}

	// Startup token → 303 + session cookie, then dashboard renders.
	c := login(t, ts, testStartupToken)
	resp, err = c.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "CASK viewer") {
		t.Fatalf("dashboard = %d, %.80q", resp.StatusCode, body)
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

func TestLoginThrottle(t *testing.T) {
	ts, _ := newTestServer(t)
	for i := 0; i < 5; i++ {
		resp, err := http.PostForm(ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	resp, err := http.PostForm(ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
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
	st, _ := storage.New(ctx, storage.Config{Dir: t.TempDir()})
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := st.Put(ctx, h, strings.NewReader("view me")); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, Config{StartupToken: testStartupToken, RoleTokens: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	admin := login(t, ts, testStartupToken)

	// Object detail renders with the full hash.
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
	// htmx is public (needed on the login page).
	resp, err := http.Get(ts.URL + "/viewer/static/htmx.min.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("htmx = %d, want 200", resp.StatusCode)
	}
}

func mustParse(t *testing.T, s string) cas.Hash {
	t.Helper()
	h, err := cas.ParseHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
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
