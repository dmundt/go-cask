package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fs "github.com/dmundt/go-cask/cas/backend/fs"
)

// This file pins the two hardening bounds of the example surface and the
// trusted-proxy wiring the README documents (api-design §7/§8/§9): an oversized
// body is refused before it is read (413, nothing stored, no spool left
// behind), the server sets every connection lifetime, -tokens is required, and
// -trusted-proxy makes callerIP's forwarded branch reachable.

// serverUnderTest starts the example handler over a fresh store, optionally
// trusting the test server's own address as a proxy.
func serverUnderTest(t *testing.T, maxObjectBytes int64, cfg RateLimitConfig, myTokens map[string]string, trustProxy bool) (*testClient, *fs.Backend) {
	t.Helper()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, myTokens, cfg, maxObjectBytes)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if trustProxy {
		// The httptest client dials this server, so its own address is the peer
		// callerIP sees: trusting it makes the forwarded branch reachable.
		host, _, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		srv.WithTrustedProxies(host)
	}
	return &testClient{base: ts.URL, token: "op-tok", hc: ts.Client()}, backend
}

// A body one byte past the bound is refused with 413 before it reaches the
// store: nothing is stored and the upload spool is removed.
func TestPostObjectRejectsOversizedBody(t *testing.T) {
	ctx := context.Background()
	const limit = 1024
	// A small bound keeps the test's body tiny while exercising the same path.
	c, backend := serverUnderTest(t, limit, DefaultRateLimit(), map[string]string{"op-tok": "operator"}, false)
	before := caskUploadSpools(t)

	status, b := c.do(ctx, http.MethodPost, "/api/cas/v1/objects", strings.NewReader(strings.Repeat("x", limit+1)))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized put = %d body=%s, want 413", status, b)
	}
	if !strings.Contains(string(b), "maximum body size") {
		t.Fatalf("oversized put body = %s, want it to name the bound", b)
	}
	if st, err := backend.Stats(ctx); err != nil || st.ObjectCount != 0 {
		t.Fatalf("store after rejected put = (%+v, %v), want empty", st, err)
	}
	if left := caskUploadSpools(t); len(left) != len(before) {
		t.Fatalf("leftover upload spools: %v (had %v)", left, before)
	}
}

// A body exactly at the bound is accepted: the bound is inclusive, so a
// maximum-size object is a legitimate upload.
func TestPostObjectAcceptsBodyAtTheBound(t *testing.T) {
	ctx := context.Background()
	const limit = 1024
	c, _ := serverUnderTest(t, limit, DefaultRateLimit(), map[string]string{"op-tok": "operator"}, false)

	status, _, _ := c.put(ctx, strings.Repeat("x", limit))
	if status != http.StatusCreated {
		t.Fatalf("put at the bound = %d, want 201", status)
	}
}

// An oversized GC body is 413 and deletes nothing: the reachable set never
// decodes, so it cannot sweep the store.
func TestGCRejectsOversizedBody(t *testing.T) {
	ctx := context.Background()
	c, backend := serverUnderTest(t, 0, DefaultRateLimit(), map[string]string{"admin-tok": "admin"}, false)
	admin := &testClient{base: c.base, token: "admin-tok", hc: c.hc}

	if status, _, _ := admin.put(ctx, "keep me"); status != http.StatusCreated {
		t.Fatalf("put status = %d", status)
	}
	// A body over gcMaxBodyBytes: a long but entirely valid reachable list, so
	// only the bound can produce the 413.
	var oversized strings.Builder
	oversized.WriteString(`{"reachable":[`)
	entry := `"` + strings.Repeat("a", 64) + `"`
	for i := int64(0); i < gcMaxBodyBytes/64+64; i++ {
		if i > 0 {
			oversized.WriteByte(',')
		}
		oversized.WriteString(entry)
	}
	oversized.WriteString(`]}`)
	status, body := admin.do(ctx, http.MethodPost, "/api/cas/v1/gc", strings.NewReader(oversized.String()))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized gc = %d body=%.60s, want 413", status, body)
	}
	if st, err := backend.Stats(ctx); err != nil || st.ObjectCount != 1 {
		t.Fatalf("oversized gc changed the store: (%+v, %v)", st, err)
	}
}

// A body inside the GC bound still decodes normally: the bound does not break
// the documented reachable-set path.
func TestGCAcceptsBodyInsideTheBound(t *testing.T) {
	ctx := context.Background()
	c, _ := serverUnderTest(t, 0, DefaultRateLimit(), map[string]string{"admin-tok": "admin"}, false)
	admin := &testClient{base: c.base, token: "admin-tok", hc: c.hc}

	if status, _, _ := admin.put(ctx, "keep me"); status != http.StatusCreated {
		t.Fatalf("put status = %d", status)
	}
	status, g := admin.gc(ctx, nil)
	if status != http.StatusOK || g["deleted"] != float64(1) {
		t.Fatalf("gc = (%d, %v), want 200 with deleted 1", status, g)
	}
}

// The example server sets every connection lifetime: without them a client may
// trickle a body forever and a slow reader pins a goroutine and an open object
// file (api-design §9).
func TestNewHTTPServerSetsConnectionTimeouts(t *testing.T) {
	ts := newHTTPServer("127.0.0.1:0", http.NewServeMux())

	if ts.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout is unset")
	}
	if ts.ReadTimeout <= 0 {
		t.Error("ReadTimeout is unset: a client may trickle a body forever")
	}
	if ts.WriteTimeout <= 0 {
		t.Error("WriteTimeout is unset: a slow reader may pin a streamed download")
	}
	if ts.IdleTimeout <= 0 {
		t.Error("IdleTimeout is unset: idle keep-alive connections are never reclaimed")
	}
	if ts.Addr != "127.0.0.1:0" || ts.Handler == nil {
		t.Fatalf("newHTTPServer dropped addr/handler: %q, %v", ts.Addr, ts.Handler)
	}
}

// -tokens is required: the example ships no default credential, so an absent,
// empty or malformed value is refused with a message naming the flag.
func TestValidateTokensRequiresExplicitTokens(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		_, err := validateTokens("")
		if err == nil || !strings.Contains(err.Error(), "-tokens") {
			t.Fatalf("validateTokens(\"\") = %v, want a refusal naming -tokens", err)
		}
	})
	t.Run("blank pairs", func(t *testing.T) {
		if _, err := validateTokens(" , "); err == nil {
			t.Fatal("validateTokens accepted a blank token list")
		}
	})
	t.Run("malformed pair", func(t *testing.T) {
		if _, err := validateTokens("viewer=v1,operator"); err == nil {
			t.Fatal("validateTokens accepted a pair without =token")
		}
	})
	t.Run("empty token value", func(t *testing.T) {
		if _, err := validateTokens("admin="); err == nil {
			t.Fatal("validateTokens accepted an empty token")
		}
	})
	t.Run("explicit tokens", func(t *testing.T) {
		got, err := validateTokens(" viewer=v1 , operator=o1,admin=a1 ")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"v1": "viewer", "o1": "operator", "a1": "admin"}
		if len(got) != len(want) {
			t.Fatalf("validateTokens = %v, want %v", got, want)
		}
		for tok, role := range want {
			if got[tok] != role {
				t.Fatalf("validateTokens[%q] = %q, want %q", tok, got[tok], role)
			}
		}
	})
}

// -trusted-proxy seeds callerIP's forwarded branch, so a caller behind a
// configured proxy gets its own rate-limit bucket. Untrusted, the proxy's own
// address is the bucket and the header is ignored.
func TestTrustedProxyEnablesPerCallerRateLimiting(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultRateLimit()
	cfg.ExemptLoopback = false // the test client dials loopback
	cfg.Burst = 1

	putAs := func(t *testing.T, c *testClient, xff string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/cas/v1/objects", strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("X-Forwarded-For", xff)
		resp, err := c.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	t.Run("trusted proxy separates callers", func(t *testing.T) {
		c, _ := serverUnderTest(t, 0, cfg, map[string]string{"op-tok": "operator"}, true)
		if status := putAs(t, c, "198.51.100.9"); status != http.StatusCreated {
			t.Fatalf("first caller = %d, want 201", status)
		}
		if status := putAs(t, c, "198.51.100.9"); status != http.StatusTooManyRequests {
			t.Fatalf("same caller twice = %d, want 429 (burst 1)", status)
		}
		if status := putAs(t, c, "198.51.100.10"); status != http.StatusCreated {
			t.Fatalf("second caller = %d, want 201 (its own bucket)", status)
		}
	})

	t.Run("untrusted proxy shares one bucket", func(t *testing.T) {
		c, _ := serverUnderTest(t, 0, cfg, map[string]string{"op-tok": "operator"}, false)
		if status := putAs(t, c, "198.51.100.9"); status != http.StatusCreated {
			t.Fatalf("first caller = %d, want 201", status)
		}
		if status := putAs(t, c, "198.51.100.10"); status != http.StatusTooManyRequests {
			t.Fatalf("second caller = %d, want 429 (the header is ignored)", status)
		}
	})
}

// -trusted-proxy is repeatable, so one server can sit behind several proxies.
func TestStringListCollectsEveryOccurrence(t *testing.T) {
	var l stringList
	for _, host := range []string{"10.0.0.1", "10.0.0.2"} {
		if err := l.Set(host); err != nil {
			t.Fatal(err)
		}
	}
	if len(l) != 2 || l[0] != "10.0.0.1" || l[1] != "10.0.0.2" {
		t.Fatalf("stringList = %v, want both hosts", l)
	}
	if got := l.String(); got != "10.0.0.1,10.0.0.2" {
		t.Fatalf("stringList.String() = %q", got)
	}
}

// The served OpenAPI document states the body bound the handlers enforce and
// the 413 they answer with (api-design §9/§13).
func TestOpenAPIDocumentsTheBodyBound(t *testing.T) {
	doc := string(openapiYAML)
	if !strings.Contains(doc, "413") {
		t.Fatal("openapi.yaml does not document the 413 response")
	}
	if !strings.Contains(doc, "maximum object body") {
		t.Fatal("openapi.yaml does not name the maximum object body size")
	}
}

// caskUploadSpools lists the upload spool files postObject creates, so a test
// can assert a refused upload left none behind.
func caskUploadSpools(t *testing.T) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(os.TempDir(), "cask-upload-*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}
