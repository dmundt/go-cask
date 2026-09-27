package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/internal/build/core/gate"
	"github.com/dmundt/go-cask/internal/build/core/lane"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// hookRepo creates a git repository with one commit and returns its toplevel. It is a
// plain repository rather than this checkout, because the hook's rule is about the
// repository being pushed.
func hookRepo(t *testing.T) (toplevel string, head string) {
	t.Helper()
	toplevel = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = toplevel
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(toplevel, "file.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture file: %v", err)
	}
	run("add", "file.txt")
	run("commit", "-qm", "seed")
	return toplevel, run("rev-parse", "HEAD")
}

// ledgerPath returns the shared ledger's path for a repository.
func ledgerPath(t *testing.T, toplevel string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = toplevel
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-common-dir: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), policy.Gate().Ledger)
}

// TestPrePushRequiresAGreenStampForThisCommit pins the one hard rule: the pushed commit
// must have its own entry, and another worktree's entry must not authorise it.
func TestPrePushRequiresAGreenStampForThisCommit(t *testing.T) {
	repo, head := hookRepo(t)
	ledger := ledgerPath(t, repo)

	// Another commit's entry, which is what a single-slot stamp used to look like from
	// this branch's point of view.
	other := strings.Repeat("b", 40)
	if err := os.WriteFile(ledger, []byte(gate.Line(other, "docs", time.Unix(0, 0))+"\n"), 0o644); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}

	var out, errOut bytes.Buffer
	err := runPrePush([]string{"--repo", repo}, &out, &errOut)
	if err == nil {
		t.Fatal("the hook authorised a commit with no green gate")
	}
	if !strings.Contains(errOut.String(), "no green gate for "+head) {
		t.Errorf("the refusal %q does not name the commit and the remedy", errOut.String())
	}
	if !strings.Contains(errOut.String(), policy.Gate().Verify) {
		t.Errorf("the refusal %q does not point at the gate", errOut.String())
	}

	// The same ledger with this commit's entry authorises the push, and the other
	// worktree's entry survives.
	updated := gate.Append(readFileOrEmpty(ledger), head, "full", time.Unix(1, 0), policy.Gate().LedgerKeep)
	if err := os.WriteFile(ledger, []byte(updated), 0o644); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if err := runPrePush([]string{"--repo", repo}, &out, &errOut); err != nil {
		t.Fatalf("the hook refused a verified commit: %v\n%s", err, errOut.String())
	}
	if !strings.Contains(readFileOrEmpty(ledger), other) {
		t.Error("the pushed commit's entry replaced another worktree's")
	}
}

// TestPrePushReportsTheAdvisorySlotWithoutRefusing pins the second rule's shape: the
// local slot is reported, never required. A per-clone file must not be able to stop a
// landing the server would have serialized anyway.
func TestPrePushReportsTheAdvisorySlotWithoutRefusing(t *testing.T) {
	repo, head := hookRepo(t)
	ledger := ledgerPath(t, repo)
	if err := os.WriteFile(ledger, []byte(gate.Line(head, "full", time.Unix(0, 0))+"\n"), 0o644); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}

	var out, errOut bytes.Buffer
	if err := runPrePush([]string{"--repo", repo}, &out, &errOut); err != nil {
		t.Fatalf("the hook refused a verified commit: %v", err)
	}
	if !strings.Contains(errOut.String(), "advisory slot") {
		t.Errorf("the hook did not report the local slot:\n%s", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("the hook wrote %q to stdout; a hook's reports belong on stderr", out.String())
	}

	// Holding the slot is not required, and holding it silences the note.
	slot, err := resolveLandLane(repo)
	if err != nil {
		t.Fatalf("resolving the slot: %v", err)
	}
	if _, _, status := runSlot(t, slot, "acquire", "hook"); status != 0 {
		t.Fatalf("could not take the slot: %d", status)
	}
	errOut.Reset()
	if err := runPrePush([]string{"--repo", repo}, &out, &errOut); err != nil {
		t.Fatalf("the hook refused a verified commit with the slot held: %v", err)
	}
	if strings.Contains(errOut.String(), "advisory slot") {
		t.Errorf("the hook reported the slot although this worktree holds it:\n%s", errOut.String())
	}
}

// TestPrePushHookIsAShim pins that the installed hook holds no rule of its own: it
// starts the command, and it names the repository being pushed so the tool reads that
// repository's ledger rather than its own.
func TestPrePushHookIsAShim(t *testing.T) {
	hook := readFileOrEmpty(policy.Gate().PrePush)
	if hook == "" {
		t.Skipf("%s is not present in this checkout", policy.Gate().PrePush)
	}
	for _, want := range []string{"buildtool pre-push", "--repo"} {
		if !strings.Contains(hook, want) {
			t.Errorf("%s does not carry %q, so the hook is not the shim the rules live behind",
				policy.Gate().PrePush, want)
		}
	}
	// The rule the shim must not keep: nothing in it may read the ledger itself.
	if strings.Contains(hook, policy.Gate().Ledger) {
		t.Errorf("%s reads the ledger in shell; that rule belongs to the command", policy.Gate().PrePush)
	}
}

// TestPrePushReportsTheEvictedHolderToTheSlot pins that a pre-push note is not the only
// place the slot's state shows up: the eviction record is what tells an evicted holder
// apart from one that never held the slot.
func TestPrePushSeesTheSameSlotTheLaneReports(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)
	other := slotFor(t, root, "wt-other", 90)

	if _, _, status := runSlot(t, mine, "acquire", "hook"); status != 0 {
		t.Fatalf("acquire failed: %d", status)
	}
	if got := lane.SlotStatus(other.read(), other.who, other.mine()); got != lane.Other {
		t.Errorf("another worktree sees status %v, want Other", got)
	}
}
