package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/dmundt/go-cask/cas"
)

// server is the CAS API server: routes over a RawStore with bearer-token
// role auth and IP-based rate limiting.
type server struct {
	raw            *cas.FSRawStore
	tokens         map[string]string // token → role
	rl             *rateLimiter
	sizes          map[string]int64 // hash string → size (maintained at Put)
	trustedProxies map[string]bool
}

// New creates a server over raw with per-role tokens ("token" → role) and
// the given rate-limit config. The raw store MUST be an FSRawStore (the
// example serves a filesystem store; GC/Verify/Stats are FS operations).
func New(raw *cas.FSRawStore, tokens map[string]string, rlCfg RateLimitConfig) *server {
	return &server{
		raw:            raw,
		tokens:         tokens,
		rl:             newRateLimiter(rlCfg),
		sizes:          map[string]int64{},
		trustedProxies: map[string]bool{},
	}
}

// Handler returns the fully wired http.Handler: rate limit → auth → routes.
func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern string, roles []string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requireRole(roles, h))
	}
	route("POST /api/cas/v1/objects", []string{"operator", "admin"}, s.postObject)
	route("GET /api/cas/v1/objects", []string{"viewer", "operator", "admin"}, s.listObjects)
	route("GET /api/cas/v1/objects/{hash}", []string{"viewer", "operator", "admin"}, s.getObject)
	route("DELETE /api/cas/v1/objects/{hash}", []string{"admin"}, s.deleteObject)
	route("GET /api/cas/v1/objects/{hash}/meta", []string{"viewer", "operator", "admin"}, s.objectMeta)
	route("POST /api/cas/v1/objects/{hash}/verify", []string{"operator", "admin"}, s.verifyObject)
	route("GET /api/cas/v1/stats", []string{"viewer", "operator", "admin"}, s.stats)
	route("POST /api/cas/v1/gc", []string{"admin"}, s.gc)
	route("GET /api/cas/v1/openapi.yaml", []string{"viewer", "operator", "admin"}, s.openapi)
	return s.rateLimit(mux)
}

// rateLimit wraps the mux: 429 + Retry-After + X-RateLimit-* before auth
// (loopback exempt by default).
func (s *server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rl.cfg.ExemptLoopback && isLoopback(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		ok, retry, remaining := s.rl.allow(s.callerIP(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.rl.cfg.Burst))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// callerIP derives the client IP: X-Forwarded-For only via trusted proxies
// (per-caller identity).
func (s *server) callerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if s.trustedProxies[host] {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[0])
		}
	}
	return host
}

// requireRole enforces the bearer-token role matrix: 401 for a
// missing/invalid token, 403 for an insufficient role — neither discloses
// whether the target object exists.
func (s *server) requireRole(roles []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		role := s.tokens[token]
		if role == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		for _, want := range roles {
			if role == want {
				next(w, r)
				return
			}
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Store raw bytes — the hash is computed while streaming
// the body to a temp spool (memory-bounded), then the spool streams into
// the store. Identical bytes → identical hash → deduplicated.
func (s *server) postObject(w http.ResponseWriter, r *http.Request) {
	algo := r.URL.Query().Get("algo")
	if algo == "" {
		algo = "sha256"
	}
	hasher, err := cas.NewHasher(algo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	spool, err := os.CreateTemp("", "cask-upload-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload spool failed"})
		return
	}
	defer os.Remove(spool.Name())
	defer spool.Close()

	size, err := spoolAndHash(spool, hasher, r.Body)
	if err != nil || size == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
		return
	}
	h, err := cas.NewHash(algo, hasher.Sum(nil))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	exists, err := s.raw.Exists(ctx, h)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store check failed"})
		return
	}
	if !exists {
		if _, err := spool.Seek(0, 0); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "spool rewind failed"})
			return
		}
		if err := s.raw.Put(ctx, h, spool); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store failed"})
			return
		}
	}
	s.sizes[h.String()] = size
	writeJSON(w, http.StatusCreated, map[string]any{"hash": h.String(), "deduplicated": exists})
}

// List objects with algo filter and pagination.
func (s *server) listObjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := parseBounded(q.Get("limit"), 100, 1, 1000)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1-1000"})
		return
	}
	offset, err := parseBounded(q.Get("offset"), 0, 0, 1<<30)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be >= 0"})
		return
	}
	hashes, err := s.raw.List(r.Context(), q.Get("algo"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	total := len(hashes)
	lo := min(offset, total)
	hi := min(offset+limit, total)
	objects := make([]map[string]any, 0, hi-lo)
	for _, h := range hashes[lo:hi] {
		objects = append(objects, map[string]any{
			"hash":      h.String(),
			"algorithm": h.Algorithm(),
			"size":      s.sizes[h.String()],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "objects": objects})
}

// Stream the stored bytes with X-CAS-* metadata headers.
func (s *server) getObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	rc, err := s.raw.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer rc.Close()
	w.Header().Set("X-CAS-Algorithm", h.Algorithm())
	if size := s.sizes[h.String()]; size > 0 {
		w.Header().Set("X-CAS-Size", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, rc)
}

// DELETE /objects/{hash}: admin; deleting a missing object is a no-op.
func (s *server) deleteObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	if err := s.raw.Delete(r.Context(), h); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	delete(s.sizes, h.String())
	w.WriteHeader(http.StatusNoContent)
}

// Metadata — size always; type best-effort from the envelope.
func (s *server) objectMeta(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	rc, err := s.raw.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}
	// Type is best-effort from the self-describing envelope; references are
	// a typed-layer concern (this raw store cannot interpret them).
	writeJSON(w, http.StatusOK, map[string]any{
		"hash":       h.String(),
		"algorithm":  h.Algorithm(),
		"size":       len(data),
		"type":       envelopeType(data),
		"references": []string{},
	})
}

// Integrity — recompute and compare (operator).
func (s *server) verifyObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	rc, err := s.raw.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}
	recomputed, err := cas.HashBytes(h.Algorithm(), data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hash":       h.String(),
		"valid":      recomputed.Equal(h),
		"recomputed": recomputed.String(),
	})
}

// Storage statistics.
func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.raw.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stats failed"})
		return
	}
	counts := make(map[string]int64, len(st.AlgorithmCounts))
	for algo, n := range st.AlgorithmCounts {
		counts[algo] = int64(n)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object_count":     st.ObjectCount,
		"total_size":       st.TotalSize,
		"algorithm_counts": counts,
	})
}

// Mark-and-sweep from the reachable set (admin).
func (s *server) gc(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reachable []string `json:"reachable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reachable set"})
		return
	}
	reachable := make(map[string]bool, len(body.Reachable))
	for _, hs := range body.Reachable {
		h, err := cas.ParseHash(hs)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid hash in reachable set"})
			return
		}
		reachable[h.String()] = true
	}
	before, _ := s.raw.Stats(r.Context())
	if err := s.raw.GC(r.Context(), reachable); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "gc failed"})
		return
	}
	after, _ := s.raw.Stats(r.Context())
	deleted := before.ObjectCount - after.ObjectCount
	for hs := range s.sizes {
		if !reachable[hs] {
			delete(s.sizes, hs)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (s *server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Write(openapiYAML)
}

// parseHashParam validates {hash} with ParseHash → 400 on malformed.
func parseHashParam(w http.ResponseWriter, r *http.Request) (cas.Hash, bool) {
	h, err := cas.ParseHash(r.PathValue("hash"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed hash"})
		return nil, false
	}
	return h, true
}

func parseBounded(raw string, def, lo, hi int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("out of bounds")
	}
	return n, nil
}
