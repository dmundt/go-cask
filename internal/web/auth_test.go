// Tests for login, throttling, sessions, and role authorization.

package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
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

	// Wrong token → 401 with no body. The reason lives on the login page, which
	// a human returns to; the rejection itself says nothing. The request is sent
	// as a browser would send it (same-origin), which the login now requires.
	resp = postFormAsBrowser(t, ts.Client(), ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("rejected login carried a body: %.80q", body)
	}
	// The page states the reason for a caller who reaches it after the refusal.
	page := getBody(t, ts.Client(), ts.URL+"/viewer/login?failed=1")
	if !strings.Contains(page, "That token was not accepted.") {
		t.Fatalf("login page after a rejected attempt must state the reason: %.200q", page)
	}

	// Startup token → 303 + session cookie, then object browser renders.
	authClient := login(t, ts, testStartupToken)
	resp, err = authClient.Get(ts.URL + "/viewer/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
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

// TestDirectTokenLogin walks the documented deep link end to end
// (viewer-security §5.1): the URL `cask web` prints and opens is a top-level
// navigation with no initiator, so it arrives as `Sec-Fetch-Site: none`, and
// it must still sign the browser in and land on the object browser.
func TestDirectTokenLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Transport: ts.Client().Transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/viewer/?token="+testStartupToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "none")
	resp, err := c.Do(req)
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
	if len(resp.Cookies()) != 1 {
		t.Fatalf("direct token login cookies = %d, want 1", len(resp.Cookies()))
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

// TestTokenLoginAcceptsSameOriginRequest pins the accepted half of the
// same-origin rule (viewer-security §5.1): a same-origin link or form and an
// address-bar/bookmark navigation both authenticate, and so does a request
// whose Origin names the viewer's own host when Sec-Fetch-Site is absent.
func TestTokenLoginAcceptsSameOriginRequest(t *testing.T) {
	ts, _ := newTestServer(t)
	// Do not follow the login redirect: the 303 and the cookie it carries are
	// what this test asserts.
	c := &http.Client{Transport: ts.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, tc := range []struct {
		name   string
		header map[string]string
	}{
		{"same-origin link", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": ts.URL}},
		{"Origin fallback", map[string]string{"Origin": ts.URL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+"/viewer/?token="+testStartupToken, nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range tc.header {
				req.Header.Set(name, value)
			}
			resp, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("same-origin token login = %d, want 303", resp.StatusCode)
			}
			if len(resp.Cookies()) != 1 {
				t.Fatalf("same-origin token login cookies = %d, want 1", len(resp.Cookies()))
			}
		})
	}
}

// TestTokenLoginRejectsCrossSiteRequest pins viewer-security §5.1: a token
// presented by a cross-site page must not mint a session, whether it arrives
// as a GET deep link an <img> can trigger or as a POST the login form would
// send. The reply is 403 with an empty body (§13) and no session cookie, and
// the refusal never creates a session.
func TestTokenLoginRejectsCrossSiteRequest(t *testing.T) {
	ts, srv := newTestServer(t)
	// No redirect following: the login response itself is what must not carry
	// a session cookie.
	c := &http.Client{Transport: ts.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	for _, tc := range []struct {
		name   string
		header map[string]string
	}{
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"same-site", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"foreign Origin", map[string]string{"Origin": "https://attacker.example"}},
		{"no origin headers", nil},
	} {
		t.Run("GET "+tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, ts.URL+"/viewer/?token="+testStartupToken, nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range tc.header {
				req.Header.Set(name, value)
			}
			resp, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("cross-site token GET = %d, want 403", resp.StatusCode)
			}
			if len(body) != 0 {
				t.Fatalf("cross-site token GET body = %q, want empty", body)
			}
			if len(resp.Cookies()) != 0 {
				t.Fatalf("cross-site token GET set cookies: %v", resp.Cookies())
			}
		})
	}

	// The same refusal covers the login POST: a cross-site form must not force
	// the victim's browser into a session either.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/viewer/login", strings.NewReader(url.Values{"token": {testStartupToken}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://attacker.example")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login POST = %d, want 403", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("cross-site login POST set cookies: %v", resp.Cookies())
	}
	if got := sessionCount(srv); got != 0 {
		t.Fatalf("rejected cross-site logins created %d sessions, want 0", got)
	}
}

// TestCSRFQueryValueRejected pins viewer-security §5 at the HTTP surface: a
// valid CSRF token supplied as `?_csrf=…` (`?csrf=…` for the field name the
// viewer uses) is refused, while the same token in the form body or the
// X-CSRF-Token header still authorizes the mutation.
func TestCSRFQueryValueRejected(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := login(t, ts, testStartupToken)
	csrf := csrfFromPage(getBody(t, admin, ts.URL+"/viewer/objects"))
	if csrf == "" {
		t.Fatal("object browser carried no CSRF token")
	}

	// The query string alone is not a carrier, even holding the exact token.
	resp := postFormAsBrowser(t, admin, ts.URL+"/viewer/objects/verify?csrf="+url.QueryEscape(csrf), url.Values{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST with ?csrf= = %d, want 403", resp.StatusCode)
	}

	// The form body is.
	resp = postFormAsBrowser(t, admin, ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST with csrf in the body = %d, want 200", resp.StatusCode)
	}

	// So is the header.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/viewer/objects/verify", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST with X-CSRF-Token = %d, want 200", resp.StatusCode)
	}
}

func TestLoginThrottle(t *testing.T) {
	ts, _ := newTestServer(t)
	for range 5 {
		resp := postFormAsBrowser(t, ts.Client(), ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
		resp.Body.Close()
	}
	resp := postFormAsBrowser(t, ts.Client(), ts.URL+"/viewer/login", url.Values{"token": {"wrong"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th failed login = %d, want 429", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("throttled login carried a body: %.80q", body)
	}
	// The caller is told how long the block lasts, in whole seconds, and the
	// advertised delay is the throttle's own remaining window (one minute after
	// the first exhaustion).
	retryAfter := resp.Header.Get("Retry-After")
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After = %q, want an integer number of seconds", retryAfter)
	}
	if seconds <= 0 || seconds > 60 {
		t.Fatalf("Retry-After = %d, want 1..60 seconds of remaining block", seconds)
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

	resp := postFormAsBrowser(t, ts.Client(), ts.URL+"/viewer/login", url.Values{"token": {""}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("empty-token login = %d, want 401", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("rejected login carried a body: %.80q", body)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("empty-token login set cookies: %v", resp.Cookies())
	}
}

// TestThrottleRetryAfterIsTheRemainingBlock pins the Retry-After contract: a
// refused attempt advertises the block that is actually in force, rounded up to
// whole seconds, and a cleared record advertises nothing. A header that drifted
// from the enforced wait would either lie to a caller or invite a retry the
// throttle then refuses again.
func TestThrottleRetryAfterIsTheRemainingBlock(t *testing.T) {
	const window = 90 * time.Second
	th := newThrottle(2, window)
	if !th.allow("ip") || !th.allow("ip") {
		t.Fatal("the first two attempts must be allowed")
	}
	if th.allow("ip") {
		t.Fatal("the third attempt must be refused")
	}
	got := th.retryAfter("ip")
	if got <= 0 || got > window {
		t.Fatalf("retryAfter in force = %v, want (0, %v]", got, window)
	}
	// Whole seconds: the header is expressed in seconds, so a fractional answer
	// would render as 0 and read as "retry now".
	if got%time.Second != 0 {
		t.Fatalf("retryAfter = %v, want a whole number of seconds", got)
	}
	if got < time.Second {
		t.Fatalf("retryAfter = %v, want at least one second", got)
	}
	th.reset("ip")
	if got := th.retryAfter("ip"); got != time.Second {
		t.Fatalf("retryAfter after a reset = %v, want the one-second floor", got)
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
		// The same token in the query string never validates: a URL-borne
		// token leaks through logs, bookmarks, proxies, and Referer chains
		// (viewer-security §5).
		query := httptest.NewRequest(http.MethodPost, "/viewer/objects/verify?csrf=csrf-token", nil)
		query.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if csrfOK(query, sess) {
			t.Fatal("csrfOK accepted a token from the query string")
		}
		// The header remains an accepted carrier.
		header := httptest.NewRequest(http.MethodPost, "/viewer/objects/verify", nil)
		header.Header.Set("X-CSRF-Token", "csrf-token")
		if !csrfOK(header, sess) {
			t.Fatal("csrfOK rejected the X-CSRF-Token header")
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
