// Package auth implements authentication and authorization for the cask
// server: per-role bearer tokens and the IP-based rate limiter (cas-api
// R-11/R-14). Session auth and CSRF for the viewer surface are added by the
// viewer milestone (viewer-security).
package auth

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Role names (viewer-security): viewer (reads) → operator (+ store/verify)
// → admin (+ delete/GC/prune).
const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// Authenticator maps bearer tokens to roles. Missing/invalid token → no
// role (→ 401); insufficient role → 403. Neither check discloses whether a
// target object exists.
type Authenticator struct {
	tokens map[string]string // token → role
}

// NewAuthenticator builds an Authenticator from token→role pairs.
func NewAuthenticator(tokens map[string]string) *Authenticator {
	return &Authenticator{tokens: tokens}
}

// Role returns the role for the request's bearer token, or "".
func (a *Authenticator) Role(r *http.Request) string {
	return a.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
}

// RequireRole wraps a handler: 401 without a valid token, 403 with an
// insufficient role, JSON error bodies per the CAS API error contract.
func (a *Authenticator) RequireRole(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			role := a.Role(r)
			if role == "" {
				writeJSON(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			for _, want := range roles {
				if role == want {
					next(w, r)
					return
				}
			}
			writeJSON(w, http.StatusForbidden, "forbidden")
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":` + strconv.Quote(msg) + "}\n"))
}

// RateLimitConfig configures the IP-based token-bucket limiter (R-14).
type RateLimitConfig struct {
	Enabled           bool
	RequestsPerSecond float64
	Burst             int
	ExemptLoopback    bool
	TrustedProxies    []string // peers allowed to supply X-Forwarded-For
}

// DefaultRateLimit is the canonical rate-limit default (defaults §3).
func DefaultRateLimit() RateLimitConfig {
	return RateLimitConfig{Enabled: true, RequestsPerSecond: 2, Burst: 20, ExemptLoopback: true}
}

// bucket is one IP's token bucket.
type bucket struct {
	tokens float64
	last   time.Time
}

const (
	idleWindow = time.Hour // idle entries are swept after this
	maxEntries = 10000     // hard cap on distinct IPs tracked
	sweepEvery = 10 * time.Minute
)

// RateLimiter is a std-lib token bucket per caller IP with a periodic sweep
// goroutine (R-14 memory hygiene). Safe for concurrent use. Call Stop to
// end the sweeper.
type RateLimiter struct {
	cfg  RateLimitConfig
	mu   sync.Mutex
	ips  map[string]*bucket
	done chan struct{}
	wg   sync.WaitGroup
}

// NewRateLimiter starts the limiter (and its background sweeper).
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{cfg: cfg, ips: make(map[string]*bucket), done: make(chan struct{})}
	rl.wg.Add(1)
	go rl.sweepLoop()
	return rl
}

// Stop ends the background sweeper and waits for it to exit.
func (rl *RateLimiter) Stop() {
	close(rl.done)
	rl.wg.Wait()
}

// Config returns the limiter's configuration (read-only accessor).
func (rl *RateLimiter) Config() RateLimitConfig { return rl.cfg }

func (rl *RateLimiter) sweepLoop() {
	defer rl.wg.Done()
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			rl.mu.Lock()
			now := time.Now()
			for k, b := range rl.ips {
				if now.Sub(b.last) > idleWindow {
					delete(rl.ips, k)
				}
			}
			rl.mu.Unlock()
		case <-rl.done:
			return
		}
	}
}

// Allow reports whether a request from ip may proceed, plus the Retry-After
// seconds and the remaining bucket capacity.
func (rl *RateLimiter) Allow(ip string) (ok bool, retryAfter int, remaining int) {
	if !rl.cfg.Enabled {
		return true, 0, rl.cfg.Burst
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.ips[ip]
	if !exists {
		if len(rl.ips) >= maxEntries {
			for k, v := range rl.ips {
				if now.Sub(v.last) > idleWindow {
					delete(rl.ips, k)
				}
			}
			if len(rl.ips) >= maxEntries {
				return false, 1, 0
			}
		}
		b = &bucket{tokens: float64(rl.cfg.Burst), last: now}
		rl.ips[ip] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(b.tokens+elapsed*rl.cfg.RequestsPerSecond, float64(rl.cfg.Burst))
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0, int(b.tokens)
	}
	retry := int((1 - b.tokens) / rl.cfg.RequestsPerSecond)
	if retry < 1 {
		retry = 1
	}
	return false, retry, int(b.tokens)
}

// ClientIP derives the caller IP: RemoteAddr by default; X-Forwarded-For
// only when the direct peer is a trusted proxy (R-14 — never trust
// client-supplied headers by default, or the limit is trivially bypassed).
func (rl *RateLimiter) ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	for _, trusted := range rl.cfg.TrustedProxies {
		if host == trusted {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				return strings.TrimSpace(parts[0])
			}
		}
	}
	return host
}

// IsLoopback reports whether the request's RemoteAddr is a loopback address.
func IsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
