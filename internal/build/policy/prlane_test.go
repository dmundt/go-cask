package policy

import (
	"path"
	"strings"
	"testing"
)

// TestPRLaneTableIsWellFormed pins the server-side lane's layout: the ref namespace and
// the prefix built from it agree, the window is a positive number of minutes, the
// override variables are spelled as environment variables, and the local record is a
// bare file name inside the git directory.
func TestPRLaneTableIsWellFormed(t *testing.T) {
	t.Parallel()

	table := PRLane()
	for _, field := range []struct {
		name  string
		value string
	}{
		{"Namespace", table.Namespace},
		{"RefPrefix", table.RefPrefix},
		{"StaleEnv", table.StaleEnv},
		{"RepoEnv", table.RepoEnv},
		{"GhEnv", table.GhEnv},
		{"RecordFile", table.RecordFile},
	} {
		if field.value == "" {
			t.Errorf("%s is empty", field.name)
		}
	}
	if want := "refs/" + table.Namespace + "/"; table.RefPrefix != want {
		t.Errorf("RefPrefix = %q, want %q", table.RefPrefix, want)
	}
	if path.Base(table.RecordFile) != table.RecordFile {
		t.Errorf("RecordFile = %q, want a bare file name written inside the git dir", table.RecordFile)
	}
	for _, name := range []string{table.StaleEnv, table.RepoEnv, table.GhEnv} {
		if name != strings.ToUpper(name) {
			t.Errorf("the override %q is not spelled as an environment variable", name)
		}
	}
	if table.StaleMinutes <= 0 {
		t.Errorf("the claim window is %d minutes, want a positive number", table.StaleMinutes)
	}
	if table.PullRequestLimit <= 0 {
		t.Errorf("the pull-request limit is %d, want a positive number", table.PullRequestLimit)
	}
	if len(table.GhCandidates) == 0 {
		t.Error("no GitHub CLI candidate is named; the lane cannot be reached without one")
	}
	// The window's default is quoted in prose an operator relies on (AGENTS.md, "The
	// pull request is the lane"), so it is pinned rather than merely validated. So is
	// the ref prefix: it is the server-side record every clone reads, and the operations
	// spec spells it out.
	if table.StaleMinutes != 90 {
		t.Errorf("the claim window is %d minutes, want the documented 90", table.StaleMinutes)
	}
	if table.RefPrefix != "refs/lane/" {
		t.Errorf("RefPrefix = %q, want the documented refs/lane/", table.RefPrefix)
	}
}

// TestPRLaneRefIsNotABranch pins that the coordination ref stays outside the branch
// namespace docs/specs/branch-naming.md §2 governs: the lane is created, read and
// deleted through the refs API, never pushed to and never fetched as a branch, so its
// namespace must not be one git resolves a branch or a tag under.
func TestPRLaneRefIsNotABranch(t *testing.T) {
	t.Parallel()

	namespace := PRLane().Namespace
	for _, reserved := range []string{"heads", "tags", "remotes", "notes"} {
		if namespace == reserved {
			t.Errorf("the lane's namespace is %q, which is a branch namespace", namespace)
		}
	}
	if !strings.HasPrefix(PRLane().RefPrefix, "refs/") {
		t.Errorf("RefPrefix = %q, want a ref under refs/", PRLane().RefPrefix)
	}
}

// TestPRLaneRecordIsNeverCommitted pins that this worktree's record of the lanes it
// claimed is a local record: it is written inside the git directory, which is not
// tracked, so no committed file may carry its name. A tracked record would make one
// checkout's claims another's.
func TestPRLaneRecordIsNeverCommitted(t *testing.T) {
	t.Parallel()

	record := PRLane().RecordFile
	for _, file := range gitList(t, repoRoot(t), "ls-files") {
		if path.Base(file) == record {
			t.Errorf("%s is committed; the lane record is a local record", file)
		}
	}
}
