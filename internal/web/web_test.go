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

	// Unauthenticated dashboard redirects to the login page.
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/viewer/login" {
		t.Fatalf("unauthenticated dashboard = %d, location=%q, want 303 /viewer/login", resp.StatusCode, resp.Header.Get("Location"))
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
	authClient := login(t, ts, testStartupToken)
	resp, err = authClient.Get(ts.URL + "/viewer/")
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

func TestDirectTokenLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
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
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "CASK viewer") {
		t.Fatalf("authed dashboard = %d, %.80q", resp.StatusCode, body)
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
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.PostForm(ts.URL+"/viewer/login", url.Values{"token": {""}})
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
	ts := httptest.NewServer(srv.Handler())
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
