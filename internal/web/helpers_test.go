// Fixtures shared by the viewer's tests: a server wired to a temporary
// store, a logged-in client, and the small readers the assertions use.

package web

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

const testStartupToken = "AAAA-BBBB-CCCC"

type testReferenceIndex struct {
	inbound  map[string][]cas.Digest
	outbound map[string][]cas.Digest
}

func newTestReferenceIndex() *testReferenceIndex {
	return &testReferenceIndex{
		inbound:  make(map[string][]cas.Digest),
		outbound: make(map[string][]cas.Digest),
	}
}

func (i *testReferenceIndex) Record(source cas.Digest, targets []cas.Digest) {
	i.outbound[source.String()] = append([]cas.Digest(nil), targets...)
	for _, target := range targets {
		i.inbound[target.String()] = append(i.inbound[target.String()], source)
	}
}

func (i *testReferenceIndex) Inbound(target cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.inbound[target.String()]...)
}

func (i *testReferenceIndex) Outbound(source cas.Digest) []cas.Digest {
	return append([]cas.Digest(nil), i.outbound[source.String()]...)
}

type testReachabilityIndex map[string]bool

func (i testReachabilityIndex) IsReachable(digest cas.Digest) bool {
	return i[digest.String()]
}

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{
		StartupToken: testStartupToken,
		RoleTokens: map[string]string{
			"viewer-tok":   RoleViewer,
			"operator-tok": RoleOperator,
			"admin-tok":    RoleAdmin,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// postFormAsBrowser posts a form the way a page served by the viewer does: the
// request carries the origin headers a browser attaches, because the viewer
// accepts a token only from its own origin (viewer-security §5.1). Tests that
// exercise the login path itself must present them.
func postFormAsBrowser(t testing.TB, c *http.Client, target string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if u, err := url.Parse(target); err == nil {
		req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// login performs the startup-token login and returns an authed client.
func login(t testing.TB, ts *httptest.Server, token string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	// Do not follow the 303 to the object browser: the login response itself is
	// what carries the session cookie.
	c := &http.Client{Transport: ts.Client().Transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := postFormAsBrowser(t, c, ts.URL+"/viewer/login", url.Values{"token": {token}})
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", resp.StatusCode)
	}
	c.CheckRedirect = nil // follow redirects from here on
	return c
}

// sessionCount reports how many sessions the server holds, under the session
// store's lock so the race detector sees the read as synchronized.
func sessionCount(srv *Server) int {
	srv.sessions.mu.Lock()
	defer srv.sessions.mu.Unlock()
	return len(srv.sessions.byID)
}

func statusCode(t *testing.T, client *http.Client, target string) int {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func getBody(t testing.TB, client *http.Client, target string) string {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func mustParse(t *testing.T, s string) cas.Digest {
	t.Helper()
	h, err := sha256.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// tlvEnvelope builds a TLV envelope (cas-core §8 decision 1) for viewer tests.
func tlvEnvelope(typeName string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(1) // envelopeVersion
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(typeName)))
	buf.Write(lenBuf[:n])
	buf.WriteString(typeName)
	n = binary.PutUvarint(lenBuf[:], uint64(len(payload)))
	buf.Write(lenBuf[:n])
	buf.Write(payload)
	return buf.Bytes()
}

func csrfFromPage(page string) string {
	// <input type="hidden" name="csrf" value="...">
	_, after, ok := strings.Cut(page, `name="csrf" value="`)
	if !ok {
		return ""
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, `"`)
	if !ok0 {
		return ""
	}
	return before0
}
