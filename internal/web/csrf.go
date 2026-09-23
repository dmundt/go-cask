package web

import (
	"crypto/subtle"
	"net/http"
)

// csrfField is the form field/header name carrying the per-session CSRF
// token on every mutation (POST).
const csrfField = "csrf"

// csrfOK reports whether a POST request carries the session's CSRF token
// (constant-time compare). GET requests and the login POST (no session
// exists yet) are exempt.
//
// The token is read from the request body or the X-CSRF-Token header only,
// never from the query string: a URL-borne token is captured by access logs,
// bookmarks, proxies, and Referer chains. PostFormValue ignores the query, so
// `?_csrf=<token>` never validates (viewer-security §5).
func csrfOK(r *http.Request, sess *Session) bool {
	if r.Method != http.MethodPost {
		return true
	}
	if sess == nil {
		return true // login POST: no session yet; login has its own throttling
	}
	given := r.PostFormValue(csrfField)
	if given == "" {
		given = r.Header.Get("X-CSRF-Token")
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(sess.CSRF)) == 1
}
