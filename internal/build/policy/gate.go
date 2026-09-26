package policy

// GateTable names the gate's entry points and the records it owns.
//
// Two entry points, one contract: a developer runs the gate before a landing, and the
// pre-push hook refuses a commit the gate did not verify. Both are shims over
// `cmd/buildtool`, which is where the rules live.
type GateTable struct {
	// Verify is the entry point a developer runs, and the one CI runs.
	Verify string
	// PrePush is the hook that authorises a push.
	PrePush string
	// Ledger is the ledger of verified commits, inside the shared git directory: every
	// worktree of a clone writes the same one.
	Ledger string
	// LedgerKeep is how many entries the writer keeps, newest first.
	LedgerKeep int
}

// Gate returns go-cask's gate table. It is a function rather than a package-level
// variable so a caller cannot mutate the gate's policy by accident.
func Gate() GateTable {
	return GateTable{
		Verify:     "scripts/verify.sh",
		PrePush:    ".githooks/pre-push",
		Ledger:     "verify.ok",
		LedgerKeep: 200,
	}
}
