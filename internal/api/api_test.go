package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/client"
	"github.com/dmundt/go-cask/internal/auth"
	"github.com/dmundt/go-cask/internal/storage"
)

func newTestServer(t *testing.T, rlCfg auth.RateLimitConfig) (*client.Client, *httptest.Server) {
	t.Helper()
	st, err := storage.New(context.Background(), storage.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, map[string]string{
		"viewer-tok": auth.RoleViewer,
		"op-tok":     auth.RoleOperator,
		"admin-tok":  auth.RoleAdmin,
	}, rlCfg)
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return client.New(ts.URL, "op-tok"), ts
}

func TestRoundTripAndDedup(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, auth.DefaultRateLimit())

	h1, dedup1, err := c.Put(ctx, strings.NewReader("hello cask web"), "")
	if err != nil {
		t.Fatal(err)
	}
	if dedup1 {
		t.Fatal("first put must not be deduplicated")
	}
	h2, dedup2, err := c.Put(ctx, strings.NewReader("hello cask web"), "")
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
	if string(got) != "hello cask web" {
		t.Fatalf("Get = %q", got)
	}
}

func TestRoleMatrix(t *testing.T) {
	ctx := context.Background()
	st, _ := storage.New(ctx, storage.Config{Dir: t.TempDir()})
	srv := New(st, map[string]string{"viewer-tok": "viewer", "op-tok": "operator", "admin-tok": "admin"}, auth.DefaultRateLimit())
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	anon := client.New(ts.URL, "")
	if _, _, err := anon.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("anonymous put = %v", err)
	}
	viewer := client.New(ts.URL, "viewer-tok")
	if _, _, err := viewer.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, client.ErrForbidden) {
		t.Fatalf("viewer put = %v", err)
	}
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

func TestRateLimit(t *testing.T) {
	ctx := context.Background()
	cfg := auth.DefaultRateLimit()
	cfg.ExemptLoopback = false
	cfg.Burst = 2
	c, _ := newTestServer(t, cfg)

	ok, limited := 0, 0
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
	c, _ := newTestServer(t, auth.DefaultRateLimit())
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
	st, _ := storage.New(ctx, storage.Config{Dir: t.TempDir()})
	srv := New(st, map[string]string{"admin-tok": "admin"}, auth.DefaultRateLimit())
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := client.New(ts.URL, "admin-tok")

	h1, _, _ := admin.Put(ctx, strings.NewReader("one"), "")
	h2, _, _ := admin.Put(ctx, strings.NewReader("two"), "")

	n, err := admin.GC(ctx, []cas.Hash{h1})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("gc deleted %d, want 1", n)
	}
	if _, err := admin.GetBytes(ctx, h2); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("gc'd object still present: %v", err)
	}

	doc, err := admin.OpenAPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "openapi: 3.0.3") || !strings.Contains(string(doc), "/objects/{hash}") {
		t.Fatalf("openapi doc = %.80q", doc)
	}
}

func TestMalformedHash(t *testing.T) {
	ctx := context.Background()
	_, ts := newTestServer(t, auth.DefaultRateLimit())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/cas/v1/objects/not-a-hash", nil)
	req.Header.Set("Authorization", "Bearer viewer-tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed hash status = %d, want 400", resp.StatusCode)
	}
}

func TestUnknownAlgorithm(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, auth.DefaultRateLimit())
	if _, _, err := c.Put(ctx, strings.NewReader("x"), "nope"); err == nil {
		t.Fatal("unknown algorithm must be rejected")
	}
}
