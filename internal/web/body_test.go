// Tests for the viewer's request-body bound (viewer-security §13): every route
// refuses a body over the bound before parsing it, a streamed body is refused
// while it is read, and a multipart body is never parsed — so nothing is
// buffered in memory or spooled to a temp file.

package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// countingBody records how many bytes of a request body were read, so a test
// can prove a refused body was never parsed — and therefore never reached the
// multipart parser that would have spilled it to a temp file.
type countingBody struct {
	reader io.Reader
	read   int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countingBody) Close() error { return nil }

// TestLoginRefusesOversizedBody pins the bound on the login route, the viewer's
// only unauthenticated POST: a body over the bound is refused 413 whether it
// announces itself as a URL-encoded form or as multipart, and no session is
// minted either way.
func TestLoginRefusesOversizedBody(t *testing.T) {
	ts, srv := newTestServer(t)

	var multipartForm bytes.Buffer
	w := multipart.NewWriter(&multipartForm)
	if err := w.WriteField("token", strings.Repeat("a", maxRequestBodyBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"urlencoded", "application/x-www-form-urlencoded", []byte("token=" + strings.Repeat("a", maxRequestBodyBytes))},
		{"multipart", w.FormDataContentType(), multipartForm.Bytes()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/viewer/login", bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			// A browser form, so the refusal under test is the body bound and
			// not the same-origin rule.
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Origin", ts.URL)
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized %s login = %d, want 413", tc.name, resp.StatusCode)
			}
			if cookies := resp.Cookies(); len(cookies) != 0 {
				t.Fatalf("refused login set cookies: %v", cookies)
			}
			if got := sessionCount(srv); got != 0 {
				t.Fatalf("refused login created %d sessions, want 0", got)
			}
		})
	}
}

// TestOversizedBodyIsNeverRead covers the "without writing a temp file" half of
// the bound: a body whose declared length is over the bound is refused before a
// byte of it is read, so a multipart body cannot reach the parser that buffers
// it and spills the remainder to temp files. The refusal still carries the
// viewer's hardening headers, because the bound sits inside them.
func TestOversizedBodyIsNeverRead(t *testing.T) {
	ts, _ := newTestServer(t)
	body := &countingBody{reader: bytes.NewReader(bytes.Repeat([]byte("a"), maxRequestBodyBytes+1))}
	req := httptest.NewRequest(http.MethodPost, "/viewer/login", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=viewer")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.ContentLength = int64(maxRequestBodyBytes + 1)

	rec := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized declared body = %d, want 413", rec.Code)
	}
	if body.read != 0 {
		t.Fatalf("refused body was read (%d bytes); it must be refused before parsing so nothing is spooled", body.read)
	}
	if req.MultipartForm != nil {
		t.Fatal("refused multipart body was parsed")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("refused body Cache-Control = %q, want no-store", got)
	}
}

// TestMultipartLoginIsRefusedNotSpooled pins proposal 3 on a body the bound
// does not refuse: a multipart form is never parsed, so a valid token smuggled
// in one is not accepted — the viewer reads a URL-encoded form only — and the
// request's multipart state is left empty, which is what keeps the body out of
// memory and out of a temp file.
func TestMultipartLoginIsRefusedNotSpooled(t *testing.T) {
	ts, srv := newTestServer(t)

	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	if err := w.WriteField("token", testStartupToken); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/viewer/login", bytes.NewReader(form.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("multipart login = %d, want 401 (the form is never read)", rec.Code)
	}
	if req.MultipartForm != nil {
		t.Fatal("multipart body was parsed; it would have been spooled to temp files")
	}
	if got := sessionCount(srv); got != 0 {
		t.Fatalf("multipart login created %d sessions, want 0", got)
	}
}

// TestAuthenticatedRouteRefusesOversizedBody pins the bound on a mutation,
// where the CSRF token is read out of the body: a body over the bound answers
// 413 — not the 403 a truncated parse would produce if the missing token were
// mistaken for the reason — whether its length is declared up front or only
// discovered while it is read.
func TestAuthenticatedRouteRefusesOversizedBody(t *testing.T) {
	ts, srv := newTestServer(t)

	var multipartForm bytes.Buffer
	w := multipart.NewWriter(&multipartForm)
	if err := w.WriteField("csrf", strings.Repeat("c", maxRequestBodyBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		declared    bool
	}{
		{"urlencoded streamed", "application/x-www-form-urlencoded", "pad=" + strings.Repeat("p", maxRequestBodyBytes), false},
		{"multipart declared", w.FormDataContentType(), multipartForm.String(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, err := srv.sessions.create(RoleOperator)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/viewer/objects/verify", strings.NewReader(tc.body))
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.ID})
			req.Header.Set("Content-Type", tc.contentType)
			if !tc.declared {
				// A streamed body carries no declared length, so the bound is
				// enforced by the parse rather than by the length check.
				req.ContentLength = -1
			}

			rec := httptest.NewRecorder()
			ts.Config.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized mutation = %d, want 413", rec.Code)
			}
		})
	}
}

// TestMalformedFormBodyIsRejected pins the other half of the parse: a body the
// viewer cannot read as a form is answered 400, so a mutation never reports a
// malformed body as a missing CSRF token.
func TestMalformedFormBodyIsRejected(t *testing.T) {
	ts, srv := newTestServer(t)
	sess, err := srv.sessions.create(RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/viewer/objects/verify", strings.NewReader("csrf=%zz"))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.ID})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed form body = %d, want 400", rec.Code)
	}
}
