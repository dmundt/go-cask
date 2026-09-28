package taskstate

import (
	"strings"
	"testing"
)

// TestFindings pins the rule the command exists for: which branches carry work nothing will
// land, and which are debris from a pull request that already did.
func TestFindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		branch   Branch
		wantKind Kind
		wantNone bool
		inDetail []string
	}{
		{
			name:     "no pull request and commits of its own",
			branch:   Branch{Name: "chore/338-share-helpers", Ahead: 2},
			wantKind: Unlanded,
			inDetail: []string{"2 commits ahead of the base ref", "no pull request names it", "no worktree holds it"},
		},
		{
			name:     "a worktree holding it reads as in flight",
			branch:   Branch{Name: "docs/449-retrim", Ahead: 1, Worktree: true},
			wantKind: Unlanded,
			inDetail: []string{"1 commit ahead of the base ref", "(a worktree holds it)"},
		},
		{
			name:     "one commit reads as one",
			branch:   Branch{Name: "fix/one", Ahead: 1},
			wantKind: Unlanded,
			inDetail: []string{"1 commit ahead of the base ref"},
		},
		{
			name:     "an open pull request is the lease",
			branch:   Branch{Name: "ci/436-gate", Ahead: 1, PullRequest: &PullRequest{Number: 448, State: Open}},
			wantNone: true,
		},
		{
			name:     "a merged pull request leaves debris",
			branch:   Branch{Name: "docs/434-ceilings", Ahead: 1, PullRequest: &PullRequest{Number: 445, State: Merged}},
			wantKind: Leftover,
			inDetail: []string{"#445 MERGED merged", "on the base ref through it"},
		},
		{
			name:     "a closed pull request leaves the work unlanded",
			branch:   Branch{Name: "docs/9-abandoned", Ahead: 3, PullRequest: &PullRequest{Number: 9, State: Closed}},
			wantKind: Unlanded,
			inDetail: []string{"#9 CLOSED is not open", "3 commits are unlanded"},
		},
		{
			name:     "one unlanded commit reads as prose",
			branch:   Branch{Name: "docs/10-abandoned", Ahead: 1, PullRequest: &PullRequest{Number: 10, State: Closed}},
			wantKind: Unlanded,
			inDetail: []string{"1 commit is unlanded"},
		},
		{
			name:     "an unknown state is never read as finished",
			branch:   Branch{Name: "docs/unknown", Ahead: 1, PullRequest: &PullRequest{Number: 11, State: "DRAFT"}},
			wantKind: Unlanded,
			inDetail: []string{"#11 DRAFT is not open"},
		},
		{
			name:     "nothing ahead is never a finding, pull request or not",
			branch:   Branch{Name: "main", Behind: 4},
			wantNone: true,
		},
		{
			name:     "a merged pull request for a branch already contained is not debris",
			branch:   Branch{Name: "docs/done", PullRequest: &PullRequest{Number: 3, State: Merged}},
			wantNone: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			found := Findings([]Branch{test.branch})
			if test.wantNone {
				if len(found) != 0 {
					t.Fatalf("Findings = %+v, want no finding", found)
				}
				return
			}
			if len(found) != 1 {
				t.Fatalf("Findings = %+v, want exactly one", found)
			}
			if found[0].Kind != test.wantKind {
				t.Errorf("kind = %q, want %q", found[0].Kind, test.wantKind)
			}
			if found[0].Branch != test.branch.Name {
				t.Errorf("branch = %q, want %q", found[0].Branch, test.branch.Name)
			}
			for _, want := range test.inDetail {
				if !strings.Contains(found[0].Detail, want) {
					t.Errorf("detail %q does not contain %q", found[0].Detail, want)
				}
			}
		})
	}
}

// TestFindingsIsOrdered pins the report's stability: the same branches in any input order give
// one order out, so two runs are comparable and a diff between them means something changed.
func TestFindingsIsOrdered(t *testing.T) {
	t.Parallel()

	branches := []Branch{
		{Name: "z-unlanded", Ahead: 1},
		{Name: "b-leftover", Ahead: 1, PullRequest: &PullRequest{Number: 2, State: Merged}},
		{Name: "a-unlanded", Ahead: 1},
		{Name: "a-leftover", Ahead: 1, PullRequest: &PullRequest{Number: 1, State: Merged}},
	}
	want := []string{"a-unlanded", "z-unlanded", "a-leftover", "b-leftover"}

	found := Findings(branches)
	if len(found) != len(want) {
		t.Fatalf("Findings returned %d findings, want %d", len(found), len(want))
	}
	for i, name := range want {
		if found[i].Branch != name {
			t.Errorf("finding %d = %q, want %q", i, found[i].Branch, name)
		}
	}
	// Unlanded first: the work at risk is what a reader must see before the debris.
	if found[0].Kind != Unlanded || found[len(found)-1].Kind != Leftover {
		t.Errorf("kinds = %q … %q, want the unlanded findings first", found[0].Kind, found[len(found)-1].Kind)
	}
}

// TestPullRequestRenders is the one rendering the engine owns, because the finding's detail
// and the command's table must spell a pull request the same way.
func TestPullRequestRenders(t *testing.T) {
	t.Parallel()

	if got, want := (PullRequest{Number: 445, State: Merged}).String(), "#445 MERGED"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
