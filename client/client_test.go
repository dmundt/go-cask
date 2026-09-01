package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// fakeServer is a minimal in-memory CAS API server implementing the route
// contract (cas-api §4–5) for client tests: bearer-token roles, streaming
// store/retrieve, meta/list/stats/verify/gc, 429 rate limiting.
type fakeServer struct {
	objects map[string][]byte // hash string → bytes
	tokens  map[string]string // token → role
	limited bool              // when true, every request → 429
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		objects: map[string][]byte{},
		tokens: map[string]string{
			"viewer-tok": "viewer",
			"op-tok":     "operator",
			"admin-tok":  "admin",
		},
	}
}

func (s *fakeServer) hash(data []byte) cas.Hash {
	sum := sha256.Sum256(data)
	h, _ := cas.NewHash("sha256", sum[:])
	return h
}

func (s *fakeServer) role(r *http.Request) string {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return s.tokens[tok]
}

func (s *fakeServer) require(r *http.Request, w http.ResponseWriter, roles ...string) bool {
	role := s.role(r)
	if role == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	for _, want := range roles {
		if role == want {
			return true
		}
	}
	writeErr(w, http.StatusForbidden, "forbidden")
	return false
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.limited {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("X-RateLimit-Limit", "2")
		w.Header().Set("X-RateLimit-Remaining", "0")
		writeErr(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/cas/v1")

	switch {
	case r.Method == http.MethodPost && rest == "/objects":
		if !s.require(r, w, "operator", "admin") {
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			writeErr(w, http.StatusBadRequest, "empty body")
			return
		}
		h := s.hash(body)
		_, existed := s.objects[h.String()]
		s.objects[h.String()] = body
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"hash": h.String(), "deduplicated": existed})

	case r.Method == http.MethodGet && rest == "/objects":
		if !s.require(r, w, "viewer", "operator", "admin") {
			return
		}
		var items []map[string]any
		for hs, b := range s.objects {
			items = append(items, map[string]any{
				"hash": hs, "algorithm": "sha256", "size": len(b),
			})
		}
		sort.Slice(items, func(i, j int) bool { return items[i]["hash"].(string) < items[j]["hash"].(string) })
		json.NewEncoder(w).Encode(map[string]any{"total": len(items), "objects": items})

	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/objects/") && strings.HasSuffix(rest, "/meta"):
		h := strings.TrimSuffix(strings.TrimPrefix(rest, "/objects/"), "/meta")
		if !s.require(r, w, "viewer", "operator", "admin") {
			return
		}
		b, ok := s.objects[h]
		if !ok {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"hash": h, "algorithm": "sha256", "size": len(b),
		})

	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/objects/"):
		h := strings.TrimPrefix(rest, "/objects/")
		if !s.require(r, w, "viewer", "operator", "admin") {
			return
		}
		b, ok := s.objects[h]
		if !ok {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		w.Header().Set("X-CAS-Algorithm", "sha256")
		w.Header().Set("X-CAS-Size", fmt.Sprint(len(b)))
		w.Write(b)

	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "/objects/"):
		if !s.require(r, w, "admin") {
			return
		}
		delete(s.objects, strings.TrimPrefix(rest, "/objects/"))
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodPost && strings.HasPrefix(rest, "/objects/") && strings.HasSuffix(rest, "/verify"):
		if !s.require(r, w, "operator", "admin") {
			return
		}
		h := strings.TrimSuffix(strings.TrimPrefix(rest, "/objects/"), "/verify")
		b, ok := s.objects[h]
		if !ok {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"hash": h, "valid": true, "recomputed": s.hash(b).String()})

	case r.Method == http.MethodPost && rest == "/gc":
		if !s.require(r, w, "admin") {
			return
		}
		var body struct {
			Reachable []string `json:"reachable"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		keep := map[string]bool{}
		for _, hs := range body.Reachable {
			keep[hs] = true
		}
		deleted := 0
		for hs := range s.objects {
			if !keep[hs] {
				delete(s.objects, hs)
				deleted++
			}
		}
		json.NewEncoder(w).Encode(map[string]int{"deleted": deleted})

	case r.Method == http.MethodGet && rest == "/stats":
		if !s.require(r, w, "viewer", "operator", "admin") {
			return
		}
		var total int64
		for _, b := range s.objects {
			total += int64(len(b))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"object_count":     len(s.objects),
			"total_size":       total,
			"algorithm_counts": map[string]int64{"sha256": int64(len(s.objects))},
		})

	case r.Method == http.MethodGet && rest == "/openapi.yaml":
		if !s.require(r, w, "viewer", "operator", "admin") {
			return
		}
		w.Write([]byte("openapi: 3.0.3\n"))

	default:
		writeErr(w, http.StatusNotFound, "no such route")
	}
}

func newTestClient(t *testing.T, s *fakeServer, token string) (*Client, string) {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return New(ts.URL, token), ts.URL
}

func TestPutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t, newFakeServer(), "op-tok")

	h1, dedup1, err := c.Put(ctx, strings.NewReader("hello world"), "")
	if err != nil {
		t.Fatal(err)
	}
	if dedup1 {
		t.Fatal("first put must not be deduplicated")
	}
	h2, dedup2, err := c.Put(ctx, strings.NewReader("hello world"), "")
	if err != nil {
		t.Fatal(err)
	}
	if h1.String() != h2.String() {
		t.Fatalf("identical bytes must hash identically: %s vs %s", h1, h2)
	}
	if !dedup2 {
		t.Fatal("second put must report deduplicated")
	}

	// Acceptance: client.Put → client.Get returns identical bytes.
	got, err := c.GetBytes(ctx, h1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("Get = %q, want %q", got, "hello world")
	}
}

func TestGetMetaHeaders(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t, newFakeServer(), "op-tok")
	h, _, err := c.Put(ctx, strings.NewReader("stream me"), "")
	if err != nil {
		t.Fatal(err)
	}
	rc, meta, err := c.Get(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if meta.Algorithm != "sha256" || meta.Size != 9 {
		t.Fatalf("meta = %+v", meta)
	}
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatal(err)
	}
}

func TestMetaListStats(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer()
	op, _ := newTestClient(t, s, "op-tok")
	h, _, err := op.Put(ctx, strings.NewReader("meta me"), "")
	if err != nil {
		t.Fatal(err)
	}
	// A viewer-role token can read meta/list/stats.
	viewer, _ := newTestClient(t, s, "viewer-tok")
	m, err := viewer.Meta(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hash.String() != h.String() || m.Size != 7 {
		t.Fatalf("meta = %+v", m)
	}
	list, err := viewer.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Objects) != 1 {
		t.Fatalf("list = %+v", list)
	}
	st, err := viewer.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectCount != 1 || st.TotalSize != 7 || st.AlgorithmCounts["sha256"] != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestDeleteVerifyGC(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer()
	c, _ := newTestClient(t, s, "admin-tok")

	h1, _, _ := c.Put(ctx, strings.NewReader("one"), "")
	h2, _, _ := c.Put(ctx, strings.NewReader("two"), "")

	if err := c.Delete(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBytes(ctx, h1); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}

	v, err := c.Verify(ctx, h2)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid || v.Hash.String() != h2.String() {
		t.Fatalf("verify = %+v", v)
	}

	n, err := c.GC(ctx, nil) // delete everything
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("gc deleted %d, want 1", n)
	}
}

func TestAuthErrors(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer()

	// No token → 401.
	anon, _ := newTestClient(t, s, "")
	if _, _, err := anon.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("anonymous put = %v, want ErrUnauthorized", err)
	}

	// Viewer token cannot store → 403.
	viewer, _ := newTestClient(t, s, "viewer-tok")
	if _, _, err := viewer.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer put = %v, want ErrForbidden", err)
	}

	// Operator token cannot delete → 403.
	op, _ := newTestClient(t, s, "op-tok")
	h, _, err := op.Put(ctx, strings.NewReader("x"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Delete(ctx, h); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator delete = %v, want ErrForbidden", err)
	}

	// Admin can delete → nil.
	admin, _ := newTestClient(t, s, "admin-tok")
	if err := admin.Delete(ctx, h); err != nil {
		t.Fatalf("admin delete = %v", err)
	}
}

func TestRateLimited(t *testing.T) {
	ctx := context.Background()
	s := newFakeServer()
	s.limited = true
	c, _ := newTestClient(t, s, "admin-tok")
	if _, _, err := c.Put(ctx, strings.NewReader("x"), ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate-limited put = %v, want ErrRateLimited", err)
	}
}

func TestOpenAPI(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t, newFakeServer(), "viewer-tok")
	doc, err := c.OpenAPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(doc, []byte("openapi:")) {
		t.Fatalf("openapi doc = %q", doc)
	}
}
