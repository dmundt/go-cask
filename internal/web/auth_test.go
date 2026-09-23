// Tests for login, throttling, sessions, and role authorization.

package web

import (
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

	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

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

func TestRoleTokensLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	// A configured role token is a login in its own right, and it grants that
	// role rather than the startup token's admin rank: the viewer rank reads
	// the browser but cannot reach an operator route.
	viewer := login(t, ts, "viewer-tok")
	if got := statusCode(t, viewer, ts.URL+"/viewer/objects"); got != http.StatusOK {
		t.Fatalf("viewer token browsing objects = %d, want 200", got)
	}
	resp, err := viewer.PostForm(ts.URL+"/viewer/objects/verify", url.Values{
		"csrf": {csrfFromPage(getBody(t, viewer, ts.URL+"/viewer/objects"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer token reaching an operator route = %d, want 403", resp.StatusCode)
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
	for range 5 {
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

// TestLoginRejectsEmptyToken pins the fail-closed rule: an empty submitted
// token never authenticates, even if a role token was misconfigured as "".
func TestLoginRejectsEmptyToken(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{
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

// TestThrottleExponentialBackoff pins the backoff growth: each consecutive
// exhaustion doubles the block window.
func TestThrottleExponentialBackoff(t *testing.T) {
	const window = 50 * time.Millisecond
	th := newThrottle(2, window)
	for i := range 2 {
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
	for range 50 {
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
		req := httptest.NewRequest(http.MethodPost, "/viewer/objects/verify", strings.NewReader(url.Values{"csrf": {"csrf-token"}}.Encode()))
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
