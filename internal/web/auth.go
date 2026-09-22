// Login, role authorization, and the per-request session lookups the
// handlers authorize with.

package web

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
)

// Roles (viewer-security §8).
const (
	// RoleViewer permits read-only viewer access.
	RoleViewer = "viewer"
	// RoleOperator permits verification operations.
	RoleOperator = "operator"
	// RoleAdmin is the highest rank. The viewer exposes no destructive
	// operation, so it currently gates nothing the operator rank does not
	// already reach; it stays because the ladder, not the viewer, defines it.
	RoleAdmin = "admin"
)

// require enforces: valid session (401 empty), sufficient role (403 empty),
// CSRF on mutations (403 empty). Never discloses existence.
func (s *Server) require(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessions.get(sessionID(r))
		if !ok {
			w.WriteHeader(http.StatusUnauthorized) // empty body
			return
		}
		if sess.Role != role && !roleAllows(sess.Role, role) {
			w.WriteHeader(http.StatusForbidden) // empty body
			return
		}
		if !csrfOK(r, sess) {
			slog.Warn("viewer csrf rejected", "path", r.URL.Path, "session", sessionHandle(sess.ID))
			w.WriteHeader(http.StatusForbidden) // empty body
			return
		}
		next(w, r)
	}
}

// roleAllows reports whether the session role satisfies the required role
// (admin ⊃ operator ⊃ viewer).
func roleAllows(sessionRole, required string) bool {
	rank := map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}
	return rank[sessionRole] >= rank[required]
}

// --- login ---

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "login", nil)
}

// loginPost validates the submitted token against the startup token (admin)
// or the configured per-role tokens, throttles failures per IP (5/min with
// backoff, viewer-security §5), and issues a session cookie.
func (s *Server) loginToken(w http.ResponseWriter, r *http.Request, token string) {
	ip := callerIP(r)
	if !s.loginThrottle.allow(ip) {
		slog.Warn("viewer login throttled", "ip", ip)
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	role, ok := s.resolveToken(token)
	if !ok {
		// The attempt was already recorded by allow.
		slog.Warn("viewer login failed", "ip", ip) // token value never logged
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	s.loginThrottle.reset(ip)
	sess, err := s.sessions.create(role)
	if err != nil {
		slog.Error("viewer login", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, sess)
	slog.Info("viewer login", "role", role, "ip", ip, "session", sessionHandle(sess.ID))
	http.Redirect(w, r, "/viewer/", http.StatusSeeOther)
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	s.loginToken(w, r, r.FormValue("token"))
}

// resolveToken matches token against the startup token (admin role) and the
// configured per-role tokens (token → role) using constant-time comparison.
// An empty token never matches, so a mis-configured empty token cannot grant
// a role (viewer-security §5).
func (s *Server) resolveToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.StartupToken)) == 1 {
		return RoleAdmin, true
	}
	for tok, role := range s.cfg.RoleTokens {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(token)) == 1 {
			return role, true
		}
	}
	return "", false
}

func (s *Server) csrfFor(r *http.Request) string {
	if sess, ok := s.sessions.get(sessionID(r)); ok {
		return sess.CSRF
	}
	return ""
}

func (s *Server) roleFor(r *http.Request) string {
	if sess, ok := s.sessions.get(sessionID(r)); ok {
		return sess.Role
	}
	return ""
}
