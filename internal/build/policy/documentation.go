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

// InstructionBudgets returns the byte ceiling go-cask sets for each auto-read agent
// instruction file, checked by the markdown-integrity step.
//
// The root `AGENTS.md` is the file the ceiling binds, and it is a router: read at the
// start of every session whatever the change, it names the owner of each rule instead
// of restating it, so its size is a recurring cost with nothing to spend it on.
// 6 KiB is a ceiling, not a target — the router is expected to sit far below it — and
// the rule behind the number, the style it enforces and the relocation the failure
// asks for are docs/AGENT.md §2.1. A rule that no longer fits belongs in its owner;
// raising this number is a policy change, not a fix.
func InstructionBudgets() []docs.InstructionBudget {
	return []docs.InstructionBudget{
		{Path: "AGENTS.md", MaxBytes: 6 * 1024},
	}
}
