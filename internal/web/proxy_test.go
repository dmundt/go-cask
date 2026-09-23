// Tests for the viewer's proxy-aware caller identity (viewer-security §5.2):
// the trust boundary that decides whether a forwarded client address may move
// a caller's login-throttle bucket.

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

func TestNewTrustedProxy(t *testing.T) {
	trusted, err := newTrustedProxy([]string{" 10.0.0.0/8 ", "127.0.0.1", "::1", ""})
	if err != nil {
		t.Fatalf("newTrustedProxy: %v", err)
	}
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"10.1.2.3", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"192.0.2.1", false},
		{"127.0.0.1.5", false},
		{"", false},
		{" 10.1.2.3", false}, // an untrimmed peer address is never trusted
	} {
		if got := trusted.isTrusted(tc.addr); got != tc.want {
			t.Errorf("isTrusted(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}

	// The zero value trusts nothing, which is the default configuration.
	var none *trustedProxy
	if none.isTrusted("127.0.0.1") {
		t.Error("a nil trustedProxy must trust nothing")
	}
	empty, err := newTrustedProxy(nil)
	if err != nil {
		t.Fatalf("newTrustedProxy(nil): %v", err)
	}
	if empty.isTrusted("127.0.0.1") {
		t.Error("an empty trustedProxy must trust nothing")
	}

	// A mistyped entry is refused rather than ignored: silently trusting
	// nothing would restore the shared-bucket lockout the setting exists to
	// prevent.
	for _, entry := range []string{"10.0.0.0/33", "not-an-address", "10.0.0.1/24/8"} {
		if _, err := newTrustedProxy([]string{entry}); err == nil {
			t.Errorf("newTrustedProxy(%q) error = nil, want a parse error", entry)
		}
	}
}

func TestCallerIP(t *testing.T) {
	trusted := func(t *testing.T, proxies ...string) *Server {
		t.Helper()
		parsed, err := newTrustedProxy(proxies)
		if err != nil {
			t.Fatalf("newTrustedProxy(%v): %v", proxies, err)
		}
		return &Server{trusted: parsed}
	}

	for _, tc := range []struct {
		name    string
		proxies []string
		peer    string
		headers map[string]string
		want    string
	}{
		{
			name: "untrusted peer with a spoofed X-Forwarded-For is ignored",
			peer: "198.51.100.7:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.9",
			},
			want: "198.51.100.7",
		},
		{
			name: "untrusted peer with a spoofed Forwarded is ignored",
			peer: "198.51.100.7:41234",
			headers: map[string]string{
				"Forwarded": `for="203.0.113.9"`,
			},
			want: "198.51.100.7",
		},
		{
			name:    "trusted peer forwards one client",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.9",
			},
			want: "203.0.113.9",
		},
		{
			name:    "trusted peer forwards two clients",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.9, 198.51.100.4",
			},
			want: "198.51.100.4",
		},
		{
			name:    "a client-supplied prefix never displaces the hop the proxy appended",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.99, 198.51.100.4",
			},
			want: "198.51.100.4",
		},
		{
			name:    "an empty hop is skipped",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.9, ",
			},
			want: "203.0.113.9",
		},
		{
			name:    "a trusted peer with no forwarded address speaks for itself",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			want:    "10.0.0.5",
		},
		{
			name: "a direct caller with no forwarded address is its own address",
			peer: "198.51.100.7:41234",
			want: "198.51.100.7",
		},
		{
			name:    "a malformed hop keys its own bucket instead of the peer's",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "unknown",
			},
			want: "unknown",
		},
		{
			name:    "the Forwarded header supplies the address when X-Forwarded-For is absent",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"Forwarded": `for=192.0.2.60;proto=http;by=203.0.113.43, for="198.51.100.17"`,
			},
			want: "192.0.2.60",
		},
		{
			name:    "a bracketed Forwarded node loses its brackets and port",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"Forwarded": `for="[2001:db8::7]:4711"`,
			},
			want: "2001:db8::7",
		},
		{
			name:    "a forwarded port is not normalized into another client's address",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.9:4711",
			},
			want: "203.0.113.9:4711",
		},
		{
			name:    "a chain of only trusted hops keys the leftmost",
			proxies: []string{"10.0.0.0/8"},
			peer:    "10.0.0.5:41234",
			headers: map[string]string{
				"X-Forwarded-For": "10.1.2.3, 10.0.0.6",
			},
			want: "10.1.2.3",
		},
		{
			name:    "a host:port trusted entry matches the peer",
			proxies: []string{"127.0.0.1:8080"},
			peer:    "127.0.0.1:41234",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.9",
			},
			want: "203.0.113.9",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := trusted(t, tc.proxies...)
			r := httptest.NewRequest(http.MethodPost, "/viewer/login", nil)
			r.RemoteAddr = tc.peer
			for name, value := range tc.headers {
				r.Header.Set(name, value)
			}
			if got := srv.callerIP(r); got != tc.want {
				t.Fatalf("callerIP(peer %q, headers %v) = %q, want %q", tc.peer, tc.headers, got, tc.want)
			}
		})
	}
}

// testPeerHeader carries the synthetic peer address a caller-identity test
// wants the viewer to see. The HTTP server always overwrites RemoteAddr with
// the real TCP peer, so the test handler below substitutes one from this header
// (and closes the connection, so no later request reuses it).
const testPeerHeader = "X-Test-Peer"

// proxyTestServer builds a viewer over a temporary store with the given
// trusted proxies configured and a two-attempt login budget, so a test spends
// few requests to exhaust one bucket. It returns the running server and the
// viewer it serves.
func proxyTestServer(t *testing.T, proxies ...string) (*httptest.Server, *Server) {
	t.Helper()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(backend, Config{StartupToken: testStartupToken, TrustedProxies: proxies})
	if err != nil {
		t.Fatal(err)
	}
	srv.loginThrottle = newThrottle(2, time.Minute)
	ts := httptest.NewTLSServer(withPeer(srv.Handler()))
	t.Cleanup(ts.Close)
	return ts, srv
}

// withPeer rewrites each request's RemoteAddr from testPeerHeader so a test can
// drive caller identity — the trust boundary this file pins — without a real
// network topology. Removing the header first keeps the synthetic peer from
// reaching handler code that might otherwise treat it as input.
func withPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if peer := r.Header.Get(testPeerHeader); peer != "" {
			r.Header.Del(testPeerHeader)
			r.RemoteAddr = peer
		}
		next.ServeHTTP(w, r)
	})
}

// postLogin sends one login attempt as if it arrived from peer, carrying the
// given request headers, and returns the response status and Location.
func postLogin(t *testing.T, ts *httptest.Server, peer string, headers map[string]string) (int, string) {
	t.Helper()
	return postToken(t, ts, peer, "wrong", headers)
}

// postToken sends one login attempt with the submitted token as if it arrived
// from peer. A valid token answers 303 when the caller's bucket is open and
// 429 when it is not, so it proves which bucket the request was keyed on. The
// submitted value never reaches the log (viewer-security §9).
func postToken(t *testing.T, ts *httptest.Server, peer, token string, headers map[string]string) (int, string) {
	t.Helper()
	form := url.Values{"token": {token}}.Encode()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/viewer/login", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if peer != "" {
		req.Header.Set(testPeerHeader, peer)
	}
	// A fresh connection per request keeps a reused keep-alive connection from
	// carrying the previous attempt's address into this one.
	req.Close = true
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

// openBucket asserts that peer is not throttled: a valid login from it is
// answered as a success — the login redirect, followed to the landing it names
// — rather than 429.
func openBucket(t *testing.T, ts *httptest.Server, peer string, headers map[string]string) {
	t.Helper()
	switch status, _ := postToken(t, ts, peer, testStartupToken, headers); status {
	case http.StatusSeeOther, http.StatusOK:
	default:
		t.Fatalf("valid login from %s = %d, want 303 (or the landing it redirects to): the bucket is blocked", peer, status)
	}
}

// exhaust spends peer's whole budget, asserting that every attempt is still
// answered on its merits (401), and returns the status of the first attempt
// past the budget, which must be 429.
func exhaust(t *testing.T, ts *httptest.Server, peer string, headers map[string]string) int {
	t.Helper()
	for i := range 2 {
		if status, _ := postLogin(t, ts, peer, headers); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d from %s = %d, want 401 (within budget)", i+1, peer, status)
		}
	}
	status, _ := postLogin(t, ts, peer, headers)
	return status
}

// TestThrottleIgnoresSpoofedForwardedAddressFromUntrustedPeer pins the trust
// boundary the issue names: with no trusted proxy configured, a caller's own
// X-Forwarded-For must neither move its own throttle bucket nor push another
// caller into a block. If the header were believed, the fresh value used in
// the last attempt would get its own budget and answer 401.
func TestThrottleIgnoresSpoofedForwardedAddressFromUntrustedPeer(t *testing.T) {
	ts, _ := proxyTestServer(t)
	forged := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	if status := exhaust(t, ts, "198.51.100.7:41234", forged); status != http.StatusTooManyRequests {
		t.Fatalf("attempt past the budget from an untrusted peer = %d, want 429", status)
	}

	// The same peer with a different forged address is still the same bucket.
	other := map[string]string{"X-Forwarded-For": "203.0.113.10"}
	if status, _ := postLogin(t, ts, "198.51.100.7:41234", other); status != http.StatusTooManyRequests {
		t.Fatalf("a changed X-Forwarded-For moved an untrusted peer's bucket: got %d, want 429", status)
	}

	// And a forged value cannot lock out the client it names: a second peer is
	// untouched by the first peer's exhaustion.
	fresh := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	openBucket(t, ts, "198.51.100.8:41234", fresh)
}

// TestThrottleHonoursForwardedAddressFromTrustedPeer pins the other half: a
// configured trusted proxy's forwarded client address is what the throttle
// keys on, so one client's failures leave another's budget alone — the
// reverse-proxy lockout the issue reports.
func TestThrottleHonoursForwardedAddressFromTrustedPeer(t *testing.T) {
	ts, _ := proxyTestServer(t, "10.0.0.0/8")
	alice := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	if status := exhaust(t, ts, "10.0.0.5:41234", alice); status != http.StatusTooManyRequests {
		t.Fatalf("attempt past the budget for the forwarded client = %d, want 429", status)
	}

	// A different forwarded client through the same proxy keeps its budget.
	bob := map[string]string{"X-Forwarded-For": "203.0.113.10"}
	openBucket(t, ts, "10.0.0.5:41234", bob)

	// An untrusted peer cannot borrow the trusted proxy's identity, and cannot
	// become trusted by claiming to be the proxy.
	outsider := map[string]string{"X-Forwarded-For": "203.0.113.11"}
	if status := exhaust(t, ts, "198.51.100.7:41234", outsider); status != http.StatusTooManyRequests {
		t.Fatalf("an untrusted peer claiming a forwarded address = %d, want 429", status)
	}
}

// TestThrottleWithoutForwardedAddress pins the no-header cases: with the
// header absent, a trusted proxy and a direct caller both key on their own
// peer address, which is what the documented default does.
func TestThrottleWithoutForwardedAddress(t *testing.T) {
	t.Run("trusted proxy without a forwarded address", func(t *testing.T) {
		ts, _ := proxyTestServer(t, "10.0.0.0/8")
		if status := exhaust(t, ts, "10.0.0.5:41234", nil); status != http.StatusTooManyRequests {
			t.Fatalf("attempt past the budget from the proxied peer = %d, want 429", status)
		}
		// The proxy's own exhausted bucket is what blocks; another peer is
		// unaffected, so no forwarded address means no shared bucket.
		openBucket(t, ts, "10.0.0.6:41234", nil)
	})

	t.Run("direct caller without a forwarded address", func(t *testing.T) {
		ts, _ := proxyTestServer(t)
		if status := exhaust(t, ts, "198.51.100.7:41234", nil); status != http.StatusTooManyRequests {
			t.Fatalf("attempt past the budget from the direct peer = %d, want 429", status)
		}
		openBucket(t, ts, "198.51.100.8:41234", nil)
	})

	t.Run("an empty header value is no forwarded address", func(t *testing.T) {
		ts, _ := proxyTestServer(t, "10.0.0.0/8")
		openBucket(t, ts, "10.0.0.5:41234", map[string]string{"X-Forwarded-For": ""})
	})
}

// TestNewRejectsMalformedTrustedProxy pins the fail-closed startup: a mistyped
// -trusted-proxy is refused instead of leaving the viewed configuration
// silently trusting nothing.
func TestNewRejectsMalformedTrustedProxy(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(backend, Config{TrustedProxies: []string{"10.0.0.0/33"}}); err == nil {
		t.Fatal("New accepted a malformed trusted proxy, want an error")
	}
}
