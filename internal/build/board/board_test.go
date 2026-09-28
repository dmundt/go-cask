package board

import (
	"strings"
	"testing"
	"time"
)

// issueListing is `gh issue list --state open --json number,title,labels,url` as gh prints
// it, including the label shape that is an object list rather than a string list.
const issueListing = `[
  {"number": 438, "title": "make the coordinator board mechanical", "url": "https://example.invalid/438",
   "labels": [{"name": "chore"}, {"name": "agent-workflow"}]},
  {"number": 461, "title": "the worktree removal is not registered", "url": "https://example.invalid/461",
   "labels": []}
]`

// pullListing is `gh pr list --state open --json ...`: one armed green pull request, one
// draft with no auto-merge, and one conflicting pull request — the three states a board has
// to tell apart.
const pullListing = `[
  {"number": 469, "title": "report unlanded branches", "headRefName": "chore/lane/454",
   "headRefOid": "aaaa1111bbbb2222cccc3333dddd4444eeee5555", "isDraft": false,
   "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
   "autoMergeRequest": {"enabledAt": "2026-09-27T19:00:00Z"}},
  {"number": 470, "title": "the board", "headRefName": "chore/lane/438",
   "headRefOid": "1111aaaa2222bbbb3333cccc4444dddd5555eeee", "isDraft": true,
   "mergeable": "MERGEABLE", "mergeStateStatus": "DRAFT", "autoMergeRequest": null},
  {"number": 471, "title": "collisions", "headRefName": "chore/lane/440",
   "headRefOid": "9999888877776666555544443333222211110000", "isDraft": false,
   "mergeable": "CONFLICTING", "mergeStateStatus": "DIRTY",
   "autoMergeRequest": {"enabledAt": "2026-09-27T20:00:00Z"}}
]`

// worktreeListing is `git worktree list --porcelain` with a primary checkout, a lane's
// worktree and a bare mirror that names no branch.
const worktreeListing = `worktree D:/src/go-cask
HEAD e25e7ef4aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
branch refs/heads/main

worktree D:/src/go-cask/.worktrees/wt-438
HEAD 1111aaaa2222bbbb3333cccc4444dddd5555eeee
branch refs/heads/chore/438-buildtool-board

worktree D:/src/mirror.git
HEAD 0000000000000000000000000000000000000000
bare

worktree D:/src/go-cask/.worktrees/wt-454
HEAD aaaa1111bbbb2222cccc3333dddd4444eeee5555
detached
`

// laneDiff is the shape `git diff --unified=0` prints for a change to the board engine, a
// changed CHANGELOG bullet and a changed docs row.
const laneDiff = `diff --git a/CHANGELOG.md b/CHANGELOG.md
index 1111111..2222222 100644
--- a/CHANGELOG.md
+++ b/CHANGELOG.md
@@ -30,0 +31,3 @@
+### Added
+
+- the board, collisions and verify-landing verbs

diff --git a/internal/build/board/board.go b/internal/build/board/board.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/internal/build/board/board.go
@@ -0,0 +1,2 @@
+package board
+
`

// TestParseIssues pins the issue listing and its label shape.
func TestParseIssues(t *testing.T) {
	issues, err := ParseIssues(issueListing)
	if err != nil {
		t.Fatalf("ParseIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if issues[0].Number != 438 || issues[0].Title == "" || issues[0].URL == "" {
		t.Errorf("issue 438 = %+v, want its number, title and url", issues[0])
	}
	if got := strings.Join(issues[0].Labels, ","); got != "chore,agent-workflow" {
		t.Errorf("labels = %q, want the two names", got)
	}
	if len(issues[1].Labels) != 0 {
		t.Errorf("issue 461 labels = %v, want none", issues[1].Labels)
	}
}

// TestParseIssuesRejectsGarbage pins that a listing this build cannot read is an error
// rather than an empty board: "nothing is open" and "I could not read the list" are
// different reports, and only one of them means the board is empty.
func TestParseIssuesRejectsGarbage(t *testing.T) {
	if _, err := ParseIssues("not json"); err == nil {
		t.Fatal("ParseIssues of a non-listing = nil error, want a failure")
	}
}

// TestParsePullRequests pins the three states the board must tell apart: an armed pull
// request, a draft, and a conflict. A board that omits draft and autoMergeRequest is not a
// board (docs/specs/coordination.md §2).
func TestParsePullRequests(t *testing.T) {
	pulls, err := ParsePullRequests(pullListing)
	if err != nil {
		t.Fatalf("ParsePullRequests: %v", err)
	}
	if len(pulls) != 3 {
		t.Fatalf("got %d pull requests, want 3", len(pulls))
	}
	armed := pulls[0]
	if armed.Number != 469 || armed.Branch != "chore/lane/454" {
		t.Errorf("first pull request = %+v, want #469 on chore/lane/454", armed)
	}
	if !armed.AutoMerge || armed.Draft {
		t.Errorf("#469 = auto-merge %t, draft %t; want armed and not a draft", armed.AutoMerge, armed.Draft)
	}
	if armed.HeadOID != "aaaa1111bbbb2222cccc3333dddd4444eeee5555" {
		t.Errorf("#469 head = %q, want the head commit", armed.HeadOID)
	}
	draft := pulls[1]
	if !draft.Draft || draft.AutoMerge {
		t.Errorf("#470 = draft %t, auto-merge %t; want a draft with no auto-merge", draft.Draft, draft.AutoMerge)
	}
	conflict := pulls[2]
	if conflict.MergeState != MergeDirty || conflict.Mergeable != "CONFLICTING" {
		t.Errorf("#471 = %q/%q, want the rebuild signal", conflict.MergeState, conflict.Mergeable)
	}
}

// TestParsePullRequestsDropsUnjoinableEntries pins the join's precondition: an entry with no
// number or no head branch names no lane, so it is dropped rather than attached to a lane at
// random.
func TestParsePullRequestsDropsUnjoinableEntries(t *testing.T) {
	pulls, err := ParsePullRequests(`[{"number": 0, "headRefName": "x"}, {"number": 12, "headRefName": ""}]`)
	if err != nil {
		t.Fatalf("ParsePullRequests: %v", err)
	}
	if len(pulls) != 0 {
		t.Fatalf("got %d pull requests, want the unjoinable entries dropped", len(pulls))
	}
	if _, err := ParsePullRequests("nope"); err == nil {
		t.Fatal("ParsePullRequests of a non-listing = nil error, want a failure")
	}
}

// TestParseWorktrees pins the porcelain listing, including the two registration kinds that
// name no branch.
func TestParseWorktrees(t *testing.T) {
	entries := ParseWorktrees(worktreeListing)
	if len(entries) != 4 {
		t.Fatalf("got %d registrations, want 4: %+v", len(entries), entries)
	}
	if entries[0].Path != "D:/src/go-cask" || entries[0].Branch != "main" {
		t.Errorf("primary = %+v, want the main checkout", entries[0])
	}
	if entries[1].Branch != "chore/438-buildtool-board" {
		t.Errorf("lane worktree branch = %q, want the lane's branch", entries[1].Branch)
	}
	if !entries[2].Bare || entries[2].Branch != "" {
		t.Errorf("mirror = %+v, want a bare registration naming no branch", entries[2])
	}
	if !entries[3].Detached || entries[3].Branch != "" {
		t.Errorf("detached = %+v, want a detached registration naming no branch", entries[3])
	}

	held := WorktreesOf(entries)
	if held["main"].Path != "D:/src/go-cask" {
		t.Errorf("join for main = %+v, want the primary checkout", held["main"])
	}
	if _, found := held[""]; found {
		t.Error("a registration with no branch joined the map")
	}
}

// TestParseClaimRecord pins the local record: one issue per line, blank lines and stray
// whitespace ignored, order kept.
func TestParseClaimRecord(t *testing.T) {
	got := ParseClaimRecord("438\n\n  440  \n")
	if strings.Join(got, ",") != "438,440" {
		t.Fatalf("record = %v, want 438 and 440", got)
	}
}

// TestParseDiff pins the new-side spans of a `--unified=0` diff: a counted hunk, a new file
// whose first hunk opens at line 1, and a change that occupies no line at all.
func TestParseDiff(t *testing.T) {
	ranges := ParseDiff(laneDiff)
	if got := ranges["CHANGELOG.md"]; len(got) != 1 || got[0] != [2]int{31, 33} {
		t.Errorf("CHANGELOG.md spans = %v, want one span 31-33", got)
	}
	if got := ranges["internal/build/board/board.go"]; len(got) != 1 || got[0] != [2]int{1, 2} {
		t.Errorf("board.go spans = %v, want one span 1-2", got)
	}
	if len(ranges) != 2 {
		t.Errorf("parsed %d files, want 2", len(ranges))
	}

	pure := ParseDiff("--- a/x.go\n+++ b/x.go\n@@ -5,0 +6,0 @@\n-old\n+new\n")
	if spans, found := pure["x.go"]; !found || len(spans) != 1 || spans[0] != [2]int{} {
		t.Errorf("a hunk that adds nothing = %v, want the file touched with an empty span", spans)
	}

	removed := ParseDiff("--- a/gone.go\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-a\n-b\n-c\n")
	if _, found := removed["gone.go"]; found {
		t.Error("the deleted side of a removal was parsed as a path")
	}
}

// TestParseClaimedAndMinutes pins the claim moment and its age, including a record stamped
// in the future: a clock that moved must not report a negative age.
func TestParseClaimedAndMinutes(t *testing.T) {
	at, ok := ParseClaimed("2026-09-27T19:34:59Z")
	if !ok {
		t.Fatal("ParseClaimed of an RFC 3339 stamp = not ok")
	}
	if got := at.UTC().Format(time.RFC3339); got != "2026-09-27T19:34:59Z" {
		t.Errorf("claimed = %s, want the stamp back", got)
	}
	if _, ok := ParseClaimed("yesterday"); ok {
		t.Error("ParseClaimed of prose = ok, want not ok")
	}
	if got := MinutesBetween(at, at.Add(95*time.Minute)); got != 95 {
		t.Errorf("age = %d minutes, want 95", got)
	}
	if got := MinutesBetween(at, at.Add(-5*time.Minute)); got != 0 {
		t.Errorf("age of a record stamped in the future = %d, want 0", got)
	}
}

// TestMentionedAndPaths pins the two literal readings: whether a body cites an issue the
// landing way, and whether a changed path is spelled the way git prints it.
func TestMentionedAndPaths(t *testing.T) {
	if !Mentioned("Closes #438\n", "438") {
		t.Error("Closes #438 was not read as a citation")
	}
	if !Mentioned("closes #438", "438") {
		t.Error("a lowercase citation was not read")
	}
	if Mentioned("Refs #438", "438") {
		t.Error("Refs #438 was read as a landing citation")
	}
	if Mentioned("Closes #4380", "438") {
		t.Error("Closes #4380 was read as a citation of #438")
	}
	if got := PathsIn("a/b.go\r\n\r\nc.md\n"); strings.Join(got, ",") != "a/b.go,c.md" {
		t.Errorf("paths = %v, want the two paths without the carriage return", got)
	}
	if got := CanonicalPath("  docs/index.md "); got != "docs/index.md" {
		t.Errorf("CanonicalPath = %q, want the trimmed path", got)
	}
}
