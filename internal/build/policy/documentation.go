package policy

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
