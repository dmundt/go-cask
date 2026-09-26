package policy

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateTableIsWellFormed pins the gate's entry points and its ledger.
//
// Both entry points are files a reader is sent to by name from AGENTS.md, the pre-push
// message and the scripts guide, so a renamed or moved one has to fail here rather than in
// a reader's terminal. The ledger is a bare name inside the shared git directory: a path
// would land somewhere no reader looks, and nothing may commit it, because a stamp that
// travelled between clones would authorise a push this clone never verified.
func TestGateTableIsWellFormed(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	table := Gate()
	for _, field := range []struct {
		name  string
		value string
	}{
		{"Verify", table.Verify},
		{"PrePush", table.PrePush},
		{"Ledger", table.Ledger},
	} {
		if field.value == "" {
			t.Errorf("%s is empty", field.name)
		}
	}
	for _, entry := range []string{table.Verify, table.PrePush} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry)))
		if err != nil {
			t.Errorf("the gate's entry point %s does not exist: %v", entry, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("the gate's entry point %s is a directory", entry)
		}
	}
	if path.Base(table.Ledger) != table.Ledger {
		t.Errorf("Ledger = %q, want a bare file name written inside the git dir", table.Ledger)
	}
	if table.LedgerKeep <= 0 {
		t.Errorf("LedgerKeep = %d, want a positive number of entries", table.LedgerKeep)
	}
	for _, file := range gitList(t, root, "ls-files") {
		if path.Base(file) == table.Ledger {
			t.Errorf("%s is committed; the ledger is a local record of this clone's runs", file)
		}
	}
}

// TestVerifyEntryPointRunsTheGate pins the one thing the entry point still owns after the
// step list moved to Go: it starts the gate. A shim that printed something else, or that
// stopped reaching `verify`, would leave every document and continuous integration calling
// a name that verifies nothing.
func TestVerifyEntryPointRunsTheGate(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Gate().Verify)))
	if err != nil {
		t.Fatalf("read %s: %v", Gate().Verify, err)
	}
	shim := string(content)
	if !strings.Contains(shim, "buildtool.sh") || !strings.Contains(shim, "verify") {
		t.Errorf("%s does not start the gate's verify command:\n%s", Gate().Verify, shim)
	}
	// The step list is not shell any more: a rule written back into the entry point is the
	// regression this pins, because a rule in shell is covered by no test of its own.
	for _, forbidden := range []string{"go test", "go vet", "gofmt", "coverage"} {
		if strings.Contains(shim, forbidden) {
			t.Errorf("%s holds the step %q; the gate's steps live in cmd/buildtool verify", Gate().Verify, forbidden)
		}
	}
}
