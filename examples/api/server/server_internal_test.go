package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// This file covers the api-server pieces the route tests cannot reach through
// the wire: the unexported helpers and the handler branches that need a
// caller-supplied state (trusted proxies, an exhausted limiter, a raw
// un-enveloped object). Every case states what it asserts.

// objectSize answers from the backend's physical metadata, so an object this
// process never wrote still reports its size, and an absent one reports 0.
func TestObjectSizeAbsentIsZero(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, nil, DefaultRateLimit())

	present := sha256.Of([]byte("physically present"))
	if err := backend.Put(ctx, present, strings.NewReader("physically present")); err != nil {
		t.Fatal(err)
	}
	if got := srv.objectSize(ctx, present); got != int64(len("physically present")) {
		t.Fatalf("objectSize(present) = %d, want 15", got)
	}
	if got := srv.objectSize(ctx, sha256.Of([]byte("absent"))); got != 0 {
		t.Fatalf("objectSize(absent) = %d, want 0", got)
	}
}

// callerIP uses the socket peer unless the peer is a trusted proxy, in which
// case the first X-Forwarded-For hop is the caller. An unparseable RemoteAddr
// is returned verbatim.
func TestCallerIP(t *testing.T) {
	cases := []struct {
		name           string
		remoteAddr     string
		xff            string
		trustedProxies map[string]bool
		want           string
	}{
		{
			name:       "socket peer when no proxy is trusted",
			remoteAddr: "203.0.113.7:54321",
			xff:        "198.51.100.9",
			want:       "203.0.113.7",
		},
		{
			name:           "first X-Forwarded-For hop from a trusted proxy",
			remoteAddr:     "10.0.0.1:443",
			xff:            "198.51.100.9, 10.0.0.1",
			trustedProxies: map[string]bool{"10.0.0.1": true},
			want:           "198.51.100.9",
		},
		{
			name:           "trusted proxy with no X-Forwarded-For header",
			remoteAddr:     "10.0.0.1:443",
			trustedProxies: map[string]bool{"10.0.0.1": true},
			want:           "10.0.0.1",
		},
		{
			name:       "unparseable RemoteAddr is returned verbatim",
			remoteAddr: "not-an-address",
			want:       "not-an-address",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &server{trustedProxies: tc.trustedProxies}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := srv.callerIP(req); got != tc.want {
				t.Fatalf("callerIP(remote=%q, xff=%q) = %q, want %q", tc.remoteAddr, tc.xff, got, tc.want)
			}
		})
	}
}

// isLoopback accepts the host forms a caller can present, including an address
// with no port (SplitHostPort fails, so the whole string is the host).
func TestIsLoopback(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want bool
	}{
		{name: "IPv4 loopback with port", addr: "127.0.0.1:8080", want: true},
		{name: "IPv6 loopback with port", addr: "[::1]:8080", want: true},
		{name: "loopback address without a port", addr: "127.0.0.1", want: true},
		{name: "public address with port", addr: "203.0.113.7:8080", want: false},
		{name: "non-address without a port", addr: "not-an-ip", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoopback(tc.addr); got != tc.want {
				t.Fatalf("isLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// An empty body never reaches the store: the handler answers 400 and stores
// nothing.
func TestPostObjectRejectsEmptyBody(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())

	status, body := c.do(ctx, http.MethodPost, "/api/cas/v1/objects", strings.NewReader(""))
	if status != http.StatusBadRequest || !strings.Contains(string(body), "empty body") {
		t.Fatalf("empty put = (%d, %s), want 400 empty body", status, body)
	}
	status, l := c.list(ctx)
	if status != http.StatusOK || num(l, "total") != 0 {
		t.Fatalf("list after rejected put = (%d, %v), want 0 objects", status, l)
	}
}

// Pagination is a window over the digest list: out-of-range limit/offset are
// 400 (a negative offset cannot even be expressed, so the upper bound is the
// tested one), and an offset past the end yields an empty window, never a
// panic.
func TestListPagination(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	for _, body := range []string{"a", "b", "c"} {
		if status, _, _ := c.put(ctx, body); status != http.StatusCreated {
			t.Fatalf("put %q status = %d", body, status)
		}
	}

	cases := []struct {
		name     string
		query    string
		want     int
		wantObjs int
	}{
		{name: "default window returns everything", query: "", want: http.StatusOK, wantObjs: 3},
		{name: "limit bounds the window", query: "?limit=2", want: http.StatusOK, wantObjs: 2},
		{name: "offset skips the head", query: "?limit=2&offset=1", want: http.StatusOK, wantObjs: 2},
		{name: "offset past the end is an empty window", query: "?offset=99", want: http.StatusOK, wantObjs: 0},
		{name: "limit above the maximum is rejected", query: "?limit=1001", want: http.StatusBadRequest},
		{name: "non-numeric limit is rejected", query: "?limit=many", want: http.StatusBadRequest},
		{name: "limit below the minimum is rejected", query: "?limit=0", want: http.StatusBadRequest},
		{name: "offset above the maximum is rejected", query: "?offset=1073741825", want: http.StatusBadRequest},
		{name: "non-numeric offset is rejected", query: "?offset=soon", want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, l := c.doList(ctx, tc.query)
			if status != tc.want {
				t.Fatalf("GET /objects%s = %d, want %d", tc.query, status, tc.want)
			}
			if tc.want != http.StatusOK {
				return
			}
			if num(l, "total") != 3 {
				t.Fatalf("total = %v, want 3", l["total"])
			}
			objs, ok := l["objects"].([]any)
			if !ok {
				t.Fatalf("objects = %T, want a list", l["objects"])
			}
			if len(objs) != tc.wantObjs {
				t.Fatalf("window = %d objects, want %d", len(objs), tc.wantObjs)
			}
		})
	}
}

func (c *testClient) doList(ctx context.Context, query string) (int, map[string]any) {
	status, b := c.do(ctx, http.MethodGet, "/api/cas/v1/objects"+query, nil)
	var l map[string]any
	if err := json.Unmarshal(b, &l); err != nil {
		return status, map[string]any{}
	}
	return status, l
}

// GC with an empty reachable set deletes every object: the report equals the
// store's own before/after difference.
func TestGCRemovesEverythingWithEmptyReachableSet(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, map[string]string{"admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := &testClient{base: ts.URL, token: "admin-tok", hc: ts.Client()}

	for _, body := range []string{"one", "two"} {
		if status, _, _ := admin.put(ctx, body); status != http.StatusCreated {
			t.Fatalf("put %q status = %d", body, status)
		}
	}
	status, g := admin.gc(ctx, nil)
	if status != http.StatusOK || num(g, "deleted") != 2 {
		t.Fatalf("gc(empty roots) = (%d, %v), want deleted 2", status, g)
	}
	if st, err := backend.Stats(ctx); err != nil || st.ObjectCount != 0 {
		t.Fatalf("store after gc = (%+v, %v), want empty", st, err)
	}
}

// A reachable set that is not a JSON object of strings is 400 and deletes
// nothing.
func TestGCRejectsMalformedBody(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, map[string]string{"admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := &testClient{base: ts.URL, token: "admin-tok", hc: ts.Client()}

	if status, _, _ := admin.put(ctx, "keep me"); status != http.StatusCreated {
		t.Fatalf("put status = %d", status)
	}
	cases := []struct {
		name string
		body string
	}{
		{name: "not JSON", body: "{"},
		{name: "unknown field", body: `{"roots":[]}`},
		{name: "reachable is not a list", body: `{"reachable":"deadbeef"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, b := admin.do(ctx, http.MethodPost, "/api/cas/v1/gc", strings.NewReader(tc.body))
			if status != http.StatusBadRequest {
				t.Fatalf("gc(%s) = %d body=%s, want 400", tc.name, status, b)
			}
			if st, err := backend.Stats(ctx); err != nil || st.ObjectCount != 1 {
				t.Fatalf("gc(%s) changed the store: (%+v, %v)", tc.name, st, err)
			}
		})
	}
}

// A reachable-entry that is not a digest is 400 and deletes nothing.
func TestGCRejectsMalformedDigest(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, map[string]string{"admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := &testClient{base: ts.URL, token: "admin-tok", hc: ts.Client()}

	if status, _, _ := admin.put(ctx, "keep me"); status != http.StatusCreated {
		t.Fatalf("put status = %d", status)
	}
	status, b := admin.do(ctx, http.MethodPost, "/api/cas/v1/gc", strings.NewReader(`{"reachable":["not-a-digest"]}`))
	if status != http.StatusBadRequest {
		t.Fatalf("gc(malformed digest) = %d body=%s, want 400", status, b)
	}
	if st, err := backend.Stats(ctx); err != nil || st.ObjectCount != 1 {
		t.Fatalf("gc(malformed digest) changed the store: (%+v, %v)", st, err)
	}
}

// writeJSON commits the status and headers before encoding, so a value the
// encoder rejects (a NaN float) cannot become an error response: the status
// stays what the caller asked for.
func TestWriteJSONEncodeFailureKeepsStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusTeapot, map[string]any{"size": math.NaN()})

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d (headers are committed before encoding)", rec.Code, http.StatusTeapot)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

// The OpenAPI route serves the embedded document byte for byte, with the
// YAML content type.
func TestOpenAPIWritesEmbeddedDocument(t *testing.T) {
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, map[string]string{"viewer-tok": "viewer"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	c := &testClient{base: ts.URL, token: "viewer-tok", hc: ts.Client()}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.base+"/api/cas/v1/openapi.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/yaml; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/yaml; charset=utf-8", ct)
	}
	doc, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(doc) != string(openapiYAML) {
		t.Fatalf("openapi body is not the embedded document (%d bytes vs %d)", len(doc), len(openapiYAML))
	}
}

// parseBounded: empty means the default; a non-numeric or out-of-range value is
// an error, and the inclusive bounds are accepted.
func TestParseBounded(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "empty uses the default", in: "", want: 100},
		{name: "value at the lower bound", in: "1", want: 1},
		{name: "value inside the range", in: "42", want: 42},
		{name: "value at the upper bound", in: "1000", want: 1000},
		{name: "non-numeric", in: "many", wantErr: true},
		{name: "below the lower bound", in: "0", wantErr: true},
		{name: "above the upper bound", in: "1001", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBounded(tc.in, 100, 1, 1000)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseBounded(%q) = %d, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBounded(%q) = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseBounded(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// A disabled limiter never counts: every request answers with the full burst
// as its remaining capacity.
func TestRateLimiterDisabled(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{Enabled: false, Burst: 7})
	for i := range 3 {
		ok, retry, remaining := rl.allow("203.0.113.7")
		if !ok || retry != 0 || remaining != 7 {
			t.Fatalf("allow #%d = (%v, %d, %d), want (true, 0, 7)", i, ok, retry, remaining)
		}
	}
}

// A fresh IP starts with a full bucket: the first request is allowed and
// reports the tokens left after spending one.
func TestRateLimiterFirstRequestSpendsOneToken(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{Enabled: true, RequestsPerSecond: 1, Burst: 3})
	ok, retry, remaining := rl.allow("203.0.113.7")
	if !ok || retry != 0 || remaining != 2 {
		t.Fatalf("first allow = (%v, %d, %d), want (true, 0, 2)", ok, retry, remaining)
	}
}

// An exhausted bucket is refused with a Retry-After of at least one second.
func TestRateLimiterExhaustedReportsRetryAfter(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{Enabled: true, RequestsPerSecond: 1, Burst: 1})
	if ok, _, _ := rl.allow("203.0.113.7"); !ok {
		t.Fatal("the first request must be allowed")
	}
	ok, retry, remaining := rl.allow("203.0.113.7")
	if ok {
		t.Fatal("the second request must be refused: the burst is 1")
	}
	if retry < 1 {
		t.Fatalf("retryAfter = %d, want >= 1", retry)
	}
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}
}

// The lazy size guard evicts entries idle past idleWindow; when nothing is
// evictable the new caller is refused instead of growing the map.
func TestRateLimiterMaxEntries(t *testing.T) {
	cases := []struct {
		name       string
		idleWindow time.Duration
		wantNew    bool
	}{
		{name: "idle entries are evicted, the new caller proceeds", idleWindow: time.Nanosecond, wantNew: true},
		{name: "nothing is idle, the new caller is refused", idleWindow: time.Hour, wantNew: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rl := newRateLimiter(RateLimitConfig{Enabled: true, RequestsPerSecond: 1, Burst: 1})
			rl.idleWindow = tc.idleWindow
			rl.maxEntries = 2

			for _, ip := range []string{"10.0.0.1", "10.0.0.2"} {
				if ok, _, _ := rl.allow(ip); !ok {
					t.Fatalf("seeding %s must be allowed", ip)
				}
			}
			if tc.idleWindow == time.Nanosecond {
				time.Sleep(time.Millisecond) // make both seeds idle
			}

			ok, retry, remaining := rl.allow("10.0.0.3")
			if ok != tc.wantNew {
				t.Fatalf("allow at capacity = %v, want %v", ok, tc.wantNew)
			}
			if tc.wantNew {
				if _, exists := rl.ips["10.0.0.3"]; !exists {
					t.Fatal("the evicting limiter must admit the new caller")
				}
				return
			}
			if retry < 1 || remaining != 0 {
				t.Fatalf("refusal = (retry %d, remaining %d), want retry >= 1 and remaining 0", retry, remaining)
			}
			if _, exists := rl.ips["10.0.0.3"]; exists {
				t.Fatal("a refused caller must not create an entry")
			}
		})
	}
}

// rateLimit exempts loopback when configured, before auth — so an anonymous
// loopback request reaches requireRole and gets 401, not 429.
func TestRateLimitExemptsLoopback(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultRateLimit()
	cfg.ExemptLoopback = true
	cfg.Burst = 1
	c, _ := newTestServer(t, cfg)

	// The httptest client dials 127.0.0.1, so it is loopback and exempt: all
	// five requests are authenticated (401), none is rate limited.
	for i := range 5 {
		status, _ := c.do(ctx, http.MethodPost, "/api/cas/v1/objects", strings.NewReader("x"))
		if status != http.StatusCreated {
			t.Fatalf("exempt loopback put #%d = %d, want 201", i, status)
		}
	}
}

// The rate-limit headers name the limit and what is left when a request is
// refused, so a client can back off without guessing.
func TestRateLimitHeaders(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultRateLimit()
	cfg.ExemptLoopback = false
	cfg.Burst = 1
	c, _ := newTestServer(t, cfg)

	if status, _, _ := c.put(ctx, "x"); status != http.StatusCreated {
		t.Fatalf("first put = %d, want 201", status)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/cas/v1/objects", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second put = %d, want 429", resp.StatusCode)
	}
	for _, h := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining"} {
		if resp.Header.Get(h) == "" {
			t.Fatalf("429 response is missing %s", h)
		}
	}
	if got := resp.Header.Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("X-RateLimit-Limit = %q, want 1", got)
	}
}

// A GET of an absent object is 404 and carries no body stream.
func TestGetMissingObject(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	missing := sha256.Of([]byte("never stored here"))
	status, body := c.getBytes(ctx, missing)
	if status != http.StatusNotFound {
		t.Fatalf("get(missing) = %d body=%s, want 404", status, body)
	}
}

// Deleting an absent object is a documented no-op, not an error. The 204 path
// itself is covered by the role matrix.
func TestDeleteAbsentObjectIsNoOp(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, map[string]string{"admin-tok": "admin"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := &testClient{base: ts.URL, token: "admin-tok", hc: ts.Client()}

	missing := sha256.Of([]byte("never stored here"))
	if status, _ := admin.del(ctx, missing); status != http.StatusNoContent {
		t.Fatalf("delete(missing) = %d, want 204", status)
	}
}

// A missing object cannot be verified: 404, never a fabricated "valid".
func TestVerifyMissingObject(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	missing := sha256.Of([]byte("never stored here"))
	status, body := c.do(ctx, http.MethodPost, "/api/cas/v1/objects/"+missing.String()+"/verify", nil)
	if status != http.StatusNotFound {
		t.Fatalf("verify(missing) = %d body=%s, want 404", status, body)
	}
}

// A raw (un-enveloped) object is ErrCorrupt to HeaderType: the metadata
// handler reports it untyped with its real size instead of failing.
func TestObjectMetaRawObjectIsUntyped(t *testing.T) {
	ctx := context.Background()
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(backend, map[string]string{"viewer-tok": "viewer"}, DefaultRateLimit())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	raw := []byte("raw bytes without a TLV header")
	h := sha256.Of(raw)
	if err := backend.Put(ctx, h, strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	typ, err := cas.HeaderType(ctx, backend, h)
	if err == nil {
		t.Fatalf("HeaderType(raw) = %q, nil; want ErrCorrupt", typ)
	}

	c := &testClient{base: ts.URL, token: "viewer-tok", hc: ts.Client()}
	status, m := c.meta(ctx, h)
	if status != http.StatusOK || m["type"] != "" {
		t.Fatalf("meta(raw) = (%d, %v), want 200 with an empty type", status, m)
	}
	if num(m, "size") != float64(len(raw)) {
		t.Fatalf("meta(raw).size = %v, want %d", m["size"], len(raw))
	}
}

// The metadata route serves an object stored as raw bytes (the byte layer
// keeps no envelope): its size is real and its type is reported as unknown,
// never invented.
func TestObjectMetaRawBytesIsUntyped(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestServer(t, DefaultRateLimit())
	status, h, _ := c.put(ctx, "typed payload")
	if status != http.StatusCreated {
		t.Fatalf("put = %d", status)
	}
	status, m := c.meta(ctx, h)
	if status != http.StatusOK {
		t.Fatalf("meta = %d, want 200", status)
	}
	// The API stores raw bytes (the store is the byte layer), so the type is
	// unknown — the handler must say so rather than invent one.
	if m["type"] != "" {
		t.Fatalf("meta.type = %v, want empty for a raw store", m["type"])
	}
}
