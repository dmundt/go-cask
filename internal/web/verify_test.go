// Tests for the integrity checks and the results they leave in the session.

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// TestIntegrityOfClassifiesSentinels pins the classification the sweep and the
// per-object action share: only a digest mismatch is corruption, while an
// object that is absent or cannot be read was never verified.
func TestIntegrityOfClassifiesSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"mismatch", fmt.Errorf("%w: ab", cas.ErrDigestMismatch), "corrupt"},
		{"absent", fmt.Errorf("cas: get: %w", cas.ErrNotFound), "not-verified"},
		{"unreadable", errors.New("read failed"), "not-verified"},
	}
	for _, tc := range cases {
		if got := integrityOf(tc.err); got != tc.want {
			t.Errorf("integrityOf(%s: %v) = %q, want %q", tc.name, tc.err, got, tc.want)
		}
	}
}

// TestVerifyMissingObjectIsNotCorrupt drives the per-object action for a digest
// that is not stored: an absent object is unverified with "Missing" prose, never
// corrupted content — the same distinction cas.VerifyAll makes, so the viewer's
// states and `cask verify --all` agree.
func TestVerifyMissingObjectIsNotCorrupt(t *testing.T) {
	ts, _ := newTestServer(t)
	operator := login(t, ts, "operator-tok")
	csrf := csrfFromPage(getBody(t, operator, ts.URL+"/viewer/objects"))

	missing := strings.Repeat("11", 32) // well-formed digest, nothing stored at it
	resp, err := operator.PostForm(ts.URL+"/viewer/objects/"+missing+"/verify",
		url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify of a missing object = %d, want 200", resp.StatusCode)
	}
	got := string(body)
	if strings.Contains(got, "viewer-status-corrupt") {
		t.Fatalf("a missing object must not be reported as corrupt: %.400q", got)
	}
	if !strings.Contains(got, "viewer-status-not-verified") {
		t.Fatalf("missing object = %.400q, want the not-verified state", got)
	}
	if !strings.Contains(got, "Missing") {
		t.Fatalf("missing object = %.400q, want the prose to name it missing", got)
	}
}

func TestRemovedGCPostReturnsNotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := login(t, ts, testStartupToken)
	csrf := csrfFromPage(getBody(t, admin, ts.URL+"/viewer/objects"))

	resp, err := admin.PostForm(ts.URL+"/viewer/gc", url.Values{
		"csrf":  {csrf},
		"roots": {"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	})
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
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := backend.Put(ctx, h, strings.NewReader("view me")); err != nil {
		t.Fatal(err)
	}
	references := newTestReferenceIndex()
	references.Record(h, []cas.Digest{mustParse(t, "sha256:"+strings.Repeat("cd", 32))})
	srv, err := New(backend, Config{StartupToken: testStartupToken, RoleTokens: map[string]string{}, References: references})
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
	rc, err := backend.Get(ctx, h)
	if err != nil {
		t.Fatalf("object must survive a delete attempt: %v", err)
	}
	rc.Close()
}

// A corrupt result must name both digests instead of leaking a backend Go error,
// because "digest mismatch: <one hash>" does not say which side that hash is.
func TestCorruptVerifyResultNamesBothDigests(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Of([]byte("intended-content"))
	stored := test.TLVEnvelope("blob@1", []byte("tampered"))
	if err := backend.Put(ctx, h, bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
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
		t.Fatalf("backend sentinel error must not reach the UI: %.400q", result)
	}
	for _, want := range []string{"Corrupt", "Expected", "Actual", h.String(), sha256.Of(stored).String()} {
		if !strings.Contains(result, want) {
			t.Fatalf("corrupt result missing %q: %.500q", want, result)
		}
	}
}

// TestUnreadableObjectNeverRendersTheError pins viewer-design §3: a failed
// action is presented as structured prose, never as a raw Go error string. An
// unreadable object is the path that used to carry the interpreter's own words
// into the result, and those words name the storage layer's absolute paths, so
// the response must state only the viewer's finding while the cause goes to the
// audit line.
func TestUnreadableObjectNeverRendersTheError(t *testing.T) {
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	restoreLogs := captureLogs(&logs)
	defer restoreLogs()

	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	// A directory where the object's file belongs. The bytes of the named object
	// are what the digest addresses, so this one is unreadable for good: opening
	// the path succeeds, but reading it never yields the object's bytes and the
	// error names the absolute path the operator must not see.
	if err := os.MkdirAll(objectDir(base, h), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objectDir(base, h), "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if read, err := backend.Get(context.Background(), h); err != nil {
		t.Fatalf("the object's path must open, so the failure lands in the read: %v", err)
	} else {
		buf := make([]byte, 64)
		_, err := read.Read(buf)
		read.Close()
		if err == nil {
			t.Fatal("reading a directory as an object must fail")
		}
		if !strings.Contains(err.Error(), base) {
			t.Fatalf("the read error does not name the store root, so this test proves nothing: %v", err)
		}
	}

	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	// The error the response must not describe names the store root, so the
	// assertions below are testing something real.
	if _, err := srv.verifyObject(context.Background(), h); err == nil {
		t.Fatal("verifying an unreadable object must fail")
	} else if !strings.Contains(err.Error(), base) {
		t.Fatalf("the verification error does not name the store root, so this test proves nothing: %v", err)
	}

	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	operator := login(t, ts, testStartupToken)
	csrf := csrfFromPage(getBody(t, operator, ts.URL+"/viewer/objects"))

	resp, err := operator.PostForm(ts.URL+"/viewer/objects/"+h.String()+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify of an unreadable object = %d, want 200", resp.StatusCode)
	}
	got := string(body)
	if !strings.Contains(got, "Unreadable") {
		t.Fatalf("unreadable result = %.400q, want the viewer's own headline", got)
	}
	if strings.Contains(got, base) {
		t.Fatalf("result leaked a filesystem path: %.400q", got)
	}
	if strings.Contains(got, "cas: get") || strings.Contains(got, "open") && strings.Contains(got, "object") {
		t.Fatalf("result leaked the storage layer's error text: %.400q", got)
	}
	// The cause is not lost: the audit line carries it, where an operator who
	// needs the interpreter's words reads them (viewer-security §9).
	audit := logs.String()
	if !strings.Contains(audit, h.String()) {
		t.Fatalf("audit line does not name the object:\n%s", audit)
	}
	if !strings.Contains(audit, "cas:") {
		t.Fatalf("audit line does not record why the read failed:\n%s", audit)
	}
}

// captureLogs routes the default logger into buf for the duration of a test and
// returns the restore function. slog serializes writes, so the buffer is safe
// to read once the requests under test have returned.
func captureLogs(buf *bytes.Buffer) func() {
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	return func() { slog.SetDefault(restore) }
}

// objectDir is the fs backend's default layout (fan-out 2, one level) for one
// digest: the object's first two hex characters, then the full hex digest.
func objectDir(base string, h cas.Digest) string {
	hexDigest := h.String()
	return filepath.Join(base, hexDigest[:2], hexDigest)
}

// The top-bar control verifies the whole store in one request and reports the
// counts, so an operator does not click through objects one at a time.
func TestVerifyAllUpdatesEveryObject(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	good := test.TLVEnvelope("blob@1", []byte("sound"))
	goodDigest := sha256.Of(good)
	if err := backend.Put(ctx, goodDigest, bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	badDigest := sha256.Of([]byte("intended"))
	if err := backend.Put(ctx, badDigest, bytes.NewReader(test.TLVEnvelope("blob@1", []byte("tampered")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
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

	resp, err := admin.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
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

func TestVerificationRefreshesObjectList(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	data := test.TLVEnvelope("blob@1", []byte("object body"))
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
	// The action only targets the result panel, so the inspector's Integrity
	// row must arrive as an out-of-band swap.
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
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	good := test.TLVEnvelope("blob@1", []byte("sound"))
	goodDigest := sha256.Of(good)
	if err := backend.Put(ctx, goodDigest, bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	badDigest := sha256.Of([]byte("intended"))
	if err := backend.Put(ctx, badDigest, bytes.NewReader(test.TLVEnvelope("blob@1", []byte("tampered")))); err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
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
	if !strings.Contains(page, sha256.Of(test.TLVEnvelope("blob@1", []byte("tampered"))).String()) {
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
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := test.TLVEnvelope("blob@1", []byte("sound"))
	digest := sha256.Of(data)
	if err := backend.Put(ctx, digest, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
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

	resp, err := admin.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
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
