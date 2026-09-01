package web

import (
	"sync"
	"time"
)

// throttle rate-limits login failures per caller IP (viewer-security §5):
// maxFailures failures per window, then blocked until the window passes
// (backoff). Safe for concurrent use.
type throttle struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	failures map[string][]time.Time
}

func newThrottle(max int, window time.Duration) *throttle {
	return &throttle{max: max, window: window, failures: make(map[string][]time.Time)}
}

// blocked reports whether ip has exceeded the failure budget in the window.
func (t *throttle) blocked(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	keep := t.failures[ip][:0]
	for _, ts := range t.failures[ip] {
		if now.Sub(ts) < t.window {
			keep = append(keep, ts)
		}
	}
	t.failures[ip] = keep
	return len(keep) >= t.max
}

// fail records a failed attempt for ip.
func (t *throttle) fail(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures[ip] = append(t.failures[ip], time.Now())
}

// reset clears the failure record for ip (successful login).
func (t *throttle) reset(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, ip)
}
