package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas/backend/fs"
)

// TestVerifyAllRefusalAtTheRoute pins what an operator sees when the session's
// budget is spent: 429, Retry-After, and a fragment that says how long to wait
// instead of a sweep queued behind the running one (viewer-design §3).
func TestVerifyAllRefusalAtTheRoute(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	// The page load that carries the CSRF token spends one token (it builds the
	// metadata snapshot), the first verify-all spends the second, and the third
	// request in the same session is the refusal.
	srv.expensive = newExpensiveOpsWith(time.Minute, time.Now)
	srv.expensive.burst = 2
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)
	csrf := csrfFromPage(getBody(t, admin, ts.URL+"/viewer/objects"))

	resp, err := admin.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first verify-all = %d, want 200", resp.StatusCode)
	}

	resp, err = admin.PostForm(ts.URL+"/viewer/objects/verify", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second verify-all within the cooldown = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want the remaining cooldown in seconds (60)", got)
	}
	if !strings.Contains(string(body), "retry in") {
		t.Fatalf("refusal fragment does not say how long to wait: %.200q", body)
	}
	// The refusal is not a status change, so it must not ask the browser to
	// refresh the object list.
	if trigger := resp.Header.Get("HX-Trigger"); trigger != "" {
		t.Fatalf("refusal carried HX-Trigger %q, want none", trigger)
	}
}

// TestMetadataRouteServesTheStaleSnapshotWhenRefused pins the metadata half of
// the bound: when a session has spent its budget, a refresh serves the
// published snapshot — stale, not wrong — instead of starting another walk.
func TestMetadataRouteServesTheStaleSnapshotWhenRefused(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	srv.expensive = newExpensiveOpsWith(time.Minute, time.Now)
	srv.expensive.burst = 1
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := login(t, ts, testStartupToken)

	// One page load builds and publishes the snapshot and spends the token.
	if body := getBody(t, admin, ts.URL+"/viewer/objects"); body == "" {
		t.Fatal("object browser rendered nothing")
	}
	published := srv.snapshot.Load()
	if published == nil {
		t.Fatal("the first page load published no snapshot")
	}

	// Age the snapshot past the refresh interval; the session is still inside
	// its cooldown, so the next request is refused a rebuild and must serve the
	// published snapshot unchanged.
	time.Sleep(snapshotRefreshInterval + 100*time.Millisecond)
	if body := getBody(t, admin, ts.URL+"/viewer/objects"); body == "" {
		t.Fatal("object browser rendered nothing after the snapshot aged")
	}
	if srv.snapshot.Load() != published {
		t.Fatal("a refused session still rebuilt the snapshot")
	}
}

// TestExpensiveOpsTokenBucket pins the per-session half of the bound: a session
// may start expensiveBurst operations, then waits a cooldown for the next one,
// and the bucket never refills beyond the burst.
func TestExpensiveOpsTokenBucket(t *testing.T) {
	now := time.Unix(0, 0)
	l := newExpensiveOpsWith(time.Minute, func() time.Time { return now })
	l.burst = 2

	for i := range 2 {
		if _, ok := l.begin("session"); !ok {
			t.Fatalf("admission %d refused while the bucket had tokens", i+1)
		}
		l.end()
	}
	retry, ok := l.begin("session")
	if ok {
		t.Fatal("admission beyond the burst was allowed")
	}
	if retry != time.Minute {
		t.Fatalf("retry after the burst = %v, want one cooldown", retry)
	}

	// One cooldown later exactly one token is back.
	now = now.Add(time.Minute)
	if _, ok := l.begin("session"); !ok {
		t.Fatal("the refilled token was not admitted")
	}
	l.end()
	if _, ok := l.begin("session"); ok {
		t.Fatal("a refill granted more than one token")
	} else if retry != time.Minute {
		t.Fatalf("retry after the refilled token = %v, want one cooldown", retry)
	}

	// A long idle period refills no further than the burst.
	now = now.Add(24 * time.Hour)
	for i := range 2 {
		if _, ok := l.begin("session"); !ok {
			t.Fatalf("admission %d after a long idle period refused", i+1)
		}
		l.end()
	}
	if _, ok := l.begin("session"); ok {
		t.Fatal("a long idle period refilled the bucket beyond the burst")
	}
}

// TestExpensiveOpsConcurrencyCap pins the global half: one expensive operation
// runs at a time, a second session is refused while it does, and a busy refusal
// does not spend the refused session's token.
func TestExpensiveOpsConcurrencyCap(t *testing.T) {
	now := time.Unix(0, 0)
	l := newExpensiveOpsWith(time.Minute, func() time.Time { return now })
	l.burst = 3

	if _, ok := l.begin("first"); !ok {
		t.Fatal("the first session was refused an idle slot")
	}
	retry, ok := l.begin("second")
	if ok {
		t.Fatal("a second session ran while the slot was held")
	}
	if retry != busyRetryAfter {
		t.Fatalf("busy retry = %v, want %v", retry, busyRetryAfter)
	}
	// The busy refusal must not have spent the second session's token: after
	// the slot is free it still has all burst admissions.
	l.end()
	for i := range 3 {
		if _, ok := l.begin("second"); !ok {
			t.Fatalf("admission %d of the refused session, want the full burst", i+1)
		}
		l.end()
	}
	if _, ok := l.begin("second"); ok {
		t.Fatal("the refused session had more than its burst")
	}
}

// TestExpensiveOpsZeroCooldownAdmitsEveryRequest pins the test seam: a zero
// cooldown disables the per-session budget (the concurrency cap still applies),
// which is how a test whose subject is not the limiter keeps it out of the way.
func TestExpensiveOpsZeroCooldownAdmitsEveryRequest(t *testing.T) {
	l := newExpensiveOpsWith(0, time.Now)
	for i := range 10 {
		if _, ok := l.begin("session"); !ok {
			t.Fatalf("admission %d refused with a zero cooldown", i+1)
		}
		l.end()
	}
	if _, ok := l.begin("first"); !ok {
		t.Fatal("the first session was refused an idle slot")
	}
	if _, ok := l.begin("second"); ok {
		t.Fatal("a zero cooldown disabled the concurrency cap too")
	}
	l.end()
}

// TestExpensiveOpsSweepsIdleSessions pins the map's bound: a viewer that has
// served many sessions does not accumulate one bucket per session forever.
func TestExpensiveOpsSweepsIdleSessions(t *testing.T) {
	now := time.Unix(0, 0)
	l := newExpensiveOpsWith(time.Minute, func() time.Time { return now })
	for i := range sweepThreshold + 1 {
		l.budgets[string(rune(i))] = &budget{tokens: l.burst, refilled: now.Add(-time.Hour)}
	}
	l.begin("session")
	if len(l.budgets) > 1 {
		t.Fatalf("idle sessions were not swept: %d buckets remain", len(l.budgets))
	}
}

// TestRetryAfterSeconds pins the header's rounding: whole seconds, rounded up,
// never zero.
func TestRetryAfterSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want int
	}{
		{0, 1},
		{time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Millisecond, 2},
		{90 * time.Second, 90},
	} {
		if got := retryAfterSeconds(tc.in); got != tc.want {
			t.Errorf("retryAfterSeconds(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
