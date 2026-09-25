// Tests that drive the viewer's routes and handlers to the failure branches the
// existing suites do not reach: a store the snapshot walk cannot read, an
// object whose bytes cannot be read, the malformed-hash guards, and the digest
// failures an injected hasher produces.

package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/index"
)

// errNoDigest is the failure the injected hashers below report instead of
// producing or accepting a digest.
var errNoDigest = errors.New("test hasher: no digest")

// recordingHasher wraps the shipped SHA-256 hasher and reports whether each
// method ran, so a test can assert that a guard rejected a request before the
// object was ever hashed. Validation can be made to fail on demand.
type recordingHasher struct {
	validateErr error
	digestErr   error
	validated   int
	digested    int
}

func (h *recordingHasher) Validate(d cas.Digest) error {
	h.validated++
	if h.validateErr != nil {
		return h.validateErr
	}
	return sha256.New().Validate(d)
}

func (h *recordingHasher) Digest(r io.Reader) (cas.Digest, error) {
	h.digested++
	if h.digestErr != nil {
		return nil, h.digestErr
	}
	return sha256.New().Digest(r)
}

// TestParseDigestRejectsADigestTheHasherRefuses pins the second half of the
// malformed-hash guard: text the core can parse is still refused when the
// configured algorithm says it cannot name an object. The route answers 400
// before the object is read, so a cookie directory is never opened.
func TestParseDigestRejectsADigestTheHasherRefuses(t *testing.T) {
	task := newRouteTask(t, Config{
		StartupToken: testStartupToken,
		Hasher:       &recordingHasher{validateErr: errNoDigest},
	})
	admin := login(t, task.ts, testStartupToken)

	// Any well-formed hex is a valid core digest but not a valid one for this
	// hasher, which is exactly the branch under test.
	digest := sha256.Of([]byte("whatever")).String()
	resp, err := admin.Get(task.ts.URL + "/viewer/objects/" + digest)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a digest the hasher refuses = %d, want 400", resp.StatusCode)
	}
}

// TestObjectDumpRejectsMalformedHash pins the path-value guard on the dump
// route: a hash that is not hex is a malformed request, not a missing object,
// and the bytes at that address are never read.
func TestObjectDumpRejectsMalformedHash(t *testing.T) {
	task := newRouteTask(t, Config{StartupToken: testStartupToken})
	admin := login(t, task.ts, testStartupToken)

	resp, err := admin.Get(task.ts.URL + "/viewer/objects/not-a-digest/dump")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed dump hash = %d, want 400", resp.StatusCode)
	}
}

// TestVerifyObjectRejectsADigestTheHasherRefuses pins the guard the bulk sweep
// and the per-object action share: a digest the configured algorithm refuses is
// reported as a failure before the store is touched, so the sweep records an
// unreadable object instead of asking a backend for an address that cannot
// exist.
func TestVerifyObjectRejectsADigestTheHasherRefuses(t *testing.T) {
	hasher := &recordingHasher{validateErr: errNoDigest}
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken, Hasher: hasher})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Of([]byte("unstored"))

	actual, err := srv.verifyObject(context.Background(), h)
	if !errors.Is(err, errNoDigest) {
		t.Fatalf("verifyObject(digest the hasher refuses) error = %v, want the hasher's refusal", err)
	}
	if actual != "" {
		t.Fatalf("verifyObject returned actual digest %q, want empty: nothing was hashed", actual)
	}
	if hasher.validated != 1 {
		t.Fatalf("verifyObject called Validate %d times, want exactly 1", hasher.validated)
	}
	if hasher.digested != 0 {
		t.Fatalf("verifyObject called Digest %d times, want none: the guard runs first", hasher.digested)
	}

	// With a hasher that accepts the digest, the same call falls through the
	// guard to the store, where a missing object is still distinguishable from
	// the hasher's refusal.
	permissive := &recordingHasher{digestErr: errors.New("unreachable")}
	srv.cfg.Hasher = permissive
	if _, err := srv.verifyObject(context.Background(), h); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("verifyObject(missing object) with a permissive hasher = %v, want ErrNotFound", err)
	}
}

// TestVerifyObjectReportsAReadFailure pins the middle of the same chain: the
// bytes could be opened but hashing them failed, so the result carries no
// digest and the error names the read. The error never reaches the page — the
// handler renders owned prose from it (verify.go) — but the sweep's audit line
// records it.
func TestVerifyObjectReportsAReadFailure(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hasher := &recordingHasher{digestErr: errNoDigest}
	srv, err := New(backend, Config{StartupToken: testStartupToken, Hasher: hasher})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Of([]byte("stored"))
	if err := backend.Put(context.Background(), h, strings.NewReader("stored")); err != nil {
		t.Fatal(err)
	}

	actual, err := srv.verifyObject(context.Background(), h)
	if !errors.Is(err, errNoDigest) {
		t.Fatalf("verifyObject(read failure) error = %v, want the hasher's read error", err)
	}
	if actual != "" {
		t.Fatalf("verifyObject returned actual digest %q, want empty after a failed read", actual)
	}
	if !strings.Contains(err.Error(), "cas: verify read") {
		t.Fatalf("verifyObject error = %v, want it to name the failed read", err)
	}
}

// TestVerifyFragmentAnswersMalformedHash pins the per-object verify route's
// own guard: the parse rejects the hash before any session-scoped state is
// recorded, so the answer is a plain 400 rather than an audit entry about an
// object that was never identified.
func TestVerifyFragmentAnswersMalformedHash(t *testing.T) {
	task := newRouteTask(t, Config{StartupToken: testStartupToken})
	admin := login(t, task.ts, testStartupToken)
	csrf := csrfFromPage(getBody(t, admin, task.ts.URL+"/viewer/objects"))

	resp, err := admin.PostForm(task.ts.URL+"/viewer/objects/not-a-digest/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("verify with a malformed hash = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "malformed hash") {
		t.Fatalf("verify with a malformed hash = %.200q, want the viewer's own reason", body)
	}
}

// TestVerifyAllReportsAListFailure pins the sweep's first failure: the store
// cannot be enumerated at all, so the answer is a 500 in the viewer's own words
// rather than a fragment claiming an empty store was verified.
func TestVerifyAllReportsAListFailure(t *testing.T) {
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := srv.sessions.create(RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/viewer/objects/verify", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.ID})
	rec := httptest.NewRecorder()
	srv.verifyAllFragment(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("verify-all over an unreadable store = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "list failed" {
		t.Fatalf("verify-all failure body = %q, want the viewer's own prose", got)
	}
}

// TestObjectListReportsAStoreFailureOnTheFilteredPath pins the filtered walk's
// error branch: the browser cannot enumerate the store, so the answer is a 500
// with the viewer's own prose. The row-building pass runs the same snapshot
// walk against the same unreadable store, reached directly so the handler's
// earlier failure cannot mask it.
func TestObjectListReportsAStoreFailureOnTheFilteredPath(t *testing.T) {
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}

	// A filter takes the general path: the default hash-ascending page cannot
	// answer it, so the rows come from this walk.
	if _, err := srv.objectRows(context.Background(), "", objectBrowserState{Query: "blob"}); err == nil {
		t.Fatal("objectRows over an unreadable store = nil error, want the walk failure")
	}

	req := httptest.NewRequest(http.MethodGet, "/viewer/objects?q=blob", nil)
	rec := httptest.NewRecorder()
	srv.objects(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("filtered list over an unreadable store = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "list failed" {
		t.Fatalf("filtered list failure body = %q, want the viewer's own prose", got)
	}
}

// symlinkObjectFixture makes the object at digest h enumerable but unreadable:
// the store path becomes a symlink to a directory, so the backend's walk lists
// the link as a file while opening it for a read fails. That is the one shape
// the fs backend can produce for an entry whose bytes cannot be read — a real
// directory at the object's path would be skipped by the walk entirely — and it
// is what a filesystem-side mishap looks like to the viewer.
func symlinkObjectFixture(t *testing.T, base string, h cas.Digest) {
	t.Helper()
	// The shape is POSIX-specific: a symlink to a directory is enumerable but
	// unreadable there. Windows either refuses the symlink outright ("a required
	// privilege is not held by the client") or resolves it differently, so the
	// platform cannot produce this fixture — skip with the reason rather than
	// assert a state it does not have. The branch stays covered on Linux, which
	// is where the coverage gate measures (testing-strategy §5).
	if runtime.GOOS == "windows" {
		t.Skip("a symlink-to-directory object is not the POSIX enumerable-but-unreadable shape on Windows")
	}
	target := t.TempDir()
	path := objectDir(base, h)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("symlink fixture: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("this platform cannot create a symlink: %v", err)
	}
}

// TestVerifyAllRecordsAnUnreadableObject pins the sweep's non-corrupt failure
// branch against a real unreadable object: the sweep can enumerate it but not
// read it, so it is recorded as not-verified with the viewer's "Unreadable"
// prose, counted as neither verified nor corrupt, and the cause goes to the
// audit line rather than the page (viewer-design §3, viewer-security §9).
func TestVerifyAllRecordsAnUnreadableObject(t *testing.T) {
	ctx := context.Background()
	task := newRouteTask(t, Config{StartupToken: testStartupToken})
	good := tlvEnvelope("blob@1", []byte("sound"))
	goodDigest := sha256.Of(good)
	if err := task.srv.store.Put(ctx, goodDigest, bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	unreadable := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	symlinkObjectFixture(t, task.base, unreadable)
	if _, err := task.srv.verifyObject(ctx, unreadable); err == nil {
		t.Fatal("the fixture must be unreadable, so this test proves something")
	}

	var logs bytes.Buffer
	restore := captureLogs(&logs)
	defer restore()

	admin := login(t, task.ts, testStartupToken)
	page := getBody(t, admin, task.ts.URL+"/viewer/objects")
	// Both objects are listed: the walk enumerates the unreadable one, so the
	// sweep below is handed it.
	if !strings.Contains(page, "of 2") {
		t.Fatalf("the unreadable object is not listed: %.600q", page)
	}

	csrf := csrfFromPage(page)
	resp, err := admin.PostForm(task.ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), ">Verify<") {
		t.Fatalf("verify-all = (%d, %.300q), want a 200 with the plain label", resp.StatusCode, body)
	}

	// The two outcomes reach the table independently: one verified, one not,
	// and the unreadable one is never reported as corrupted content.
	refreshed := getBody(t, admin, task.ts.URL+"/viewer/objects")
	if !strings.Contains(refreshed, "viewer-status-verified") || !strings.Contains(refreshed, "viewer-status-not-verified") {
		t.Fatalf("the sweep did not record both outcomes: %.800q", refreshed)
	}
	if strings.Contains(refreshed, "viewer-status-corrupt") {
		t.Fatalf("an unreadable object must not be reported as corrupt: %.800q", refreshed)
	}

	// The inspector replays the finding as the viewer's own prose.
	inspector := getBody(t, admin, task.ts.URL+"/viewer/objects?selected="+unreadable.String()+"&tab=metadata")
	if !strings.Contains(inspector, "Unreadable") {
		t.Fatalf("the replayed finding does not name the unreadable object: %.800q", inspector)
	}

	audit := logs.String()
	if !strings.Contains(audit, "object.verify-unreadable") || !strings.Contains(audit, unreadable.String()) {
		t.Fatalf("the sweep's audit line does not record the unreadable object:\n%s", audit)
	}
	if !strings.Contains(audit, "object.verify-all") || !strings.Contains(audit, "not-verified=1") || !strings.Contains(audit, "verified=1") {
		t.Fatalf("the sweep's summary does not count both outcomes:\n%s", audit)
	}
}

// TestVerifyAllRecordsAMissingObject pins the same non-corrupt classification
// for an object that is gone: the per-object action reports the viewer's
// "Missing" prose, never corrupted content, because an absent object was never
// verified — the distinction cas.VerifyAll makes too.
func TestVerifyAllRecordsAMissingObject(t *testing.T) {
	ctx := context.Background()
	task := newRouteTask(t, Config{StartupToken: testStartupToken})
	deleted := tlvEnvelope("blob@1", []byte("vanishes"))
	deletedDigest := sha256.Of(deleted)
	if err := task.srv.store.Put(ctx, deletedDigest, bytes.NewReader(deleted)); err != nil {
		t.Fatal(err)
	}

	admin := login(t, task.ts, testStartupToken)
	deletedHash := deletedDigest.String()
	page := getBody(t, admin, task.ts.URL+"/viewer/objects?selected="+url.QueryEscape(deletedHash)+"&tab=metadata")
	csrf := csrfFromPage(page)
	if !strings.Contains(page, deletedHash) {
		t.Fatalf("the object under test is not selectable: %.600q", page)
	}
	if err := task.srv.store.Delete(ctx, deletedDigest); err != nil {
		t.Fatal(err)
	}

	resp, err := admin.PostForm(task.ts.URL+"/viewer/objects/"+deletedHash+"/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify of a deleted object = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Missing") {
		t.Fatalf("verify of a deleted object = %.400q, want the viewer's own Missing prose", body)
	}
	if strings.Contains(string(body), "corrupt") {
		t.Fatalf("a missing object must not be reported as corrupt: %.400q", body)
	}
}

// routeTask is the fixture the route-level gap tests share: a running viewer
// over a store in its own temporary directory, with the store's base path kept
// so a test can break the store underneath it.
type routeTask struct {
	ts   *httptest.Server
	srv  *Server
	base string
}

func newRouteTask(t *testing.T, cfg Config) *routeTask {
	t.Helper()
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &routeTask{ts: ts, srv: srv, base: base}
}

// TestMetadataSnapshotRebuildsWhenThePublishedOneIsStale pins the two rebuild
// branches of the snapshot cache: a request arriving before anything was
// published builds one, and a published snapshot older than the refresh
// interval is replaced rather than served. The second request must see the
// object the first could not, which is what makes the rebuild observable.
func TestMetadataSnapshotRebuildsWhenThePublishedOneIsStale(t *testing.T) {
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	srv.expensive = newExpensiveOpsWith(0, func() time.Time { return time.Unix(0, 0) })

	// Cold start: nothing has been published, so the first caller walks the
	// store and publishes what it found.
	snapshot, err := srv.metadataSnapshot(context.Background(), "")
	if err != nil {
		t.Fatalf("cold-start metadataSnapshot: %v", err)
	}
	if snapshot.Total != 0 {
		t.Fatalf("cold-start snapshot lists %d objects, want the empty store", snapshot.Total)
	}

	// An object stored after that walk is invisible to the published snapshot,
	// so the only way the next caller can see it is a rebuild.
	env := tlvEnvelope("blob@1", []byte("appeared later"))
	h := sha256.Of(env)
	if err := backend.Put(context.Background(), h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	if stale, err := srv.metadataSnapshot(context.Background(), ""); err != nil || stale.Total != 0 {
		t.Fatalf("fresh snapshot = (%v, total %d), want the published one", err, stale.Total)
	}

	// Age the published snapshot past the refresh interval without sleeping:
	// the stamp is what the freshness rule reads.
	published := srv.snapshot.Load()
	if published == nil {
		t.Fatal("the first walk published no snapshot")
	}
	srv.snapshot.Store(&snapshotState{snapshot: published.snapshot, builtAt: published.builtAt.Add(-time.Hour)})

	rebuilt, err := srv.metadataSnapshot(context.Background(), "")
	if err != nil {
		t.Fatalf("stale metadataSnapshot: %v", err)
	}
	if rebuilt.Total != 1 || rebuilt.Entries[0].Digest.String() != h.String() {
		t.Fatalf("rebuilt snapshot = %+v, want the object stored after the first walk", rebuilt)
	}
}

// TestMetadataSnapshotServesTheStaleSnapshotWhenRefused pins the refusal that
// is reachable: with a snapshot published — however old — a session that has
// spent its budget is served it instead of forcing another store walk
// (viewer-design §3). The same test pins the cache's pass-through: a snapshot
// inside the refresh interval is served without consulting the limiter at all.
func TestMetadataSnapshotServesTheStaleSnapshotWhenRefused(t *testing.T) {
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	srv.expensive = newExpensiveOpsWith(time.Minute, time.Now)
	srv.expensive.burst = 1

	// A fresh published snapshot is served as-is, whatever the budget says.
	fresh := &index.Snapshot{Total: 7}
	srv.snapshot.Store(&snapshotState{snapshot: fresh, builtAt: time.Now()})
	if got, err := srv.metadataSnapshot(context.Background(), "anything"); err != nil || got != fresh {
		t.Fatalf("fresh metadataSnapshot = (%v, %v), want the published snapshot", got, err)
	}

	// Spend the session's budget, then age the stamp past the refresh interval
	// so only the refusal can answer. The served snapshot is the published one,
	// not a new walk.
	if _, ok := srv.expensive.begin("spent"); !ok {
		t.Fatal("the fixture could not spend the session's token")
	}
	srv.expensive.end()
	srv.snapshot.Store(&snapshotState{snapshot: fresh, builtAt: time.Now().Add(-time.Hour)})

	served, err := srv.metadataSnapshot(context.Background(), "spent")
	if err != nil {
		t.Fatalf("refused rebuild with a published snapshot: %v", err)
	}
	if served != fresh {
		t.Fatalf("refused rebuild served %p, want the published snapshot %p", served, fresh)
	}

	// A session with budget left walks the store and replaces it.
	env := tlvEnvelope("blob@1", []byte("later"))
	h := sha256.Of(env)
	if err := backend.Put(context.Background(), h, bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := srv.metadataSnapshot(context.Background(), "fresh")
	if err != nil {
		t.Fatalf("admitted rebuild: %v", err)
	}
	if rebuilt.Total != 1 {
		t.Fatalf("admitted rebuild lists %d objects, want the store's one", rebuilt.Total)
	}
}

// TestRebuildSnapshotReportsAWalkFailure pins the snapshot builder's error
// contract: a store walk that fails publishes nothing and reports the failure,
// so the next caller retries instead of serving a snapshot built from a partial
// walk. A canceled request context is that failure.
func TestRebuildSnapshotReportsAWalkFailure(t *testing.T) {
	base := t.TempDir()
	backend, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	env := tlvEnvelope("blob@1", []byte("stored"))
	if err := backend.Put(context.Background(), sha256.Of(env), bytes.NewReader(env)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot, err := srv.rebuildSnapshot(ctx); err == nil {
		t.Fatalf("rebuildSnapshot(canceled) = (%v, nil), want the walk failure", snapshot)
	}
	if srv.snapshot.Load() != nil {
		t.Fatal("a failed walk must publish nothing")
	}

	// The next caller with a live context builds and publishes the snapshot.
	snapshot, err := srv.rebuildSnapshot(context.Background())
	if err != nil || snapshot.Total != 1 {
		t.Fatalf("rebuildSnapshot after the failure = (%v, %v), want the store's one object", snapshot, err)
	}
	if srv.snapshot.Load() == nil {
		t.Fatal("a successful walk must publish its snapshot")
	}
}
