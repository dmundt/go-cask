// Package web implements the embedded technical viewer (internal/web): the
// browser-facing hypermedia surface at /viewer/* — login with the startup
// token, session cookies, role authorization, CSRF-protected mutations,
// htmx fragments and object pages — per
// viewer-design and viewer-security (which MUST NOT be weakened).
package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Session lifetimes (viewer-security §6).
const (
	idleTimeout   = 30 * time.Minute
	maxLifetime   = 8 * time.Hour
	sessionCookie = "cask_session"
)

// Session is one authenticated viewer session; sessions carry exactly one
// role resolved at login (viewer-security §5.1).
type Session struct {
	// ID is the session identifier.
	ID string
	// Role is the authorized viewer role.
	Role string
	// Created is the session creation time.
	Created time.Time
	// LastSeen is the most recent authenticated request time.
	LastSeen time.Time
	// CSRF is the per-session CSRF token.
	CSRF string
	// Verifications holds the session-scoped integrity result for each object.
	// Results disappear when the session expires or the server restarts.
	Verifications map[string]verification
	// Trail is the ordered list of objects inspected in this session, and
	// TrailPos points at the current one (-1 while the trail is empty). It
	// backs the inspector's Prev/Next controls, which walk only objects this
	// session already visited — browser history would also replay filter and
	// sort changes, which are not object navigation.
	Trail    []string
	TrailPos int
}

// maxTrail bounds the session-scoped visit trail. It is a navigation aid, not
// an audit log, so the oldest entries are dropped rather than grown without end.
const maxTrail = 100

// verification is one recorded integrity check: its result, when it ran, and
// the report the check produced. The time makes a stale result visible as
// stale — bytes can rot after a check — and keeping the report lets the
// inspector show the finding again on every later visit instead of only in the
// response to the click that produced it.
type verification struct {
	// Result is the recorded integrity state.
	Result string
	// Checked is when the check ran.
	Checked time.Time
	// Report is the outcome the check rendered, replayed on reselection.
	Report actionOutcome
}

// sessions is the in-memory session store: idle timeout and maximum
// lifetime are enforced on access; sessions disappear on restart.
type sessions struct {
	mu   sync.Mutex
	byID map[string]*Session
}

func newSessions() *sessions { return &sessions{byID: make(map[string]*Session)} }

func (s *sessions) create(role string) (*Session, error) {
	id, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	sess := &Session{
		ID:            id,
		Role:          role,
		Created:       time.Now(),
		LastSeen:      time.Now(),
		CSRF:          csrf,
		Verifications: make(map[string]verification),
		TrailPos:      -1,
	}
	s.mu.Lock()
	s.sweepLocked(time.Now())
	s.byID[sess.ID] = sess
	s.mu.Unlock()
	return sess, nil
}

// expiredLocked reports whether sess has passed its idle timeout or its
// maximum lifetime (viewer-security §6).
func expiredLocked(sess *Session, now time.Time) bool {
	return now.Sub(sess.LastSeen) > idleTimeout || now.Sub(sess.Created) > maxLifetime
}

// sweepLocked drops every expired session. get() expires a session it is asked
// for, but an abandoned session is never asked for again, so without this
// sweep it would live until the process exits — holding a verification record
// per object it ever checked. Login is the natural moment to run it: it is the
// only operation that grows the map, and it is rare.
func (s *sessions) sweepLocked(now time.Time) {
	for id, sess := range s.byID {
		if expiredLocked(sess, now) {
			delete(s.byID, id)
		}
	}
}

func (s *sessions) verification(id, digest string) string {
	record, _ := s.verificationRecord(id, digest)
	return record
}

// verificationRecord reports the recorded result and when it was checked. The
// zero time means the object has not been checked in this session.
func (s *sessions) verificationRecord(id, digest string) (string, time.Time) {
	record, checked, _ := s.verificationReport(id, digest)
	return record, checked
}

// verificationReport adds the stored report to verificationRecord, so the
// inspector can render a past finding without re-running the check.
func (s *sessions) verificationReport(id, digest string) (string, time.Time, actionOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return "not-verified", time.Time{}, actionOutcome{}
	}
	if record, ok := sess.Verifications[digest]; ok {
		return record.Result, record.Checked, record.Report
	}
	return "not-verified", time.Time{}, actionOutcome{}
}

func (s *sessions) setVerification(id, digest, result string, report actionOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byID[id]; ok {
		sess.Verifications[digest] = verification{Result: result, Checked: time.Now(), Report: report}
	}
}

// visit records digest as the newest trail entry. Revisiting the current entry
// changes nothing, and visiting after stepping back drops the forward entries:
// a new branch replaces the abandoned one, exactly as browser history does.
func (s *sessions) visit(id, digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok || digest == "" {
		return
	}
	if sess.TrailPos >= 0 && sess.TrailPos < len(sess.Trail) && sess.Trail[sess.TrailPos] == digest {
		return
	}
	sess.Trail = append(sess.Trail[:sess.TrailPos+1], digest)
	if len(sess.Trail) > maxTrail {
		sess.Trail = sess.Trail[len(sess.Trail)-maxTrail:]
	}
	sess.TrailPos = len(sess.Trail) - 1
}

// restart begins a new trail at digest, discarding what came before. Picking a
// row in the object table is a fresh point of departure, so the Prev/Next
// controls must not offer a chain the operator abandoned.
func (s *sessions) restart(id, digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok || digest == "" {
		return
	}
	sess.Trail = []string{digest}
	sess.TrailPos = 0
}

// seek moves the cursor onto digest without disturbing the trail. It backs
// Prev/Next, which navigate the trail rather than extend it.
func (s *sessions) seek(id, digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return
	}
	if i := slices.Index(sess.Trail, digest); i >= 0 {
		sess.TrailPos = i
	}
}

// trailNeighbors reports the objects on either side of the cursor. An empty
// string means that direction is exhausted, which disables its control.
func (s *sessions) trailNeighbors(id string) (prev, next string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return "", ""
	}
	if sess.TrailPos > 0 {
		prev = sess.Trail[sess.TrailPos-1]
	}
	if sess.TrailPos >= 0 && sess.TrailPos+1 < len(sess.Trail) {
		next = sess.Trail[sess.TrailPos+1]
	}
	return prev, next
}

// get returns the session for id, enforcing idle and lifetime expiry and
// updating LastSeen. A session that has not been used within idleTimeout or
// is older than maxLifetime is deleted and reported absent.
func (s *sessions) get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if expiredLocked(sess, now) {
		delete(s.byID, id)
		return nil, false
	}
	sess.LastSeen = now
	return sess, true
}

// setCookie writes the session cookie (HttpOnly, SameSite=Strict; Secure).
func setSessionCookie(w http.ResponseWriter, sess *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// sessionID extracts the session id from the request cookie.
func sessionID(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// sessionHandle renders a session identifier for the audit log. The session id
// is the cookie value, and viewer-security §9 requires the log to name the
// session but forbids it from carrying the cookie, so the log gets a one-way
// digest prefix instead: it correlates a session's actions with each other
// without being replayable as a credential.
func sessionHandle(id string) string {
	if id == "" {
		return "anonymous"
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:4])
}

// randomHex returns n cryptographically random bytes as lowercase hex, or
// an error if the OS entropy source cannot be read.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("web: crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
