// Package api implements the CAS API handlers (/api/cas/v1) for the cask
// server: content-addressed store with dedup, streaming uploads/downloads,
// metadata, paginated listing, verify, GC, stats, and the self-served
// OpenAPI document — per cas-api.instructions.md (R-01…R-14).
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/internal/auth"
	"github.com/dmundt/go-cask/internal/index"
	"github.com/dmundt/go-cask/internal/storage"
)

//go:embed openapi.yaml
var openapiYAML []byte

// Server is the CAS API handler set over the shared store.
type Server struct {
	store *storage.Store
	auth  *auth.Authenticator
	rl    *auth.RateLimiter
}

// New builds the CAS API server over store with per-role tokens and the
// rate-limit config.
func New(store *storage.Store, tokens map[string]string, rlCfg auth.RateLimitConfig) *Server {
	return &Server{store: store, auth: auth.NewAuthenticator(tokens), rl: auth.NewRateLimiter(rlCfg)}
}

// Close stops the rate limiter's background sweeper.
func (s *Server) Close() { s.rl.Stop() }

// Handler returns the fully wired http.Handler: rate limit → auth → routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern string, roles []string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.auth.RequireRole(roles...)(h))
	}
	route("POST /api/cas/v1/objects", []string{auth.RoleOperator, auth.RoleAdmin}, s.postObject)
	route("GET /api/cas/v1/objects", []string{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin}, s.listObjects)
	route("GET /api/cas/v1/objects/{hash}", []string{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin}, s.getObject)
	route("DELETE /api/cas/v1/objects/{hash}", []string{auth.RoleAdmin}, s.deleteObject)
	route("GET /api/cas/v1/objects/{hash}/meta", []string{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin}, s.objectMeta)
	route("POST /api/cas/v1/objects/{hash}/verify", []string{auth.RoleOperator, auth.RoleAdmin}, s.verifyObject)
	route("GET /api/cas/v1/stats", []string{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin}, s.stats)
	route("POST /api/cas/v1/gc", []string{auth.RoleAdmin}, s.gc)
	route("GET /api/cas/v1/openapi.yaml", []string{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin}, s.openapi)
	return s.rateLimit(mux)
}

// rateLimit wraps the mux: 429 + Retry-After + X-RateLimit-* before auth
// (R-14; loopback exempt by default).
func (s *Server) rateLimit(next http.Handler) http.Handler {
	cfg := s.rl.Config()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.ExemptLoopback && auth.IsLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		ok, retry, remaining := s.rl.Allow(s.rl.ClientIP(r))
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.Burst))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			writeJSON(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// R-01/R-02/R-04: store raw bytes — the hash is computed while streaming
// the body to a temp spool (memory-bounded), then the spool streams into
// the store. Identical bytes → identical hash → deduplicated.
func (s *Server) postObject(w http.ResponseWriter, r *http.Request) {
	algo := r.URL.Query().Get("algo")
	if algo == "" {
		algo = "sha256"
	}
	hasher, err := newHasher(algo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	spool, err := os.CreateTemp("", "cask-upload-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "upload spool failed")
		return
	}
	defer os.Remove(spool.Name())
	defer spool.Close()

	size, err := spoolAndHash(spool, hasher, r.Body)
	if err != nil || size == 0 {
		writeJSON(w, http.StatusBadRequest, "empty body")
		return
	}
	h, err := cas.NewHash(algo, hasher.Sum(nil))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	exists, err := s.store.Exists(ctx, h)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "store check failed")
		return
	}
	if !exists {
		if _, err := spool.Seek(0, 0); err != nil {
			writeJSON(w, http.StatusInternalServerError, "spool rewind failed")
			return
		}
		if _, err := s.store.Put(ctx, h, spool); err != nil {
			writeJSON(w, http.StatusInternalServerError, "store failed")
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"hash": h.String(), "deduplicated": exists})
}

// R-05: list objects with algo filter and pagination.
func (s *Server) listObjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := parseBounded(q.Get("limit"), 100, 1, 1000)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "limit must be 1-1000")
		return
	}
	offset, err := parseBounded(q.Get("offset"), 0, 0, 1<<30)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "offset must be >= 0")
		return
	}
	hashes, err := s.store.List(r.Context(), q.Get("algo"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "list failed")
		return
	}
	page := index.Paginate(hashes, offset, limit)
	objects := make([]map[string]any, 0, len(page))
	for _, h := range page {
		objects = append(objects, map[string]any{
			"hash":      h.String(),
			"algorithm": h.Algorithm(),
			"size":      s.store.Size(h),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(hashes), "objects": objects})
}

// R-03/R-04: stream the stored bytes with X-CAS-* metadata headers.
func (s *Server) getObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	rc, err := s.store.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, "not found")
		return
	}
	defer rc.Close()
	w.Header().Set("X-CAS-Algorithm", h.Algorithm())
	if size := s.store.Size(h); size > 0 {
		w.Header().Set("X-CAS-Size", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, rc)
}

// DELETE /objects/{hash}: admin; deleting a missing object is a no-op.
func (s *Server) deleteObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	if err := s.store.Delete(r.Context(), h); err != nil {
		writeJSON(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// R-06: metadata — size always; type best-effort from the envelope;
// references are a typed-layer concern (this raw store cannot interpret
// them).
func (s *Server) objectMeta(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	rc, err := s.store.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, "not found")
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "read failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hash":       h.String(),
		"algorithm":  h.Algorithm(),
		"size":       len(data),
		"type":       index.EnvelopeType(data),
		"references": []string{},
	})
}

// R-07: integrity — recompute and compare (operator).
func (s *Server) verifyObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseHashParam(w, r)
	if !ok {
		return
	}
	rc, err := s.store.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, "not found")
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "read failed")
		return
	}
	recomputed, err := hashBytes(h.Algorithm(), data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hash":       h.String(),
		"valid":      recomputed.Equal(h),
		"recomputed": recomputed.String(),
	})
}

// R-09: storage statistics.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "stats failed")
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

// R-08: mark-and-sweep from the reachable set (admin).
func (s *Server) gc(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reachable []string `json:"reachable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, "invalid reachable set")
		return
	}
	reachable := make(map[string]bool, len(body.Reachable))
	for _, hs := range body.Reachable {
		h, err := cas.ParseHash(hs)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, "invalid hash in reachable set")
			return
		}
		reachable[h.String()] = true
	}
	deleted, err := s.store.GC(r.Context(), reachable)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "gc failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Write(openapiYAML)
}

// parseHashParam validates {hash} with ParseHash → 400 on malformed.
func parseHashParam(w http.ResponseWriter, r *http.Request) (cas.Hash, bool) {
	h, err := cas.ParseHash(r.PathValue("hash"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "malformed hash")
		return nil, false
	}
	return h, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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

// --- hashing helpers (registered algorithms; R-01) ---

func newHasher(algo string) (hash.Hash, error) {
	return newHasherFor(algo)
}

func hashBytes(algo string, data []byte) (cas.Hash, error) {
	h, err := newHasherFor(algo)
	if err != nil {
		return nil, err
	}
	h.Write(data)
	return cas.NewHash(algo, h.Sum(nil))
}

func spoolAndHash(w io.Writer, hasher hash.Hash, r io.Reader) (int64, error) {
	return io.Copy(io.MultiWriter(w, hasher), r)
}
