package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/client"
)

func newTestServer(t *testing.T, rlCfg RateLimitConfig) (*client.Client, *httptest.Server) {
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
	return client.New(ts.URL, "op-tok"), ts
}

func TestRoundTripAndDedup(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())

	h1, dedup1, err := c.Put(ctx, strings.NewReader("hello server"), "")
	if err != nil {
		t.Fatal(err)
	}
	if dedup1 {
		t.Fatal("first put must not be deduplicated")
	}
	h2, dedup2, err := c.Put(ctx, strings.NewReader("hello server"), "")
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() != h2.String() || !dedup2 {
		t.Fatalf("dedup: %s vs %s, dedup=%v", h1, h2, dedup2)
	}
	got, err := c.GetBytes(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello server" {
		t.Fatalf("Get = %q", got)
	}
}

// R-04: large payloads stream without buffering (memory-bounded spool).
func TestLargePayload(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	payload := strings.Repeat("x", 4<<20) // 4 MiB
	h, _, err := c.Put(ctx, strings.NewReader(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetBytes(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) || got[0] != 'x' {
		t.Fatalf("large payload round-trip: got %d bytes", len(got))
	}
}

func TestRoleMatrix(t *testing.T) {
	ctx := context.Background()
	raw, _ := cas.NewFSRawStore(t.TempDir())
	srv := New(raw, map[string]string{"viewer-tok": "viewer", "op-tok": "operator", "admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// No token → 401.
	anon := client.New(ts.URL, "")
	if _, _, err := anon.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("anonymous put = %v", err)
	}
	// Viewer cannot store → 403.
	viewer := client.New(ts.URL, "viewer-tok")
	if _, _, err := viewer.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, client.ErrForbidden) {
		t.Fatalf("viewer put = %v", err)
	}
	// Operator stores; operator cannot delete → 403; admin can → nil.
	op := client.New(ts.URL, "op-tok")
	h, _, err := op.Put(ctx, strings.NewReader("x"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Delete(ctx, h); !errors.Is(err, client.ErrForbidden) {
		t.Fatalf("operator delete = %v", err)
	}
	admin := client.New(ts.URL, "admin-tok")
	if err := admin.Delete(ctx, h); err != nil {
		t.Fatalf("admin delete = %v", err)
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
		if _, _, err := c.Put(ctx, strings.NewReader("x"), ""); err != nil {
			if errors.Is(err, client.ErrRateLimited) {
				limited++
				continue
			}
			t.Fatalf("put %d: %v", i, err)
		}
		ok++
	}
	if ok != 2 || limited == 0 {
		t.Fatalf("ok=%d limited=%d, want ok=2 limited>0", ok, limited)
	}
}

func TestMetaVerifyListStats(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	h, _, err := c.Put(ctx, strings.NewReader("meta me"), "")
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Meta(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hash.String() != h.String() || m.Size != 7 {
		t.Fatalf("meta = %+v", m)
	}
	v, err := c.Verify(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid || v.Recomputed != h.String() {
		t.Fatalf("verify = %+v", v)
	}
	list, err := c.List(ctx, client.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Objects) != 1 || list.Objects[0].Size != 7 {
		t.Fatalf("list = %+v", list)
	}
	st, err := c.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 1 || st.TotalSize != 7 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestGCAndOpenAPI(t *testing.T) {
	ctx := context.Background()
	raw, _ := cas.NewFSRawStore(t.TempDir())
	srv := New(raw, map[string]string{"admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := client.New(ts.URL, "admin-tok")

	h1, _, _ := admin.Put(ctx, strings.NewReader("one"), "")
	h2, _, _ := admin.Put(ctx, strings.NewReader("two"), "")

	// GC keeping only h1 → deletes h2.
	n, err := admin.GC(ctx, []cas.Hash{h1})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("gc deleted %d, want 1", n)
	}
	if _, err := admin.GetBytes(ctx, h1); err != nil {
		t.Fatalf("kept object missing: %v", err)
	}
	if _, err := admin.GetBytes(ctx, h2); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("gc'd object still present: %v", err)
	}

	doc, err := admin.OpenAPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "openapi: 3.0.3") {
		t.Fatalf("openapi doc = %.60q", doc)
	}
}

func TestMalformedHash(t *testing.T) {
	ctx := context.Background()
	_, ts := newTestServer(t, DefaultRateLimit())
	// A malformed hash in the path is rejected with 400 (the typed client
	// never sends one — ParseHash rejects it first).
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/cas/v1/objects/not-a-hash", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok") // pass auth; the hash is the failure
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed hash status = %d, want 400", resp.StatusCode)
	}
}
