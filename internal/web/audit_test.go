package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

// TestSessionHandleNeverLeaksTheCookie keeps the audit identifier one-way:
// viewer-security §9 wants the session named and §11 forbids the log from
// carrying the credential, and the session id is the cookie value.
func TestSessionHandleNeverLeaksTheCookie(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	handle := sessionHandle(id)
	if handle == "" || strings.Contains(id, handle) {
		t.Fatalf("sessionHandle(%q) = %q, want a digest prefix that is not part of the id", id, handle)
	}
	if again := sessionHandle(id); again != handle {
		t.Fatalf("sessionHandle is not stable: %q then %q", handle, again)
	}
	if other := sessionHandle(id + "x"); other == handle {
		t.Fatal("distinct sessions must get distinct handles")
	}
	if got := sessionHandle(""); got != "anonymous" {
		t.Fatalf("sessionHandle(\"\") = %q, want %q", got, "anonymous")
	}
}

// TestAuditLogNamesTheSession checks every audited action records who acted,
// without recording the cookie that identifies them.
func TestAuditLogNamesTheSession(t *testing.T) {
	ctx := context.Background()
	raw, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := raw.Put(ctx, h, strings.NewReader("audit me")); err != nil {
		t.Fatal(err)
	}
	srv, err := New(raw, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)

	var log bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	admin := login(t, ts, testStartupToken)
	csrf := csrfFromPage(getBody(t, admin, ts.URL+"/viewer/objects"))
	for _, target := range []string{
		ts.URL + "/viewer/objects/" + h.String() + "/verify",
		ts.URL + "/viewer/objects/verify",
	} {
		resp, err := admin.PostForm(target, url.Values{"csrf": {csrf}})
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s = %d, want 200", target, resp.StatusCode)
		}
	}

	lines := log.String()
	for _, want := range []string{"viewer login", "object.verify", "object.verify-all"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("audit log missing %q:\n%s", want, lines)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(lines), "\n") {
		if !strings.Contains(line, "viewer login") && !strings.Contains(line, "viewer audit") {
			continue
		}
		if !strings.Contains(line, "session=") {
			t.Fatalf("audited line does not name the session: %s", line)
		}
	}
	for _, sess := range srv.sessions.byID {
		if strings.Contains(lines, sess.ID) {
			t.Fatal("audit log contains a raw session id")
		}
		if strings.Contains(lines, sess.CSRF) {
			t.Fatal("audit log contains a CSRF token")
		}
	}
	if strings.Contains(lines, testStartupToken) {
		t.Fatal("audit log contains the startup token")
	}
}
