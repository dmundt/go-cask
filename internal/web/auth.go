// Login, role authorization, and the per-request session lookups the
// handlers authorize with.

package web

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
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
		if !roleAllows(sess.Role, role) {
			w.WriteHeader(http.StatusForbidden) // empty body
			return
		}
		// The body is parsed under the viewer's bound before the CSRF token is
		// read out of it: an oversized body answers 413 and an unparsable one
		// 400, so a refused mutation is decided by the parse rather than
		// mistaken for a missing token (viewer-security §13).
		if !parseFormBody(w, r) {
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
// (admin ⊃ operator ⊃ viewer). A role always allows itself, so callers compare
// through this function alone rather than testing equality first.
func roleAllows(sessionRole, required string) bool {
	return roleRank(sessionRole) >= roleRank(required)
}

// roleRank ranks one role on the viewer's authorization ladder. An unknown
// role ranks 0, below every named role, so a role the viewer does not know
// never satisfies a route. Two unknown-but-equal roles still compare equal:
// the rank comparison is what the ladder means, and a session carrying an
// unknown role cannot reach any of the routes the viewer registers.
func roleRank(role string) int {
	switch role {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// --- login ---

// loginFailedParam marks the login page reached after a rejected attempt. It
// carries no detail: the reason a token was rejected is never disclosed, and the
// page renders one owned sentence either way.
const loginFailedParam = "failed"

// loginData backs the login page. The rejection itself is answered 401 with an
// empty body (api-design §5), so the page — not the rejected response — is where
// the human-readable reason lives.
type loginData struct {
	// Failed reports that the page was reached after a rejected attempt.
	Failed bool
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "login", loginData{Failed: r.URL.Query().Get(loginFailedParam) != ""})
}

// loginPost validates the submitted token against the startup token (admin)
// or the configured per-role tokens, throttles failures per caller address
// (5/min with backoff, viewer-security §5), and issues a session cookie. The
// caller address is the direct peer unless a configured trusted proxy
// forwarded one (§5.2, proxy.go). It is the only way besides the deep link
// (§5.1) that a token establishes a session, and both accept a token only from
// the viewer's own origin.
func (s *Server) loginToken(w http.ResponseWriter, r *http.Request, token string) {
	ip := s.callerIP(r)
	if !sameOrigin(r) {
		// A cross-site page can present a token in an <img>, a <link>, or a
		// navigation, and SameSite=Strict does not stop the login *response*
		// from setting a session cookie, so the victim would be pinned into
		// the presenter's session. Reject before the throttle is consulted: a
		// cross-site flood must not spend a real caller's login budget. The
		// audit line follows §9 and never carries the token.
		slog.Warn("viewer login rejected", "path", r.URL.Path, "ip", ip,
			"session", sessionHandle(sessionID(r)), "result", "forbidden")
		w.WriteHeader(http.StatusForbidden) // empty body (§13)
		return
	}
	if !s.loginThrottle.allow(ip) {
		slog.Warn("viewer login throttled", "ip", ip)
		// A throttled caller is told how long to wait rather than left to guess
		// (api-design §5). The delay is the throttle's own remaining block, so
		// the header and the enforced wait cannot drift apart.
		w.Header().Set("Retry-After", strconv.Itoa(int(s.loginThrottle.retryAfter(ip)/time.Second)))
		w.WriteHeader(http.StatusTooManyRequests) // empty body
		return
	}
	role, ok := s.resolveToken(token)
	if !ok {
		// The attempt was already recorded by allow.
		slog.Warn("viewer login failed", "ip", ip) // token value never logged
		// The rejection is answered without a body: a response about an
		// authentication decision never carries prose a caller could read as
		// detail about the token, and a browser form POST is the same response
		// as any other. The login page states the reason for a human who comes
		// back to it (api-design §5, viewer-design §3).
		w.WriteHeader(http.StatusUnauthorized) // empty body
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

// loginPost submits the login form. The body is parsed under the viewer's
// bound before the token is read, so an oversized body is refused 413 and a
// non-form body — a multipart one, which ParseForm leaves unread — never
// reaches memory or a temp file (viewer-security §13). FormValue is safe after
// that parse: it re-parses nothing, and in particular never triggers the
// multipart parse it would otherwise run on its own.
func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if !parseFormBody(w, r) {
		return
	}
	s.loginToken(w, r, r.FormValue("token"))
}

// sameOrigin reports whether a credential-bearing login request provably comes
// from the viewer's own origin (viewer-security §5.1). The browser describes
// the relation in Sec-Fetch-Site, which a page cannot forge (it is a forbidden
// header name): only `same-origin` — a form post, link, or htmx request from a
// viewer page — and `none` — a top-level navigation with no initiator, such as
// the address bar, a bookmark, or the browser `cask web` opens — are accepted.
// `same-site` and `cross-site` are not the viewer's origin, so they are refused
// even though the token may be valid. When a browser sends no Sec-Fetch-Site,
// an Origin header naming the request's own host is the fallback. Two missing
// headers fail closed: only a browser attaches either, and a client that
// attaches neither is not the victim this check protects.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Compare hosts only: behind a TLS-terminating reverse proxy (§12) the
	// viewer sees plain HTTP while the browser's Origin names https, so the
	// scheme is not comparable here. The host is what separates the viewer's
	// own pages from an attacker's.
	return parsed.Host != "" && parsed.Host == r.Host
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
