// The viewer's request-body bound and the form parse that inherits it.
//
// The viewer is the product's only surface that accepts untrusted network
// input, and its POST routes carry a login token, a CSRF token and a digest —
// a few KB at most. Nothing bounded the request body before, so an
// unauthenticated caller decided how much memory each in-flight request held,
// and a multipart body decided how much of it spilled to temp files
// (viewer-security §13).

package web

import (
	"errors"
	"log/slog"
	"net/http"
)

// maxRequestBodyBytes bounds the body of every viewer request, on every route
// (viewer-security §13). The bound is generous for what the viewer accepts and
// small enough that a concurrent flood costs memory in kilobytes rather than in
// tens of megabytes per request.
const maxRequestBodyBytes = 4 << 10 // 4 KiB

// bodyTooLargeMessage is the viewer's own prose for a refused body. It names
// the request, never the store or the caller (viewer-design §3).
const bodyTooLargeMessage = "request body too large"

// boundBody caps the request body every viewer route accepts. It sits outside
// the routes, so a route added later inherits the bound instead of re-deciding
// it.
//
// A declared length over the bound is refused with 413 before anything reads a
// byte, so a body that announces itself as oversized — a URL-encoded form or a
// multipart one — never reaches a parser, let alone ParseMultipartForm's
// in-memory buffer and temp files. A chunked body declares no length;
// MaxBytesReader bounds that one as the parse reads it, and flags the
// connection so the server closes it rather than let a caller keep sending into
// a body the viewer has stopped reading.
func boundBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequestBodyBytes {
			slog.Warn("viewer request body refused",
				"path", r.URL.Path, "content-length", r.ContentLength, "limit", maxRequestBodyBytes)
			http.Error(w, bodyTooLargeMessage, http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// parseFormBody parses a POST body as a URL-encoded form under the bound
// boundBody installed. It answers the caller itself — 413 for a body that
// passes the bound while being read, 400 for one it cannot parse — and reports
// whether the handler may continue.
//
// It calls ParseForm, never ParseMultipartForm: a body that is not an
// `application/x-www-form-urlencoded` form is left unread, so a multipart body
// is refused here rather than spooled into memory and temp files
// (viewer-security §13). The bound's answer and the parser's are the same
// error, so no route has to restate the limit.
func parseFormBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			slog.Warn("viewer request body over the bound",
				"path", r.URL.Path, "limit", maxRequestBodyBytes)
			http.Error(w, bodyTooLargeMessage, http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "malformed form body", http.StatusBadRequest)
		return false
	}
	return true
}
