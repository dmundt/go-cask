package web

import (
	"crypto/subtle"
	"net/http"
)

// csrfToken is the form field/header name carrying the per-session CSRF
// token on every mutation (POST).
const csrfField = "csrf"

// csrfOK reports whether a POST request carries the session's CSRF token
// (constant-time compare). GET requests and the login POST (no session
// exists yet) are exempt.
func csrfOK(r *http.Request, sess *Session) bool {
	if r.Method != http.MethodPost {
		return true
	}
	if sess == nil {
		return true // login POST: no session yet; login has its own throttling
	}
	given := r.FormValue(csrfField)
	if given == "" {
		given = r.Header.Get("X-CSRF-Token")
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(sess.CSRF)) == 1
}
