package claim

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIssueOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ref   string
		issue string
		found bool
	}{
		{ref: "refs/lane/389", issue: "389", found: true},
		{ref: "refs/lane/1", issue: "1", found: true},
		{ref: "refs/lane/", found: false},
		{ref: "refs/lane/38a", found: false},
		{ref: "refs/lane/38/9", found: false},
		{ref: "refs/heads/main", found: false},
		{ref: "", found: false},
	}
	for _, tc := range cases {
		issue, found := IssueOf(tc.ref, "refs/lane/")
		if found != tc.found || issue != tc.issue {
			t.Errorf("IssueOf(%q) = %q, %v; want %q, %v", tc.ref, issue, found, tc.issue, tc.found)
		}
	}
}

// TestBranchNamesIssue pins the branch-matching rule: a branch carries its issue as a whole
// segment, so a bigger number that merely starts with it is a different issue.
func TestBranchNamesIssue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		branch string
		issue  string
		want   bool
	}{
		{branch: "feat/389-lane", issue: "389", want: true},
		{branch: "fix/389", issue: "389", want: true},
		{branch: "389-x", issue: "389", want: true},
		{branch: "feat/3890-other", issue: "389", want: false},
		{branch: "feat/1389-x", issue: "389", want: false},
		{branch: "389", issue: "389", want: true},
		{branch: "main", issue: "389", want: false},
		{branch: "", issue: "389", want: false},
	}
	for _, tc := range cases {
		if got := BranchNamesIssue(tc.branch, tc.issue); got != tc.want {
			t.Errorf("BranchNamesIssue(%q, %q) = %v, want %v", tc.branch, tc.issue, got, tc.want)
		}
	}
}

func TestClaimMessage(t *testing.T) {
	t.Parallel()

	message := ClaimMessage("chore/lane/389", "wt-389")
	if message != "branch=chore/lane/389 worktree=wt-389" {
		t.Errorf("ClaimMessage = %q", message)
	}
	// Path-free by construction: the two toolchains spell one directory differently, so a
	// path in the record would make two clones' claims indistinguishable.
	if strings.Contains(message, ":/") || strings.HasPrefix(strings.TrimPrefix(message, "branch="), "/") {
		t.Errorf("ClaimMessage = %q, which carries a path", message)
	}
}

// TestDecideLane pins the protocol order: unreadable decides nothing, an open pull request
// holds the lane whatever the claim's age, and without one the window decides.
func TestDecideLane(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	window := 90 * time.Minute
	pr := &PullRequest{Number: 42, Branch: "chore/lane/389"}
	fresh := LaneInput{Present: true, Readable: true, Claimer: "branch=x worktree=y", Claimed: now.Add(-10 * time.Minute), AgeKnown: true}
	old := LaneInput{Present: true, Readable: true, Claimer: "branch=x worktree=y", Claimed: now.Add(-200 * time.Minute), AgeKnown: true}
	held := LaneInput{Present: true, Readable: true, PullRequest: pr, Claimer: "branch=x worktree=y", Claimed: now.Add(-500 * time.Minute), AgeKnown: true}

	cases := []struct {
		name         string
		in           LaneInput
		want         State
		wantAge      time.Duration
		wantAgeKnown bool
	}{
		{name: "no ref is free", in: LaneInput{}, want: Free},
		{name: "an unreadable record decides nothing", in: LaneInput{Present: true}, want: Unreadable},
		{name: "a fresh claim is inside its window", in: fresh, want: Claiming, wantAge: 10 * time.Minute, wantAgeKnown: true},
		{name: "a claim past its window is stale", in: old, want: Stale, wantAge: 200 * time.Minute, wantAgeKnown: true},
		{
			// Held is decided by the pull request, not by the claim's age, so no age is
			// reported even when the record carries one.
			name: "an open pull request holds the lane",
			in:   held, want: Held,
		},
		{
			name: "an unreadable moment leaves the claim inside its window",
			in:   LaneInput{Present: true, Readable: true, Claimer: "x", AgeKnown: false},
			want: Claiming,
		},
		{
			name:         "exactly at the window is stale",
			in:           LaneInput{Present: true, Readable: true, AgeKnown: true, Claimed: now.Add(-window)},
			want:         Stale,
			wantAge:      window,
			wantAgeKnown: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict := DecideLane(tc.in, window, now)
			if verdict.State != tc.want {
				t.Errorf("DecideLane = %v, want %v", verdict.State, tc.want)
			}
			if tc.want == Held && (verdict.PullRequest == nil || verdict.PullRequest.Number != pr.Number) {
				t.Errorf("the held verdict lost its pull request: %+v", verdict.PullRequest)
			}
			if verdict.Age != tc.wantAge || verdict.AgeKnown != tc.wantAgeKnown {
				t.Errorf("DecideLane reports age %v (known %v), want %v (known %v)",
					verdict.Age, verdict.AgeKnown, tc.wantAge, tc.wantAgeKnown)
			}
		})
	}
}

func TestStateString(t *testing.T) {
	t.Parallel()

	for state, want := range map[State]string{
		Free: "free", Held: "held", Claiming: "claiming", Stale: "stale", Unreadable: "unreadable",
	} {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
	if got := State(99).String(); got != "unknown" {
		t.Errorf("an unknown state renders as %q", got)
	}
}

// TestStatusJSON pins the shape the command promises: field names and order, nulls for a
// missing pull request and an unknown age, and an empty array rather than `null` when there
// are no lanes.
func TestStatusJSON(t *testing.T) {
	t.Parallel()

	number, age := 42, 17
	statuses := []Status{
		{Issue: "389", Ref: "refs/lane/389", Object: "abc", PullRequest: &number, Branch: "chore/lane/389",
			Claimant: "branch=x worktree=y", AgeMinutes: &age, Mine: true, State: "held"},
		{Issue: "390", Ref: "refs/lane/390", Object: "def", Claimant: "", State: "stale"},
	}
	rendered, err := StatusJSON(statuses)
	if err != nil {
		t.Fatalf("StatusJSON: %v", err)
	}
	if !strings.HasSuffix(rendered, "\n") {
		t.Errorf("StatusJSON does not end in a newline: %q", rendered)
	}

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
		t.Fatalf("the rendered JSON does not parse: %v\n%s", err, rendered)
	}
	if len(decoded) != 2 {
		t.Fatalf("rendered %d lanes, want 2", len(decoded))
	}
	held := decoded[0]
	for _, key := range []string{"issue", "ref", "object", "pull_request", "branch", "claimant", "age_minutes", "mine", "state"} {
		if _, found := held[key]; !found {
			t.Errorf("the held lane is missing %q: %s", key, rendered)
		}
	}
	if held["pull_request"] != float64(number) || held["age_minutes"] != float64(age) || held["mine"] != true {
		t.Errorf("the held lane rendered as %v", held)
	}
	// A lane with no pull request and no readable age renders both as null, not as zero.
	stale := decoded[1]
	if stale["pull_request"] != nil || stale["age_minutes"] != nil {
		t.Errorf("the stale lane rendered %v, want nulls", stale)
	}

	empty, err := StatusJSON(nil)
	if err != nil {
		t.Fatalf("StatusJSON(nil): %v", err)
	}
	if strings.TrimSpace(empty) != "[]" {
		t.Errorf("StatusJSON(nil) = %q, want an empty array", empty)
	}
}
