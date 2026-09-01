// Package main implements the cas-api example: a standalone CAS HTTP API
// server (examples spec §3.4) implementing the /api/cas/v1 contract
// (cas-api.instructions.md, R-01…R-14): content-addressed store with dedup,
// streaming uploads/downloads, bearer-token role auth, IP-based rate
// limiting, metadata, list, verify, GC, stats, and a self-served OpenAPI
// document. The companion public SDK is the root `client/` package.
package main

import (
	"net"
	"sync"
	"time"
)

// RateLimitConfig configures the IP-based token-bucket limiter (R-14).
type RateLimitConfig struct {
	Enabled           bool
	RequestsPerSecond float64
	Burst             int
	ExemptLoopback    bool
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

// rateLimiter is a std-lib token bucket per caller IP: sustained rate with
// burst capacity. Per-IP state is lazily expired after idleWindow and
// bounded by maxEntries (R-14 memory hygiene). Safe for concurrent use.
type rateLimiter struct {
	cfg        RateLimitConfig
	mu         sync.Mutex
	ips        map[string]*bucket
	idleWindow time.Duration
	maxEntries int
}

func newRateLimiter(cfg RateLimitConfig) *rateLimiter {
	return &rateLimiter{
		cfg:        cfg,
		ips:        make(map[string]*bucket),
		idleWindow: time.Hour,
		maxEntries: 10000,
	}
}

// allow reports whether a request from ip may proceed, plus the
// Retry-After seconds and the remaining bucket capacity.
func (rl *rateLimiter) allow(ip string) (ok bool, retryAfter int, remaining int) {
	if !rl.cfg.Enabled {
		return true, 0, rl.cfg.Burst
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.ips[ip]
	if !exists {
		// Evict idle entries and enforce the size guard lazily.
		if len(rl.ips) >= rl.maxEntries {
			for k, v := range rl.ips {
				if now.Sub(v.last) > rl.idleWindow {
					delete(rl.ips, k)
				}
			}
			if len(rl.ips) >= rl.maxEntries {
				return false, 1, 0
			}
		}
		b = &bucket{tokens: float64(rl.cfg.Burst), last: now}
		rl.ips[ip] = b
	}

	// Refill: tokens += elapsed * rate, capped at burst.
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

// isLoopback reports whether the host part of addr is a loopback address.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
