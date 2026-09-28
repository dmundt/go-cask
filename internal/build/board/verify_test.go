package board

import (
	"strings"
	"testing"
)

// landedVerdict is a landing that holds every line of docs/specs/coordination.md §6, used as
// the base each case departs from: a squash merge citing its issue, the issue closed, every
// file under the declared scope, a user-visible change with both documents, a signed head
// with a published receipt, and a released lane with no worktree left.
func landedVerdict() Verdict {
	return Verdict{
		Subject:          "PR #469",
		Merged:           true,
		Squash:           true,
		Body:             "Closes #454\n",
		Issue:            "454",
		IssueState:       "CLOSED",
		Files:            []string{"internal/build/board/board.go", "CHANGELOG.md", "docs/index.md"},
		Scope:            []string{"internal/build/board", "CHANGELOG.md", "docs/index.md"},
		UserVisible:      true,
		Changelog:        true,
		VersionBumped:    true,
		Signed:           true,
		ReceiptPublished: true,
		Head:             "aaaa1111bbbb2222cccc3333dddd4444eeee5555",
		LaneReleased:     true,
		WorktreeGone:     true,
	}
}

// TestChecksAllHold pins the six lines and their order, which is §6's order, so a report read
// beside the spec lines up line for line.
func TestChecksAllHold(t *testing.T) {
	verification := Checks(landedVerdict())
	if !verification.Verified {
		t.Fatalf("Verified = false for a complete landing: %v", failedLines(verification))
	}
	if len(verification.Checks) != 6 {
		t.Fatalf("got %d checks, want the six §6 names", len(verification.Checks))
	}
	for _, check := range verification.Checks {
		if check.Evidence == "" {
			t.Errorf("check %q carries no evidence", check.Name)
		}
	}
	if len(failedLines(verification)) != 0 {
		t.Errorf("Failed = %v, want none", failedLines(verification))
	}
}

// TestChecksFailures pins each line failing from its own authority, and that the evidence
// says which half of a two-part line is missing.
func TestChecksFailures(t *testing.T) {
	cases := []struct {
		name     string
		change   func(*Verdict)
		wantLine string
		evidence string
	}{
		{
			name:     "not merged",
			change:   func(v *Verdict) { v.Merged = false },
			wantLine: "the pull request merged as a squash and cites the issue",
			evidence: "does not report the pull request as merged",
		},
		{
			name:     "merged but not a squash",
			change:   func(v *Verdict) { v.Squash = false },
			wantLine: "the pull request merged as a squash and cites the issue",
			evidence: "not a squash",
		},
		{
			name:     "the body cites no issue",
			change:   func(v *Verdict) { v.Body = "Refs #454" },
			wantLine: "the pull request merged as a squash and cites the issue",
			evidence: "does not cite Closes #454",
		},
		{
			name:     "the issue is still open",
			change:   func(v *Verdict) { v.IssueState = "OPEN" },
			wantLine: "the issue is CLOSED",
			evidence: "issue #454 is OPEN",
		},
		{
			name:     "a drive-by file",
			change:   func(v *Verdict) { v.Files = append(v.Files, "cas/backend/fs/fs.go") },
			wantLine: "the merged file list stays within the issue's scope",
			evidence: "cas/backend/fs/fs.go",
		},
		{
			name:     "a user-visible change with no changelog bullet",
			change:   func(v *Verdict) { v.Changelog = false },
			wantLine: "the change carried the docs it needed",
			evidence: "no CHANGELOG bullet",
		},
		{
			name:     "a user-visible change with no version bump",
			change:   func(v *Verdict) { v.VersionBumped = false },
			wantLine: "the change carried the docs it needed",
			evidence: "no frontmatter bump",
		},
		{
			name: "a docs-only change that carried a changelog bullet anyway",
			change: func(v *Verdict) {
				v.UserVisible = false
			},
			wantLine: "the change carried the docs it needed",
			evidence: "not user-visible, but it carried",
		},
		{
			name:     "an unsigned head",
			change:   func(v *Verdict) { v.Signed = false },
			wantLine: "the head was signed and its gate receipt was published",
			evidence: "is not a verified signature",
		},
		{
			name:     "no published receipt",
			change:   func(v *Verdict) { v.ReceiptPublished = false },
			wantLine: "the head was signed and its gate receipt was published",
			evidence: "refs/gate/aaaa1111bbbb is not on origin",
		},
		{
			name:     "both halves of the evidence missing",
			change:   func(v *Verdict) { v.Signed = false; v.ReceiptPublished = false },
			wantLine: "the head was signed and its gate receipt was published",
			evidence: "is not a verified signature and refs/gate/aaaa1111bbbb is not on origin",
		},
		{
			name:     "the lane is still held",
			change:   func(v *Verdict) { v.LaneReleased = false },
			wantLine: "the lane was released and its worktree is gone",
			evidence: "the claim ref still exists",
		},
		{
			name:     "the worktree survives",
			change:   func(v *Verdict) { v.WorktreeGone = false },
			wantLine: "the lane was released and its worktree is gone",
			evidence: "a worktree still holds the lane",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := landedVerdict()
			tc.change(&verdict)
			verification := Checks(verdict)
			if verification.Verified {
				t.Fatal("Verified = true, want a failed line")
			}
			if failed := strings.Join(failedLines(verification), "; "); !strings.Contains(failed, tc.wantLine) {
				t.Errorf("Failed = %q, want %q", failed, tc.wantLine)
			}
			if !evidenceSays(verification, tc.wantLine, tc.evidence) {
				t.Errorf("no evidence for %q contains %q:\n%+v", tc.wantLine, tc.evidence, verification.Checks)
			}
		})
	}
}

// evidenceSays reports whether the named check's evidence contains a phrase.
func evidenceSays(verification Verification, name, phrase string) bool {
	for _, check := range verification.Checks {
		if check.Name == name {
			return strings.Contains(check.Evidence, phrase)
		}
	}
	return false
}

// failedLines names the checks a verification found failing, in the report's order.
func failedLines(verification Verification) []string {
	var failed []string
	for _, check := range verification.Checks {
		if !check.OK {
			failed = append(failed, check.Name)
		}
	}
	return failed
}

// TestChecksDocsOnlyChange pins the empty side of §6's fourth line: a change a user cannot
// see carried neither document, and that is a line that holds.
func TestChecksDocsOnlyChange(t *testing.T) {
	verdict := landedVerdict()
	verdict.UserVisible = false
	verdict.Changelog = false
	verdict.VersionBumped = false
	verdict.Files = []string{"internal/build/board/board.go"}
	verdict.Scope = []string{"internal/build/board"}

	verification := Checks(verdict)
	if !verification.Verified {
		t.Fatalf("Verified = false for an internal-only change: %v", failedLines(verification))
	}
	if !evidenceSays(verification, "the change carried the docs it needed", "carried neither") {
		t.Errorf("evidence does not say the change needed neither document:\n%+v", verification.Checks)
	}
}

// TestChecksUnreadScope pins the honesty rule: a check that could not read its input fails
// and says so, rather than passing because it found nothing to complain about.
func TestChecksUnreadScope(t *testing.T) {
	verdict := landedVerdict()
	verdict.Scope = nil
	verification := Checks(verdict)
	if verification.Verified {
		t.Fatal("Verified = true with no scope read, want the line to fail")
	}
	if !evidenceSays(verification, "the merged file list stays within the issue's scope", "names no scope") {
		t.Errorf("evidence does not say the scope could not be read:\n%+v", verification.Checks)
	}
}

// TestChecksKeepNotes pins that what the caller could not read travels with the report.
func TestChecksKeepNotes(t *testing.T) {
	verdict := landedVerdict()
	verdict.Notes = []string{"gh pr view #469 failed"}
	verification := Checks(verdict)
	if len(verification.Notes) != 1 || !strings.Contains(verification.Notes[0], "failed") {
		t.Errorf("notes = %v, want the caller's caveat kept", verification.Notes)
	}
	if verification.Subject != "PR #469" {
		t.Errorf("subject = %q, want what was verified", verification.Subject)
	}
}

// TestOutside pins the scope reading: an entry covers its directory and everything under it,
// a trailing slash is irrelevant, and a file nothing covers is named.
func TestOutside(t *testing.T) {
	scope := []string{"internal/build/board/", "CHANGELOG.md", " "}
	files := []string{
		"internal/build/board/board.go",
		"internal/build/board/parse.go",
		"internal/build/lane/lane.go",
		"CHANGELOG.md",
		"CHANGELOG.md.bak",
	}
	outside := Outside(files, scope)
	if strings.Join(outside, ",") != "CHANGELOG.md.bak,internal/build/lane/lane.go" {
		t.Errorf("Outside = %v, want the two uncovered files", outside)
	}
	if got := Outside(nil, scope); len(got) != 0 {
		t.Errorf("Outside of nothing = %v, want none", got)
	}
	if got := Outside(files, nil); len(got) != len(files) {
		t.Errorf("Outside with no scope = %v, want every file named", got)
	}
}

// issueBody is issue #438's own body, whose `Home` line named a directory that does not
// exist — the stale premise this change corrects. It is the fixture for the scope reader for
// exactly that reason: a scope read from prose has to be read as written, not as intended.
const issueBody = `## Goal

The coordinator role is defined by its contract landing (see #437).

## Commands

- ` + "`buildtool board`" + ` - one view of every open issue with its lane state

## Home

` + "`internal/build/core/board`" + ` (the engine module: stdlib only, tested) plus thin
` + "`cmd/buildtool`" + ` verbs and the ` + "`docs/index.md`" + ` rows.

## Depends on

The contract landing.
`

// TestScopeIn pins the issue-side scope reader: it takes the paths an issue declares and
// leaves its prose behind. A body that declares nothing yields nothing, which is what makes
// the scope check fail honestly rather than pass.
func TestScopeIn(t *testing.T) {
	scope := ScopeIn(issueBody)
	joined := strings.Join(scope, ",")
	for _, want := range []string{"internal/build/core/board", "cmd/buildtool", "docs/index.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("scope = %v, want %q among the declared paths", scope, want)
		}
	}
	for _, prose := range []string{"437", "Goal", "The", "contract"} {
		for _, path := range scope {
			if path == prose {
				t.Errorf("scope carries the prose word %q", prose)
			}
		}
	}

	listed := ScopeIn("## Home\n\n- `internal/build/board/`\n- `cmd/buildtool/board.go`\n\n## Other\n\nprose\n")
	if strings.Join(listed, ",") != "internal/build/board,cmd/buildtool/board.go" {
		t.Errorf("scoped list = %v, want the two listed paths", listed)
	}

	if got := ScopeIn("## Goal\n\nno paths here\n"); len(got) != 0 {
		t.Errorf("ScopeIn of a body declaring nothing = %v, want none", got)
	}
	if got := ScopeIn(""); len(got) != 0 {
		t.Errorf("ScopeIn of an empty body = %v, want none", got)
	}
}
