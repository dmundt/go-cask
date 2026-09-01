package auth

import (
	"net/http/httptest"
	"testing"
)

func TestRateLimiterBurst(t *testing.T) {
	cfg := DefaultRateLimit()
	cfg.ExemptLoopback = false
	cfg.Burst = 3
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	ok, _, _ := rl.Allow("10.0.0.1")
	if !ok {
		t.Fatal("first request must pass")
	}
	for i := 0; i < 2; i++ {
		if ok, _, _ := rl.Allow("10.0.0.1"); !ok {
			t.Fatalf("request %d within burst must pass", i+2)
		}
	}
	if ok, retry, _ := rl.Allow("10.0.0.1"); ok {
		t.Fatal("request beyond burst must be limited")
	} else if retry < 1 {
		t.Fatalf("Retry-After = %d, want >= 1", retry)
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	cfg := DefaultRateLimit()
	cfg.ExemptLoopback = false
	cfg.Burst = 1
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	if ok, _, _ := rl.Allow("10.0.0.1"); !ok {
		t.Fatal("first IP must pass")
	}
	if ok, _, _ := rl.Allow("10.0.0.1"); ok {
		t.Fatal("same IP beyond burst must be limited")
	}
	if ok, _, _ := rl.Allow("10.0.0.2"); !ok {
		t.Fatal("a different IP has its own bucket")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{Enabled: false, Burst: 2})
	defer rl.Stop()
	for i := 0; i < 10; i++ {
		if ok, _, _ := rl.Allow("10.0.0.1"); !ok {
			t.Fatal("disabled limiter must always allow")
		}
	}
}

func TestIsLoopback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	if !IsLoopback(req) {
		t.Fatal("127.0.0.1 must be loopback")
	}
	req.RemoteAddr = "10.0.0.1:5555"
	if IsLoopback(req) {
		t.Fatal("10.0.0.1 must not be loopback")
	}
}

func TestClientIPTrustedProxy(t *testing.T) {
	cfg := DefaultRateLimit()
	cfg.TrustedProxies = []string{"10.0.0.9"}
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.9:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.9")
	if got := rl.ClientIP(req); got != "203.0.113.7" {
		t.Fatalf("trusted proxy ClientIP = %q, want the forwarded IP", got)
	}

	// Untrusted peer: X-Forwarded-For MUST be ignored (R-14 spoofing guard).
	req.RemoteAddr = "203.0.113.66:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := rl.ClientIP(req); got != "203.0.113.66" {
		t.Fatalf("untrusted peer ClientIP = %q, want RemoteAddr", got)
	}
}

func TestAuthenticatorRoles(t *testing.T) {
	a := NewAuthenticator(map[string]string{"admin-tok": "admin"})
	req := httptest.NewRequest("GET", "/", nil)
	if a.Role(req) != "" {
		t.Fatal("no token must yield no role")
	}
	req.Header.Set("Authorization", "Bearer admin-tok")
	if a.Role(req) != "admin" {
		t.Fatalf("role = %q, want admin", a.Role(req))
	}
}
