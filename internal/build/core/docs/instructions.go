package docs

import "fmt"

// InstructionBudget is one auto-read agent instruction file and the byte ceiling its
// repository allows it.
//
// The engine ships no number: a ceiling is one repository's answer, so the caller
// supplies it. What this package owns is the decision — a file above its ceiling
// fails, a ceiling that names no file fails too, and a file at or under its ceiling
// passes.
type InstructionBudget struct {
	// Path is the repository-relative path of the instruction file, with forward
	// slashes, as Git reports it.
	Path string
	// MaxBytes is the ceiling the repository sets for that file.
	MaxBytes int
}

// CheckInstructionBudgets reports every instruction file that outgrew its ceiling,
// and every ceiling that names no file in the set it was checked against.
//
// An instruction file is read into an agent's context at session start, so its size
// is a recurring cost; a ceiling is what keeps such a file a router — a pointer to the
// rule's owner — instead of a second copy of the specification. The finding says how
// far over the file is, because the fix is a relocation, not a rewrite.
//
// files is the set of budgeted files that exist, already read by the caller: reading
// the working tree stays with the command, which is what keeps this decision pure and
// testable without a repository. A budget whose path is absent from files is reported
// as stale — a renamed or deleted instruction file must not keep its exemption (or its
// ceiling) alive after it is gone.
func CheckInstructionBudgets(budgets []InstructionBudget, files []File) []Finding {
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = file.Content
	}

	var findings []Finding
	for _, budget := range budgets {
		body, found := content[budget.Path]
		if !found {
			findings = append(findings, Finding{
				Path:    budget.Path,
				Message: fmt.Sprintf("instruction budget of %d bytes names no such file in the set it was checked against; drop the entry or fix its path", budget.MaxBytes),
			})
			continue
		}
		if len(body) <= budget.MaxBytes {
			continue
		}
		findings = append(findings, Finding{
			Path: budget.Path,
			Message: fmt.Sprintf("instruction file is %d bytes, %d over its %d-byte ceiling: move a rule to its owner and link that, never raise the ceiling silently",
				len(body), len(body)-budget.MaxBytes, budget.MaxBytes),
		})
	}
	return findings
}
