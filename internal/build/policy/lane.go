package policy

// LandLaneTable is the local advisory slot's layout: where its records live and how
// long a slot may stay idle before another session may take it.
//
// The slot is one clone's own: it lives in the shared git directory, so every worktree
// of a clone sees the same one, and nothing here is ever committed. It is not the lane
// — the lane is the pull request, held on the server — it only keeps two gate runs in
// one clone from overlapping, so a busy clone does not pay twice for the same tree.
type LandLaneTable struct {
	// Dir is the slot's directory inside the shared git directory.
	Dir string
	// Owner is the record naming the holder; RepoID is the clone's random id, written
	// once and read by every worktree; Takeover is the record an eviction leaves for
	// the holder it evicted.
	Owner  string
	RepoID string
	// Takeover is the record an eviction leaves behind.
	Takeover string
	// Token is this worktree's own acquisition token, inside its own git directory.
	// It is per worktree, not per clone: only the acquisition that wrote it may renew
	// or release the slot.
	Token string
	// StaleMinutes is the idle window after which another session may take the slot
	// over. Idle, not age: a gate run plus a push that outlasts the window is a live
	// landing, and `renew` is how its holder pushes the deadline out.
	StaleMinutes int
	// StaleEnv overrides StaleMinutes for one invocation.
	StaleEnv string
}

// LandLane returns go-cask's advisory-slot layout. It is a function rather than a
// package-level variable so a caller cannot mutate the gate's policy by accident.
func LandLane() LandLaneTable {
	return LandLaneTable{
		Dir:          "dsh-land-lane",
		Owner:        "owner",
		RepoID:       "repo-id",
		Takeover:     "takeover",
		Token:        "dsh-land-lane-mine",
		StaleMinutes: 90,
		StaleEnv:     "LAND_LANE_STALE_MINUTES",
	}
}
