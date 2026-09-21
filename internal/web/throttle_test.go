package web

import (
	"strconv"
	"testing"
	"time"
)

// TestThrottleSweepsRotatingAddresses covers the case the throttle exists to
// survive: a caller that rotates source addresses. Such a caller never
// exhausts a per-IP budget, so only an unconditional sweep can keep the state
// map from growing by one entry per address.
func TestThrottleSweepsRotatingAddresses(t *testing.T) {
	th := newThrottle(5, time.Minute)
	stale := time.Now().Add(-2 * time.Minute)
	th.mu.Lock()
	for i := range sweepThreshold + 1 {
		th.attempts[strconv.Itoa(i)] = &ipState{recent: []time.Time{stale}}
	}
	th.mu.Unlock()

	if !th.allow("fresh") {
		t.Fatal("a first attempt from a new address must be allowed")
	}
	th.mu.Lock()
	remaining := len(th.attempts)
	th.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("attempt state holds %d addresses, want only the fresh one", remaining)
	}
}

// TestThrottleSweepKeepsBlockedAddresses makes sure the sweep cannot be used
// to clear a block: state for a blocked address survives it.
func TestThrottleSweepKeepsBlockedAddresses(t *testing.T) {
	th := newThrottle(2, time.Minute)
	for range 2 {
		if !th.allow("attacker") {
			t.Fatal("the first attempts are within budget")
		}
	}
	if th.allow("attacker") {
		t.Fatal("the third attempt exhausts the budget and must be blocked")
	}
	th.mu.Lock()
	for i := range sweepThreshold + 1 {
		th.attempts["filler"+strconv.Itoa(i)] = &ipState{}
	}
	th.mu.Unlock()
	if th.allow("attacker") {
		t.Fatal("a sweep must not clear an active block")
	}
}
