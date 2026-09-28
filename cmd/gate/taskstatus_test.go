package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/taskstate"
)

// TestParseWorktrees pins the porcelain reader: one block per registration, `worktree <path>`
// first, the branch line when the worktree is not detached, and a blank line between blocks.
// A detached registration names no branch, which is what keeps a bare worktree out of the
// report rather than attributing it to whatever branch happens to share a directory.
func TestParseWorktrees(t *testing.T) {
	t.Parallel()

	listing := strings.Join([]string{
		"worktree /repo",
		"HEAD 1111111111111111111111111111111111111111",
		"branch refs/heads/main",
		"",
		"worktree /repo/.worktrees/wt-a",
		"HEAD 2222222222222222222222222222222222222222",
		"branch refs/heads/chore/9-a",
		"locked",
		"",
		"worktree /repo/.worktrees/wt-detached",
		"HEAD 3333333333333333333333333333333333333333",
		"detached",
		"",
	}, "\n")

	got := parseWorktrees(listing)
	want := []worktreeEntry{
		{Path: "/repo", Branch: "main"},
		{Path: "/repo/.worktrees/wt-a", Branch: "chore/9-a"},
		{Path: "/repo/.worktrees/wt-detached"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseWorktrees returned %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestParseWorktreesIsEmptyForEmptyInput keeps the reader from inventing a registration for the
// empty listing a repository with no worktrees prints.
func TestParseWorktreesIsEmptyForEmptyInput(t *testing.T) {
	t.Parallel()

	if got := parseWorktrees(""); len(got) != 0 {
		t.Errorf("parseWorktrees(\"\") = %+v, want no entries", got)
	}
}

// TestParsePullRequests pins the listing reader: a branch keeps the first pull request that
// names it, an entry without a branch or a number is skipped rather than reported under an empty
// name, and a malformed listing is an error rather than an empty result — reading nothing is not
// the same as there being nothing.
func TestParsePullRequests(t *testing.T) {
	t.Parallel()

	raw := `[
		{"headRefName":"ci/436-gate","number":448,"state":"OPEN"},
		{"headRefName":"ci/436-gate","number":447,"state":"CLOSED"},
		{"headRefName":"docs/434-ceilings","number":445,"state":"MERGED"},
		{"headRefName":"","number":1,"state":"OPEN"},
		{"headRefName":"orphan","number":0,"state":"OPEN"}
	]`
	got, err := parsePullRequests(raw)
	if err != nil {
		t.Fatalf("parsePullRequests: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d pull requests, want 2: %+v", len(got), got)
	}
	if request := got["ci/436-gate"]; request.Number != 448 || request.State != taskstate.Open {
		t.Errorf("ci/436-gate = %+v, want the newest pull request #448 OPEN", request)
	}
	if request := got["docs/434-ceilings"]; request.State != taskstate.Merged {
		t.Errorf("docs/434-ceilings = %+v, want MERGED", request)
	}

	if _, err := parsePullRequests("not json"); err == nil {
		t.Error("parsePullRequests on a malformed listing = nil error, want one")
	}
}

// TestRenderTaskFindingsSaysWhenThereIsNothing pins the one line that makes the report usable:
// a session has to be able to tell "nothing carries unlanded work" from "the command printed
// nothing".
func TestRenderTaskFindingsSaysWhenThereIsNothing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	renderTaskFindings(&out, nil)
	if !strings.Contains(out.String(), "none:") {
		t.Errorf("the empty report printed %q, want it to say there is nothing to act on", out.String())
	}
}

// TestClipKeepsColumnsAligned pins the one rendering decision that keeps the table readable: a
// long branch name is shortened rather than allowed to push every column after it out of line.
func TestClipKeepsColumnsAligned(t *testing.T) {
	t.Parallel()

	if got := clip("short", 10); got != "short" {
		t.Errorf("clip(short) = %q, want it unchanged", got)
	}
	long := strings.Repeat("x", 50)
	got := clip(long, 44)
	if len([]rune(got)) != 44 {
		t.Errorf("clip(50 chars to 44) returned %d runes, want 44", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clip(%q) = %q, want it to mark the truncation", long, got)
	}
}
