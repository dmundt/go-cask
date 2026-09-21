package web

import (
	"testing"
	"time"
)

// TestSessionsSweepAbandonedSessions covers the session a browser simply walks
// away from: nothing ever asks for it again, so only the sweep can reclaim it.
func TestSessionsSweepAbandonedSessions(t *testing.T) {
	store := newSessions()
	abandoned, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	aged, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	// One went idle; the other stayed busy but outlived its maximum lifetime.
	store.byID[abandoned.ID].LastSeen = time.Now().Add(-idleTimeout - time.Minute)
	store.byID[aged.ID].Created = time.Now().Add(-maxLifetime - time.Minute)
	store.mu.Unlock()

	live, err := store.create(RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	remaining := len(store.byID)
	_, keptLive := store.byID[live.ID]
	store.mu.Unlock()
	if remaining != 1 || !keptLive {
		t.Fatalf("after a login sweep %d sessions remain (live kept: %v), want only the live one", remaining, keptLive)
	}
}
