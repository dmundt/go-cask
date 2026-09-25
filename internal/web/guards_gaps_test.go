// Tests closing the uncovered branches of the session trail, the role ladder,
// the login-throttle backoff, and the caller-identity helpers: the small
// guards whose behavior is only visible when the caller is not the one the
// happy path assumes.

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	sha512 "github.com/dmundt/go-cask/cas/hash/sha512"
)

// TestSessionTrailGuards pins the trail's behavior for the callers it must
// ignore: an unknown session, an empty digest, and a revisit of the entry the
// cursor already names. None of them may move the cursor, and the cursor must
// still step through the entries that are there.
func TestSessionTrailGuards(t *testing.T) {
	store := newSessions()
	sess, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	const unknown = "no-such-session"

	t.Run("visit ignores an unknown session and an empty digest", func(t *testing.T) {
		store.visit(unknown, "aaa")
		store.visit(sess.ID, "")
		if prev, next := store.trailNeighbors(sess.ID); prev != "" || next != "" {
			t.Fatalf("trail after ignored visits = (%q, %q), want an empty trail", prev, next)
		}
	})

	t.Run("visit keeps the cursor on a repeated entry", func(t *testing.T) {
		store.visit(sess.ID, "aaa")
		store.visit(sess.ID, "bbb")
		// Revisiting the current entry is not a move: the trail keeps both
		// entries and the cursor stays on the second.
		store.visit(sess.ID, "bbb")
		prev, next := store.trailNeighbors(sess.ID)
		if prev != "aaa" || next != "" {
			t.Fatalf("trail after a repeated visit = (%q, %q), want (aaa, \"\")", prev, next)
		}
	})
}

// TestSessionTrailDropsTheOldestEntries pins the trail's bound: it is a
// navigation aid, not an audit log, so a long session drops its oldest entries
// rather than growing without end.
func TestSessionTrailDropsTheOldestEntries(t *testing.T) {
	store := newSessions()
	sess, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	extra := 5
	for i := range maxTrail + extra {
		store.visit(sess.ID, "digest-"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('0'+i%10)))
	}
	store.mu.Lock()
	trail, pos := append([]string(nil), sess.Trail...), sess.TrailPos
	store.mu.Unlock()
	if len(trail) != maxTrail {
		t.Fatalf("trail holds %d entries, want it bounded at %d", len(trail), maxTrail)
	}
	if pos != maxTrail-1 {
		t.Fatalf("cursor at %d, want it on the newest entry (%d)", pos, maxTrail-1)
	}
}

// TestSessionRestartAndSeekGuards pins the two navigation calls that must
// ignore a caller the trail does not belong to: restart and seek leave another
// session's trail untouched, and restart discards the forward entries a new
// branch replaces.
func TestSessionRestartAndSeekGuards(t *testing.T) {
	store := newSessions()
	sess, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	store.visit(sess.ID, "aaa")
	store.visit(sess.ID, "bbb")

	const unknown = "no-such-session"
	store.restart(unknown, "ccc")
	store.restart(sess.ID, "")
	store.seek(unknown, "aaa")

	prev, next := store.trailNeighbors(sess.ID)
	if prev != "aaa" || next != "" {
		t.Fatalf("trail after ignored calls = (%q, %q), want (aaa, \"\")", prev, next)
	}

	// A seek onto an entry the trail does not hold leaves the cursor where it
	// was; a seek onto one it does hold moves it there.
	store.seek(sess.ID, "aaa")
	if prev, next = store.trailNeighbors(sess.ID); prev != "" || next != "bbb" {
		t.Fatalf("trail after seek(aaa) = (%q, %q), want (\"\", bbb)", prev, next)
	}
	store.seek(sess.ID, "missing")
	if prev, next = store.trailNeighbors(sess.ID); prev != "" || next != "bbb" {
		t.Fatalf("seek of an unstored digest moved the cursor: (%q, %q)", prev, next)
	}

	// A restart begins a new trail at the named object.
	store.restart(sess.ID, "ccc")
	if prev, next = store.trailNeighbors(sess.ID); prev != "" || next != "" {
		t.Fatalf("trail after restart = (%q, %q), want a single-entry trail", prev, next)
	}
	store.mu.Lock()
	trail := append([]string(nil), sess.Trail...)
	store.mu.Unlock()
	if len(trail) != 1 || trail[0] != "ccc" {
		t.Fatalf("trail after restart = %v, want [ccc]", trail)
	}
}

// TestSessionTrailNeighborsForUnknownSession pins the empty answer an unknown
// session gets, which is what keeps the Prev/Next controls disabled rather than
// pointing at a neighbour that does not exist.
func TestSessionTrailNeighborsForUnknownSession(t *testing.T) {
	store := newSessions()
	if prev, next := store.trailNeighbors("no-such-session"); prev != "" || next != "" {
		t.Fatalf("trailNeighbors(unknown) = (%q, %q), want empty", prev, next)
	}
}

// TestSessionGetDropsAnExpiredSession pins the lazy half of session expiry: a
// session that passes its idle timeout while the store still holds it is
// deleted by the read that asks for it, so a stale cookie cannot be replayed
// after the operator walked away.
func TestSessionGetDropsAnExpiredSession(t *testing.T) {
	store := newSessions()
	sess, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.byID[sess.ID].LastSeen = time.Now().Add(-idleTimeout - time.Minute)
	store.mu.Unlock()

	got, ok := store.get(sess.ID)
	if ok || got != nil {
		t.Fatalf("get(expired session) = (%v, %v), want (nil, false)", got, ok)
	}
	store.mu.Lock()
	_, stillThere := store.byID[sess.ID]
	store.mu.Unlock()
	if stillThere {
		t.Fatal("an expired session must be deleted by the read that found it expired")
	}
}

// TestVerificationReportForUnknownSession pins the neutral answer an unknown
// session gets: no result, no check time, no report. It is the state the
// inspector renders as unchecked, so it must not be confusable with a stored
// finding.
func TestVerificationReportForUnknownSession(t *testing.T) {
	store := newSessions()
	result, checked, report := store.verificationReport("no-such-session", "digest")
	if result != "not-verified" || !checked.IsZero() || report != (actionOutcome{}) {
		t.Fatalf("verificationReport(unknown) = (%q, %v, %+v), want the neutral answer", result, checked, report)
	}
	if got := store.verification("no-such-session", "digest"); got != "not-verified" {
		t.Fatalf("verification(unknown) = %q, want not-verified", got)
	}
}

// TestRoleRankOfAnUnknownRole pins the bottom of the ladder: a role the viewer
// does not know ranks below every named role, so a session carrying one reaches
// no route. Two unknown roles still compare equal, because the rank is what the
// ladder means.
func TestRoleRankOfAnUnknownRole(t *testing.T) {
	if got := roleRank("root"); got != 0 {
		t.Fatalf("roleRank(unknown) = %d, want 0", got)
	}
	if roleAllows("root", RoleViewer) {
		t.Fatal("an unknown role must not satisfy the viewer role")
	}
	if roleRank(RoleViewer) != 1 || roleRank(RoleOperator) != 2 || roleRank(RoleAdmin) != 3 {
		t.Fatalf("role ranks = (%d, %d, %d), want (1, 2, 3)",
			roleRank(RoleViewer), roleRank(RoleOperator), roleRank(RoleAdmin))
	}
}

// TestLoginWithAnUnknownRoleTokenIsForbiddenAtEveryRoute pins the consequence
// of that rank: a session minted from a token mapped to an unknown role is
// authenticated but authorized for nothing, which is a 403, not a 500 and never
// a page.
func TestLoginWithAnUnknownRoleTokenIsForbiddenAtEveryRoute(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{
		StartupToken: testStartupToken,
		RoleTokens:   map[string]string{"mystery-tok": "mystery"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)

	mystery := login(t, ts, "mystery-tok")
	for _, path := range []string{"/viewer/objects", "/viewer/objects/" + strings.Repeat("ab", 32)} {
		if got := statusCode(t, mystery, ts.URL+path); got != http.StatusForbidden {
			t.Errorf("GET %s with an unknown role = %d, want 403", path, got)
		}
	}
}

// TestThrottleRetryAfterFloorsAtOneSecond pins the whole-second floor on the
// advertised wait: an address the throttle has never seen, and a block with
// less than a second left, both answer one second rather than zero.
func TestThrottleRetryAfterFloorsAtOneSecond(t *testing.T) {
	th := newThrottle(2, time.Minute)
	if got := th.retryAfter("never-seen"); got != time.Second {
		t.Fatalf("retryAfter(unknown address) = %v, want 1s", got)
	}
	th.mu.Lock()
	th.attempts["blocked"] = &ipState{blockedUntil: time.Now().Add(200 * time.Millisecond)}
	th.mu.Unlock()
	if got := th.retryAfter("blocked"); got != time.Second {
		t.Fatalf("retryAfter(200ms remaining) = %v, want the 1s floor", got)
	}
}

// TestThrottleBackoffCapsAtThirtyMinutes pins both ends of the exponential
// backoff: it doubles per exhaustion and never exceeds maxBackoff, whether the
// cap is reached by doubling or by a window already larger than it.
func TestThrottleBackoffCapsAtThirtyMinutes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		window  time.Duration
		strikes int
		want    time.Duration
	}{
		{"one strike is one window", time.Minute, 1, time.Minute},
		{"two strikes double it", time.Minute, 2, 2 * time.Minute},
		{"three strikes double it again", time.Minute, 3, 4 * time.Minute},
		{"doubling stops at the cap", time.Minute, 10, maxBackoff},
		{"a window past the cap is capped", time.Hour, 1, maxBackoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := newThrottle(1, tc.window)
			if got := th.backoff(tc.strikes); got != tc.want {
				t.Fatalf("backoff(window %v, %d strikes) = %v, want %v", tc.window, tc.strikes, got, tc.want)
			}
			if got := th.backoff(tc.strikes); got > maxBackoff {
				t.Fatalf("backoff(window %v, %d strikes) = %v, want it capped at %v", tc.window, tc.strikes, got, maxBackoff)
			}
		})
	}
}

// TestPeerIPWithoutAPort pins the peer-address reader's fallback: an address
// the server cannot split is returned verbatim, which cannot parse as an IP and
// so matches no trusted proxy — the caller is keyed on its own bucket rather
// than borrowing one.
func TestPeerIPWithoutAPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/viewer/login", nil)
	r.RemoteAddr = "not-an-address"
	if got := peerIP(r); got != "not-an-address" {
		t.Fatalf("peerIP(unsplittable) = %q, want it verbatim", got)
	}
	trusted, err := newTrustedProxy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{trusted: trusted}
	if got := srv.callerIP(r); got != "not-an-address" {
		t.Fatalf("callerIP(unsplittable peer) = %q, want it verbatim", got)
	}
}

// TestForwardedForWithoutAForParameter pins the RFC 7239 reader's two negative
// answers: a Forwarded header that carries no `for=` parameter at all, and one
// whose value cannot be read, both report no address, so the caller is keyed on
// the direct peer instead of on something the header merely looks like.
func TestForwardedForWithoutAForParameter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
	}{
		{"no parameters at all", "by=203.0.113.43"},
		{"a for parameter with no value", "for="},
		{"a for parameter with an unreadable value", "for=,"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := forwardedFor(tc.header); ok {
				t.Fatalf("forwardedFor(%q) = (%q, true), want no address", tc.header, got)
			}
		})
	}
}

// TestForwardedClientWithNoUsableForwardedHeader pins the trust boundary's last
// fallback: a trusted proxy that presents a header the viewer cannot read
// supplies no client address, and the proxy speaks for itself.
func TestForwardedClientWithNoUsableForwardedHeader(t *testing.T) {
	trusted, err := newTrustedProxy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/viewer/login", nil)
	r.RemoteAddr = "10.0.0.5:41234"
	r.Header.Set("Forwarded", "by=203.0.113.43")
	srv := &Server{trusted: trusted}
	if got := srv.callerIP(r); got != "10.0.0.5" {
		t.Fatalf("callerIP(trusted peer with an unreadable Forwarded) = %q, want the peer", got)
	}
}

// TestNodeAddressShapes pins the RFC 7239 node reader: a bare address is
// returned as itself, a bracketed IPv6 literal loses its brackets, a value with
// brackets and a port loses both, and anything that is not an address — an
// obfuscated identifier — is returned verbatim so it becomes its own bucket
// rather than another client's.
func TestNodeAddressShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"a bare IPv4 address", "203.0.113.9", "203.0.113.9"},
		{"a bare IPv6 address", "2001:db8::7", "2001:db8::7"},
		{"a bracketed IPv6 literal", "[2001:db8::7]", "2001:db8::7"},
		{"an address with a port", "203.0.113.9:4711", "203.0.113.9"},
		{"an obfuscated identifier", "_hidden", "_hidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeAddress(tc.value); got != tc.want {
				t.Fatalf("nodeAddress(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestTrustedPeerWithAnEmptyHopFallsBackToThePeer pins the forwarded-chain
// walk's end: a chain whose entries are all empty carries no client address at
// all, so the trusted proxy speaks for itself rather than the viewer inventing
// one.
func TestTrustedPeerWithAnEmptyHopFallsBackToThePeer(t *testing.T) {
	trusted, err := newTrustedProxy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/viewer/login", nil)
	r.RemoteAddr = "10.0.0.5:41234"
	r.Header.Set("X-Forwarded-For", ", ")
	srv := &Server{trusted: trusted}
	if got := srv.callerIP(r); got != "10.0.0.5" {
		t.Fatalf("callerIP(all-empty forwarded chain) = %q, want the peer", got)
	}
}

// TestRenderFailsSafelyForAnUndefinedTemplate pins the render helper's error
// contract: a template name the shell does not define is an internal fault, so
// the caller gets a clean 500 in the viewer's own words rather than a
// half-written 200, and a defined template still renders with its content type.
func TestRenderFailsSafelyForAnUndefinedTemplate(t *testing.T) {
	srv, err := New(nil, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.render(rec, "no-such-template", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("render(undefined) = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "template error" {
		t.Fatalf("render(undefined) body = %q, want the viewer's own prose", got)
	}

	rec = httptest.NewRecorder()
	srv.render(rec, "hexdump-table", struct {
		Rows []dumpRow
		Note string
	}{Note: "nothing to show"})
	if rec.Code != http.StatusOK {
		t.Fatalf("render(defined) = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("render(defined) Content-Type = %q, want text/html", got)
	}
	if !strings.Contains(rec.Body.String(), "nothing to show") {
		t.Fatalf("render(defined) did not render its data: %.200q", rec.Body.String())
	}
}

// TestNewDefaultsTheHasherAndAlgorithm pins the two composition defaults: the
// shipped SHA-256 hasher and its display name are supplied when the host omits
// them, so a viewer wired with nothing but a startup token still parses and
// verifies digests.
func TestNewDefaultsTheHasherAndAlgorithm(t *testing.T) {
	srv, err := New(nil, Config{StartupToken: testStartupToken})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.cfg.HashAlgorithm; got != "sha256" {
		t.Fatalf("default HashAlgorithm = %q, want sha256", got)
	}
	if srv.cfg.Hasher == nil {
		t.Fatal("default Hasher is nil, want the shipped SHA-256 hasher")
	}
	h := mustParse(t, "sha256:"+strings.Repeat("ab", 32))
	if err := srv.cfg.Hasher.Validate(h); err != nil {
		t.Fatalf("the default hasher refused a SHA-256 digest: %v", err)
	}
	if err := sha256.New().Validate(h); err != nil {
		t.Fatalf("the shipped hasher refused one of its own digests: %v", err)
	}
}

// TestNewKeepsTheHasherTheHostSupplied pins the other half: an embedding host
// that wires another algorithm keeps both its hasher and the name the viewer
// displays for it.
func TestNewKeepsTheHasherTheHostSupplied(t *testing.T) {
	srv, err := New(nil, Config{StartupToken: testStartupToken, Hasher: sha512.New(), HashAlgorithm: "sha512"})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.cfg.HashAlgorithm; got != "sha512" {
		t.Fatalf("explicit HashAlgorithm = %q, want it kept", got)
	}
	h := sha512.Of([]byte("payload"))
	if err := srv.cfg.Hasher.Validate(h); err != nil {
		t.Fatalf("the supplied hasher refused one of its own digests: %v", err)
	}
	if err := srv.cfg.Hasher.Validate(mustParse(t, "sha256:"+strings.Repeat("ab", 32))); err == nil {
		t.Fatal("the supplied SHA-512 hasher accepted a 32-byte digest")
	}
}
