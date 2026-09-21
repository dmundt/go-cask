// Package web implements the embedded technical viewer (internal/web): the
// browser-facing hypermedia surface at /viewer/* — login with the startup
// token, session cookies, role authorization, CSRF-protected mutations,
// htmx fragments, and the dashboard/object/stats/graph pages — per
// viewer-design and viewer-security (which MUST NOT be weakened).
package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
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
	Verifications map[string]string
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
		Verifications: make(map[string]string),
	}
	s.mu.Lock()
	s.byID[sess.ID] = sess
	s.mu.Unlock()
	return sess, nil
}

func (s *sessions) verification(id, digest string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[id]
	if !ok {
		return "not-verified"
	}
	if result, ok := sess.Verifications[digest]; ok {
		return result
	}
	return "not-verified"
}

func (s *sessions) setVerification(id, digest, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byID[id]; ok {
		sess.Verifications[digest] = result
	}
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
	if now.Sub(sess.LastSeen) > idleTimeout || now.Sub(sess.Created) > maxLifetime {
		delete(s.byID, id)
		return nil, false
	}
	sess.LastSeen = now
	return sess, true
}

func (s *sessions) delete(id string) {
	s.mu.Lock()
	delete(s.byID, id)
	s.mu.Unlock()
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

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
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

// randomHex returns n cryptographically random bytes as lowercase hex, or
// an error if the OS entropy source cannot be read.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("web: crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
