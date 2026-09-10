package web

import (
	"sync"
	"time"
)

// maxBackoff caps the exponential backoff applied after an IP exhausts its
// attempt budget (viewer-security §5).
const maxBackoff = 30 * time.Minute

// sweepThreshold is the state-map size above which stale per-IP state is
// reclaimed, so a caller rotating source addresses cannot grow the map
// without bound.
const sweepThreshold = 1024

// throttle rate-limits login attempts per caller IP (viewer-security §5): an
// IP may make max attempts per window; once the budget is exhausted the IP is
// blocked for a backoff that doubles with every consecutive exhaustion, capped
// at maxBackoff. A successful login resets the record. Safe for concurrent
// use: the budget check and the recording happen in one critical section, so
// concurrent attempts cannot slip past the limit.
type throttle struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	attempts map[string]*ipState
}

// ipState is one caller IP's rate-limit state.
type ipState struct {
	recent       []time.Time // attempts recorded inside the current window
	blockedUntil time.Time   // zero while the IP is not blocked
	strikes      int         // consecutive exhaustions (backoff exponent)
}

func newThrottle(max int, window time.Duration) *throttle {
	return &throttle{max: max, window: window, attempts: make(map[string]*ipState)}
}

// allow reports whether ip may attempt a login now and records the attempt in
// the same critical section. When the budget is exhausted, ip is blocked for
// an exponentially growing backoff and allow returns false.
func (t *throttle) allow(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	st := t.attempts[ip]
	if st == nil {
		st = &ipState{}
		t.attempts[ip] = st
	}
	if now.Before(st.blockedUntil) {
		return false
	}
	keep := st.recent[:0]
	for _, ts := range st.recent {
		if now.Sub(ts) < t.window {
			keep = append(keep, ts)
		}
	}
	st.recent = keep
	if len(st.recent) >= t.max {
		st.strikes++
		st.blockedUntil = now.Add(t.backoff(st.strikes))
		st.recent = nil
		t.sweepLocked(now)
		return false
	}
	st.recent = append(st.recent, now)
	return true
}

// reset clears the record for ip after a successful login.
func (t *throttle) reset(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, ip)
}

// backoff returns the block duration after strikes consecutive exhaustions:
// window, 2×window, 4×window, … capped at maxBackoff.
func (t *throttle) backoff(strikes int) time.Duration {
	d := t.window
	for i := 1; i < strikes; i++ {
		if d >= maxBackoff/2 {
			return maxBackoff
		}
		d *= 2
	}
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// sweepLocked drops state for IPs that are neither blocked nor have attempts
// inside the window.
func (t *throttle) sweepLocked(now time.Time) {
	if len(t.attempts) <= sweepThreshold {
		return
	}
	for ip, st := range t.attempts {
		if now.Before(st.blockedUntil) {
			continue
		}
		if len(st.recent) == 0 || now.Sub(st.recent[len(st.recent)-1]) >= t.window {
			delete(t.attempts, ip)
		}
	}
}
