package docs

import (
	"strings"
	"testing"
)

// TestCheckInstructionBudgetsTable pins the decision an instruction-file ceiling
// makes: a file at or under its ceiling passes, a file over it fails with the
// overflow named, and a ceiling whose file is gone is stale rather than silently
// excused. The boundary case matters most — a ceiling of exactly the file's length
// is met, not exceeded.
func TestCheckInstructionBudgetsTable(t *testing.T) {
	t.Parallel()

	body := "# Router\n\nShort.\n"

	tests := []struct {
		name    string
		budgets []InstructionBudget
		files   []File
		want    []string
	}{
		{
			name:    "under the ceiling",
			budgets: []InstructionBudget{{Path: "AGENTS.md", MaxBytes: len(body) + 1}},
			files:   []File{{Path: "AGENTS.md", Content: body}},
		},
		{
			name:    "exactly at the ceiling",
			budgets: []InstructionBudget{{Path: "AGENTS.md", MaxBytes: len(body)}},
			files:   []File{{Path: "AGENTS.md", Content: body}},
		},
		{
			name:    "one byte over",
			budgets: []InstructionBudget{{Path: "AGENTS.md", MaxBytes: len(body) - 1}},
			files:   []File{{Path: "AGENTS.md", Content: body}},
			want:    []string{"AGENTS.md"},
		},
		{
			name:    "a budget with no file is stale",
			budgets: []InstructionBudget{{Path: "AGENTS.md", MaxBytes: 4096}},
			files:   nil,
			want:    []string{"AGENTS.md"},
		},
		{
			name: "one over, one under",
			budgets: []InstructionBudget{
				{Path: "AGENTS.md", MaxBytes: 4},
				{Path: "cas/AGENT.md", MaxBytes: 4096},
			},
			files: []File{
				{Path: "AGENTS.md", Content: body},
				{Path: "cas/AGENT.md", Content: "# cas\n"},
			},
			want: []string{"AGENTS.md"},
		},
		{
			name:    "an unbudgeted file is never reported",
			budgets: []InstructionBudget{{Path: "AGENTS.md", MaxBytes: 4096}},
			files: []File{
				{Path: "AGENTS.md", Content: body},
				{Path: "docs/specs/cas-core.md", Content: strings.Repeat("x", 100000)},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			findings := CheckInstructionBudgets(test.budgets, test.files)

			var got []string
			for _, finding := range findings {
				got = append(got, finding.Path)
				// A size finding is not tied to one line: the whole file is the
				// subject, and naming a line would point a reader at a random one.
				if finding.Line != 0 {
					t.Errorf("finding for %s carries line %d, want 0", finding.Path, finding.Line)
				}
			}
			if len(got) != len(test.want) {
				t.Fatalf("reported %d finding(s) %v, want %d %v", len(got), got, len(test.want), test.want)
			}
			for i, want := range test.want {
				if got[i] != want {
					t.Errorf("finding %d is about %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestCheckInstructionBudgetsNamesTheOverflow pins the message a maintainer acts on:
// both numbers, so the fix — move that many bytes to the rule's owner — is stated
// rather than guessed. It also pins that the ceiling itself is never the suggestion:
// raising it is what the finding must not invite.
func TestCheckInstructionBudgetsNamesTheOverflow(t *testing.T) {
	t.Parallel()

	findings := CheckInstructionBudgets(
		[]InstructionBudget{{Path: "AGENTS.md", MaxBytes: 10}},
		[]File{{Path: "AGENTS.md", Content: strings.Repeat("a", 14)}},
	)
	if len(findings) != 1 {
		t.Fatalf("reported %d finding(s), want 1", len(findings))
	}
	message := findings[0].Message
	for _, want := range []string{"14 bytes", "4 over", "10-byte ceiling", "never raise the ceiling silently"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not name %q", message, want)
		}
	}
}
