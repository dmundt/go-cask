package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

func mustHash(t *testing.T, s string) cas.Hash {
	t.Helper()
	h, err := cas.ParseHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Every method must surface an error when the server fails (500).
func TestClientErrorPaths(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusInternalServerError, "boom")
	}))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "op-tok")
	ctx := context.Background()
	h := mustHash(t, "sha256:"+strings.Repeat("ab", 32))

	if _, _, err := c.Put(ctx, strings.NewReader("x"), ""); err == nil {
		t.Error("Put must error on 500")
	}
	if _, _, err := c.Get(ctx, h); err == nil {
		t.Error("Get must error on 500")
	}
	if err := c.Delete(ctx, h); err == nil {
		t.Error("Delete must error on 500")
	}
	if _, err := c.Meta(ctx, h); err == nil {
		t.Error("Meta must error on 500")
	}
	if _, err := c.List(ctx, ListOptions{}); err == nil {
		t.Error("List must error on 500")
	}
	if _, err := c.Verify(ctx, h); err == nil {
		t.Error("Verify must error on 500")
	}
	if _, err := c.Stats(ctx); err == nil {
		t.Error("Stats must error on 500")
	}
	if _, err := c.GC(ctx, []cas.Hash{h}); err == nil {
		t.Error("GC must error on 500")
	}
	if _, err := c.OpenAPI(ctx); err == nil {
		t.Error("OpenAPI must error on 500")
	}
}

// List must send the algo/limit/offset query parameters.
func TestListQueryParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("algo") != "sha256" || q.Get("limit") != "5" || q.Get("offset") != "10" {
			writeErr(w, http.StatusInternalServerError, "bad params")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"total": 0, "objects": []any{}})
	}))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "op-tok")
	if _, err := c.List(context.Background(), ListOptions{Algo: "sha256", Limit: 5, Offset: 10}); err != nil {
		t.Fatalf("List = %v", err)
	}
}

// A malformed hash in a JSON response must fail decoding.
func TestResponseDecodeErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/meta") {
			json.NewEncoder(w).Encode(map[string]any{"hash": "nope:zz", "size": 1})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/verify") {
			json.NewEncoder(w).Encode(map[string]any{"hash": "nope:zz", "valid": true})
			return
		}
		writeErr(w, http.StatusNotFound, "no such route")
	}))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "op-tok")
	h := mustHash(t, "sha256:"+strings.Repeat("ab", 32))
	if _, err := c.Meta(context.Background(), h); err == nil {
		t.Error("Meta with bad hash in response must error")
	}
	if _, err := c.Verify(context.Background(), h); err == nil {
		t.Error("Verify with bad hash in response must error")
	}
}

// A transport failure (unreachable server) must surface as an error.
func TestTransportError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close() // now unreachable
	c := New(url, "op-tok")
	if _, _, err := c.Put(context.Background(), strings.NewReader("x"), ""); err == nil {
		t.Fatal("Put to a closed server must error")
	}
}

// Get must surface ErrNotFound for a 404 with a JSON error body.
func TestGetNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	}))
	t.Cleanup(ts.Close)
	c := New(ts.URL, "op-tok")
	h := mustHash(t, "sha256:"+strings.Repeat("ab", 32))
	if _, _, err := c.Get(context.Background(), h); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get(404) = %v, want ErrNotFound", err)
	}
}
