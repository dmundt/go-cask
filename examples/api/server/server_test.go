package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// A tiny plain-HTTP test client: the example ships no SDK, so the tests
// speak the server's wire contract directly (api-design §2/§5).

type testClient struct {
	base  string
	token string
	hc    *http.Client
}

func newTestServer(t *testing.T, rlCfg RateLimitConfig) (*testClient, *httptest.Server) {
	t.Helper()
	raw, err := cas.NewFSRawStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(raw, map[string]string{
		"viewer-tok": "viewer",
		"op-tok":     "operator",
		"admin-tok":  "admin",
	}, rlCfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testClient{base: ts.URL, token: "op-tok", hc: ts.Client()}, ts
}

// do sends a request and returns status + body.
func (c *testClient) do(ctx context.Context, method, path string, body io.Reader) (int, []byte) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		panic(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	return resp.StatusCode, b
}

func (c *testClient) put(ctx context.Context, body string, algo string) (int, cas.Hash, bool) {
	status, b := c.do(ctx, http.MethodPost, "/api/cas/v1/objects?algo="+url.QueryEscape(algo), strings.NewReader(body))
	var res struct {
		Hash         string `json:"hash"`
		Deduplicated bool   `json:"deduplicated"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return status, nil, false
	}
	h, err := cas.ParseHash(res.Hash)
	if err != nil {
		return status, nil, false
	}
	return status, h, res.Deduplicated
}

func (c *testClient) getBytes(ctx context.Context, h cas.Hash) (int, []byte) {
	return c.do(ctx, http.MethodGet, "/api/cas/v1/objects/"+h.String(), nil)
}

func (c *testClient) del(ctx context.Context, h cas.Hash) (int, []byte) {
	return c.do(ctx, http.MethodDelete, "/api/cas/v1/objects/"+h.String(), nil)
}

func (c *testClient) meta(ctx context.Context, h cas.Hash) (int, map[string]any) {
	status, b := c.do(ctx, http.MethodGet, "/api/cas/v1/objects/"+h.String()+"/meta", nil)
	var m map[string]any
	json.Unmarshal(b, &m)
	return status, m
}

func (c *testClient) verify(ctx context.Context, h cas.Hash) (int, map[string]any) {
	status, b := c.do(ctx, http.MethodPost, "/api/cas/v1/objects/"+h.String()+"/verify", nil)
	var v map[string]any
	json.Unmarshal(b, &v)
	return status, v
}

func (c *testClient) list(ctx context.Context) (int, map[string]any) {
	status, b := c.do(ctx, http.MethodGet, "/api/cas/v1/objects", nil)
	var l map[string]any
	json.Unmarshal(b, &l)
	return status, l
}

func (c *testClient) stats(ctx context.Context) (int, map[string]any) {
	status, b := c.do(ctx, http.MethodGet, "/api/cas/v1/stats", nil)
	var st map[string]any
	json.Unmarshal(b, &st)
	return status, st
}

func (c *testClient) gc(ctx context.Context, reachable []cas.Hash) (int, map[string]any) {
	hashes := make([]string, 0, len(reachable))
	for _, h := range reachable {
		hashes = append(hashes, h.String())
	}
	body, _ := json.Marshal(map[string]any{"reachable": hashes})
	status, b := c.do(ctx, http.MethodPost, "/api/cas/v1/gc", bytes.NewReader(body))
	var g map[string]any
	json.Unmarshal(b, &g)
	return status, g
}

func num(m map[string]any, key string) float64 { return m[key].(float64) }

func TestRoundTripAndDedup(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())

	status, h1, dedup1 := c.put(ctx, "hello server", "sha256")
	if status != http.StatusCreated || dedup1 {
		t.Fatalf("first put: status=%d dedup=%v", status, dedup1)
	}
	status, h2, dedup2 := c.put(ctx, "hello server", "sha256")
	if status != http.StatusCreated || h1.String() != h2.String() || !dedup2 {
		t.Fatalf("dedup: status=%d %s vs %s dedup=%v", status, h1, h2, dedup2)
	}
	status, got := c.getBytes(ctx, h1)
	if status != http.StatusOK || string(got) != "hello server" {
		t.Fatalf("get = (%d, %q)", status, got)
	}
}

// R-04: large payloads stream without buffering (memory-bounded spool).
func TestLargePayload(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	payload := strings.Repeat("x", 4<<20) // 4 MiB
	status, h, _ := c.put(ctx, payload, "sha256")
	if status != http.StatusCreated {
		t.Fatalf("put status = %d", status)
	}
	status, got := c.getBytes(ctx, h)
	if status != http.StatusOK || len(got) != len(payload) || got[0] != 'x' {
		t.Fatalf("large payload round-trip: status=%d got %d bytes", status, len(got))
	}
}

func TestRoleMatrix(t *testing.T) {
	ctx := context.Background()
	raw, _ := cas.NewFSRawStore(t.TempDir())
	srv := New(raw, map[string]string{"viewer-tok": "viewer", "op-tok": "operator", "admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// No token → 401.
	anon := &testClient{base: ts.URL, hc: ts.Client()}
	status, _ := anon.do(ctx, http.MethodPost, "/api/cas/v1/objects", strings.NewReader("x"))
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous put = %d, want 401", status)
	}
	// Viewer cannot store → 403.
	viewer := &testClient{base: ts.URL, token: "viewer-tok", hc: ts.Client()}
	status, _ = viewer.do(ctx, http.MethodPost, "/api/cas/v1/objects", strings.NewReader("x"))
	if status != http.StatusForbidden {
		t.Fatalf("viewer put = %d, want 403", status)
	}
	// Operator stores; operator cannot delete → 403; admin can → 204.
	op := &testClient{base: ts.URL, token: "op-tok", hc: ts.Client()}
	status, h, _ := op.put(ctx, "x", "sha256")
	if status != http.StatusCreated {
		t.Fatalf("operator put = %d", status)
	}
	if status, _ := op.del(ctx, h); status != http.StatusForbidden {
		t.Fatalf("operator delete = %d, want 403", status)
	}
	admin := &testClient{base: ts.URL, token: "admin-tok", hc: ts.Client()}
	if status, _ := admin.del(ctx, h); status != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", status)
	}
}

// R-14: exceeding the burst → 429 with Retry-After + X-RateLimit-* headers.
func TestRateLimit(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultRateLimit()
	cfg.ExemptLoopback = false // so the test client (loopback) is limited
	cfg.Burst = 2
	c, _ := newTestServer(t, cfg)

	ok := 0
	limited := 0
	for i := 0; i < 5; i++ {
		status, _, _ := c.put(ctx, "x", "sha256")
		switch status {
		case http.StatusCreated:
			ok++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("put %d: status %d", i, status)
		}
	}
	if ok != 2 || limited == 0 {
		t.Fatalf("ok=%d limited=%d, want ok=2 limited>0", ok, limited)
	}
}

func TestMetaVerifyListStats(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	status, h, _ := c.put(ctx, "meta me", "sha256")
	if status != http.StatusCreated {
		t.Fatalf("put status = %d", status)
	}
	status, m := c.meta(ctx, h)
	if status != http.StatusOK || m["hash"] != h.String() || num(m, "size") != 7 {
		t.Fatalf("meta = (%d, %v)", status, m)
	}
	status, v := c.verify(ctx, h)
	if status != http.StatusOK || v["valid"] != true || v["recomputed"] != h.String() {
		t.Fatalf("verify = (%d, %v)", status, v)
	}
	status, l := c.list(ctx)
	if status != http.StatusOK || num(l, "total") != 1 {
		t.Fatalf("list = (%d, %v)", status, l)
	}
	objs := l["objects"].([]any)
	first := objs[0].(map[string]any)
	if num(first, "size") != 7 {
		t.Fatalf("list object = %v", first)
	}
	status, st := c.stats(ctx)
	if status != http.StatusOK || num(st, "object_count") != 1 || num(st, "total_size") != 7 {
		t.Fatalf("stats = (%d, %v)", status, st)
	}
}

func TestGCAndOpenAPI(t *testing.T) {
	ctx := context.Background()
	raw, _ := cas.NewFSRawStore(t.TempDir())
	srv := New(raw, map[string]string{"admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := &testClient{base: ts.URL, token: "admin-tok", hc: ts.Client()}

	_, h1, _ := admin.put(ctx, "one", "sha256")
	_, h2, _ := admin.put(ctx, "two", "sha256")

	// GC keeping only h1 → deletes h2.
	status, g := admin.gc(ctx, []cas.Hash{h1})
	if status != http.StatusOK || num(g, "deleted") != 1 {
		t.Fatalf("gc = (%d, %v)", status, g)
	}
	if status, _ := admin.getBytes(ctx, h1); status != http.StatusOK {
		t.Fatal("kept object missing")
	}
	if status, _ := admin.getBytes(ctx, h2); status != http.StatusNotFound {
		t.Fatalf("gc'd object still present: %d", status)
	}

	status, doc := admin.do(ctx, http.MethodGet, "/api/cas/v1/openapi.yaml", nil)
	if status != http.StatusOK || !strings.Contains(string(doc), "openapi: 3.0.3") {
		t.Fatalf("openapi = (%d, %.60q)", status, doc)
	}
}

func TestMalformedHash(t *testing.T) {
	ctx := context.Background()
	_, ts := newTestServer(t, DefaultRateLimit())
	// A malformed hash in the path is rejected with 400.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/cas/v1/objects/not-a-hash", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok") // pass auth; the hash is the failure
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed hash status = %d, want 400", resp.StatusCode)
	}
}
