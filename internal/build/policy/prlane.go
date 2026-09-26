package policy

// PRLaneTable is the server-side lane's layout: the coordination ref's namespace, the
// window a claim is honoured for, the local record this worktree keeps, and the overrides.
//
// The lane lives on the REMOTE — one open pull request is one lane — so the claim is a
// compare-and-swap on a ref, and every clone, machine and operator reads the same answer.
// Nothing here is a branch: the ref is a coordination marker, never pushed to and never
// committed to (docs/specs/branch-naming.md §2 governs the branch namespace).
type PRLaneTable struct {
	// Namespace is the ref namespace under `refs/`, and the path segment the API is asked
	// for: `matching-refs/<Namespace>` lists every lane.
	Namespace string
	// RefPrefix is the full ref prefix a lane's name is built under.
	RefPrefix string
	// StaleMinutes is the window in which a claim with no pull request yet is honoured:
	// the time a claimer has to create its worktree and run the gate. Past it the claim is
	// stale and the next claimer takes it over, so liveness needs no human judgement.
	StaleMinutes int
	// StaleEnv overrides StaleMinutes for one invocation.
	StaleEnv string
	// RepoEnv names the repository, for a caller with no origin remote to read.
	RepoEnv string
	// GhEnv names the GitHub CLI binary explicitly, for a host that keeps it elsewhere.
	GhEnv string
	// RecordFile is this worktree's own record of the lanes it claimed, inside its git
	// dir: the claim is on the server, the record only answers "did this checkout start
	// this landing".
	RecordFile string
	// PullRequestLimit caps the open pull requests one status reads.
	PullRequestLimit int
	// GhCandidates are the names and paths to try for the GitHub CLI, in order: plain `gh`
	// under Linux, `gh.exe` where Git Bash carries the Windows PATH, and the Windows CLI
	// again when the gate's WSL shell is the caller (interop makes `/mnt/c/...gh.exe`
	// callable).
	GhCandidates []string
}

// PRLane returns go-cask's lane table. It is a function rather than a package-level
// variable so a caller cannot mutate the policy by accident.
func PRLane() PRLaneTable {
	return PRLaneTable{
		Namespace:        "lane",
		RefPrefix:        "refs/lane/",
		StaleMinutes:     90,
		StaleEnv:         "PR_LANE_STALE_MINUTES",
		RepoEnv:          "PR_LANE_REPO",
		GhEnv:            "PR_LANE_GH",
		RecordFile:       "pr-lane",
		PullRequestLimit: 100,
		GhCandidates: []string{
			"gh",
			"gh.exe",
			"/mnt/c/Program Files/GitHub CLI/gh.exe",
			"/c/Program Files/GitHub CLI/gh.exe",
		},
	}
}
