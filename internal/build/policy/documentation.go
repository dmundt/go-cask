package policy

import "github.com/dmundt/go-cask/internal/build/core/docs"

// PackageReadmeTable is the frontmatter a package-level README under `internal/build`
// carries.
//
// Every versioned document in this repository declares how current it is (docs/AGENT.md
// §1.1): `type`, `title`, `description` and `version`, with `type` and `version` required.
// The gate enforces the *bump* — a changed versioned file must move its version — but a
// file that carries no frontmatter is not judged at all, so a package README without one
// silently opts out of the rule. This table is what the check reads, so the requirement is
// stated once and the READMEs stay covered as the subtree grows.
type PackageReadmeTable struct {
	// Type is the frontmatter type a package-local how-to carries: `Guide`, per the
	// vocabulary in docs/AGENT.md §1.2 (Agent Instructions is for AGENT.md files).
	Type string
	// Fields are the keys the frontmatter block must carry with a value.
	Fields []string
}

// PackageReadme returns the frontmatter contract for a package README. It is a function
// rather than a package-level variable so a caller cannot mutate it by accident.
func PackageReadme() PackageReadmeTable {
	return PackageReadmeTable{
		Type:   "Guide",
		Fields: []string{"type", "title", "description", "version"},
	}
}

// InstructionBudgets returns the byte ceiling go-cask sets for every agent instruction
// file, checked by the markdown-integrity step.
//
// An instruction file is read while an agent works in the tree it governs, so its size is
// a recurring cost and its ceiling is what keeps a guide a guide instead of a second
// specification. Each number is a ratchet set just above the file's size when the ceiling
// was added: room for a rule or two, none for the file to grow into an essay. The style
// the ceilings enforce, and the relocation a failure asks for, are docs/AGENT.md §2.1 — a
// rule that no longer fits belongs in its owner, and raising a number is a deliberate
// policy change in the change that needs it, never the fix for a failure.
//
// Root `AGENTS.md` is the one file with real headroom: read at the start of every session
// whatever the change, it is a router and sits far below its ceiling.
//
// `TestInstructionBudgetsCoverEveryInstructionFile` walks the tree, so a new guide with no
// entry here fails, and so does an entry whose file is gone.
func InstructionBudgets() []docs.InstructionBudget {
	return []docs.InstructionBudget{
		{Path: "AGENTS.md", MaxBytes: 6 * 1024},
		{Path: ".agents/AGENT.md", MaxBytes: 8 * 1024},
		{Path: ".github/AGENT.md", MaxBytes: 10 * 1024},
		{Path: "benchmarks/AGENT.md", MaxBytes: 8 * 1024},
		{Path: "benchmarks/data/AGENT.md", MaxBytes: 2 * 1024},
		{Path: "cas/AGENT.md", MaxBytes: 5 * 1024},
		{Path: "cas/codec/AGENT.md", MaxBytes: 3 * 1024},
		{Path: "cas/verify/AGENT.md", MaxBytes: 3 * 1024},
		{Path: "docs/AGENT.md", MaxBytes: 10 * 1024},
		{Path: "docs/design/AGENT.md", MaxBytes: 3 * 1024},
		{Path: "docs/specs/AGENT.md", MaxBytes: 13 * 1024},
		{Path: "examples/AGENT.md", MaxBytes: 5 * 1024},
		{Path: "internal/build/AGENT.md", MaxBytes: 5 * 1024},
		{Path: "scripts/AGENT.md", MaxBytes: 20 * 1024},
		{Path: "website/AGENT.md", MaxBytes: 9 * 1024},
	}
}
