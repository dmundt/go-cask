package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// server is the CAS API server: routes over an fs.Backend with bearer-token
// role auth and IP-based rate limiting.
type server struct {
	backend        *fs.Backend
	tokens         map[string]string // token → role
	rl             *rateLimiter
	sizesMu        sync.RWMutex
	sizes          map[string]int64 // hash string → size (maintained at Put)
	trustedProxies map[string]bool
}

// setSize records an object's size.
func (s *server) setSize(hash string, size int64) {
	s.sizesMu.Lock()
	defer s.sizesMu.Unlock()
	s.sizes[hash] = size
}

// sizeOf returns a recorded size (0 when unknown).
func (s *server) sizeOf(hash string) int64 {
	s.sizesMu.RLock()
	defer s.sizesMu.RUnlock()
	return s.sizes[hash]
}

// forgetSize drops a recorded size.
func (s *server) forgetSize(hash string) {
	s.sizesMu.Lock()
	defer s.sizesMu.Unlock()
	delete(s.sizes, hash)
}

// retainSizes drops every recorded size not in reachable (used by GC).
func (s *server) retainSizes(reachable map[string]bool) {
	s.sizesMu.Lock()
	defer s.sizesMu.Unlock()
	for hs := range s.sizes {
		if !reachable[hs] {
			delete(s.sizes, hs)
		}
	}
}

// New creates a server over backend with per-role tokens ("token" → role) and
// the given rate-limit config. The backend store MUST be an FSBackend (the
// example serves a filesystem store; GC/Verify/Stats are FS operations).
func New(backend *fs.Backend, tokens map[string]string, rlCfg RateLimitConfig) *server {
	return &server{
		backend:        backend,
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
		if slices.Contains(roles, role) {
			next(w, r)
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line and headers are already committed, so the failure can
		// no longer become an error response — log it instead.
		slog.Error("cas api write json", "status", status, "err", err)
	}
}

// Store backend bytes — the digest is computed while streaming
// the body to a temp spool (memory-bounded), then the spool streams into
// the store. Identical bytes → identical digest → deduplicated.
func (s *server) postObject(w http.ResponseWriter, r *http.Request) {
	hasher := sha256.NewHasher()
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
	h := cas.NewDigest(hasher.Sum(nil))
	ctx := r.Context()
	exists, err := s.backend.Exists(ctx, h)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store check failed"})
		return
	}
	if !exists {
		if _, err := spool.Seek(0, 0); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "spool rewind failed"})
			return
		}
		if err := s.backend.Put(ctx, h, spool); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store failed"})
			return
		}
	}
	s.setSize(h.String(), size)
	slog.Info("cas api audit", "action", "put", "hash", h.String(), "size", size, "deduplicated", exists)
	writeJSON(w, http.StatusCreated, map[string]any{"hash": h.String(), "deduplicated": exists})
}

// List objects with pagination.
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
	digests, err := s.backend.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	total := len(digests)
	lo := min(offset, total)
	hi := min(offset+limit, total)
	objects := make([]map[string]any, 0, hi-lo)
	for _, h := range digests[lo:hi] {
		objects = append(objects, map[string]any{
			"hash":      h.String(),
			"algorithm": sha256.Name,
			"size":      s.sizeOf(h.String()),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "objects": objects})
}

// Stream the stored bytes with X-CAS-* metadata headers.
func (s *server) getObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigestParam(w, r)
	if !ok {
		return
	}
	rc, err := s.backend.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer rc.Close()
	w.Header().Set("X-CAS-Algorithm", sha256.Name)
	if size := s.sizeOf(h.String()); size > 0 {
		w.Header().Set("X-CAS-Size", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil {
		// A mid-stream failure (client disconnect, read error) cannot be turned
		// into an error response: 200 and the headers are already committed.
		slog.Error("cas api stream object", "hash", h.String(), "err", err)
	}
}

// DELETE /objects/{hash}: admin; deleting a missing object is a no-op.
func (s *server) deleteObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigestParam(w, r)
	if !ok {
		return
	}
	if err := s.backend.Delete(r.Context(), h); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	s.forgetSize(h.String())
	slog.Info("cas api audit", "action", "delete", "hash", h.String())
	w.WriteHeader(http.StatusNoContent)
}

// Metadata — size always; type best-effort from the envelope.
func (s *server) objectMeta(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigestParam(w, r)
	if !ok {
		return
	}
	rc, err := s.backend.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 4<<10)) // TLV header carries the type
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}
	size, err := s.backend.Size(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stat failed"})
		return
	}
	// Type is best-effort from the self-describing envelope; references are
	// a typed-layer concern (this backend store cannot interpret them).
	writeJSON(w, http.StatusOK, map[string]any{
		"hash":       h.String(),
		"algorithm":  sha256.Name,
		"size":       size,
		"type":       envelopeType(data),
		"references": []string{},
	})
}

// Integrity — recompute and compare (operator).
func (s *server) verifyObject(w http.ResponseWriter, r *http.Request) {
	h, ok := parseDigestParam(w, r)
	if !ok {
		return
	}
	rc, err := s.backend.Get(r.Context(), h)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer rc.Close()
	hasher := sha256.NewHasher()
	if _, err := io.Copy(hasher, rc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}
	recomputed := cas.NewDigest(hasher.Sum(nil))
	valid := recomputed.Equal(h)
	slog.Info("cas api audit", "action", "verify", "hash", h.String(), "valid", valid)
	writeJSON(w, http.StatusOK, map[string]any{
		"hash":       h.String(),
		"valid":      valid,
		"recomputed": recomputed.String(),
	})
}

// Storage statistics.
func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.backend.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stats failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object_count": st.ObjectCount,
		"total_size":   st.TotalSize,
		"algorithm":    sha256.Name,
	})
}

// Mark-and-sweep from the reachable set (admin).
func (s *server) gc(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reachable []string `json:"reachable"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reachable set"})
		return
	}
	reachable := make(map[string]bool, len(body.Reachable))
	for _, hs := range body.Reachable {
		h, err := sha256.Parse(hs)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid hash in reachable set"})
			return
		}
		reachable[h.String()] = true
	}
	before, err := s.backend.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stats failed"})
		return
	}
	if err := s.backend.GC(r.Context(), reachable); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "gc failed"})
		return
	}
	// The sweep already happened: keep the in-memory size index in step with
	// the store even if the post-GC stats below fail.
	s.retainSizes(reachable)
	after, err := s.backend.Stats(r.Context())
	if err != nil {
		// GC succeeded but its outcome cannot be reported truthfully, so answer
		// 500 rather than inventing a "deleted" count; the failure is logged.
		slog.Error("cas api gc stats", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stats failed"})
		return
	}
	deleted := before.ObjectCount - after.ObjectCount
	slog.Info("cas api audit", "action", "gc", "deleted", deleted)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (s *server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	if _, err := w.Write(openapiYAML); err != nil {
		// The body is a single write of an embedded asset; headers are already
		// committed, so a failure can only be logged.
		slog.Error("cas api write openapi", "err", err)
	}
}

// parseDigestParam validates the {hash} path value with sha256.Parse → 400 on
// malformed. (The URL/JSON word stays "hash": that is the user-facing
// vocabulary, per AGENT.md §6.)
func parseDigestParam(w http.ResponseWriter, r *http.Request) (cas.Digest, bool) {
	h, err := sha256.Parse(r.PathValue("hash"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed hash"})
		return nil, false
	}
	return h, true
}

func parseBounded(backend string, def, lo, hi int) (int, error) {
	if backend == "" {
		return def, nil
	}
	n, err := strconv.Atoi(backend)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("out of bounds")
	}
	return n, nil
}
