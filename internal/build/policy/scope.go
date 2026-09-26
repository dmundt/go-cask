package policy

import "github.com/dmundt/go-cask/internal/build/core/changes"

// WebsitePaths are the paths that require the published site to be built: the
// website tree itself, and the configuration, dependency and workflow files that
// decide what it is built from.
func WebsitePaths() []changes.Pattern {
	return []changes.Pattern{
		{Prefix: "website/"},
		{Exact: "mkdocs.yml"},
		{Exact: "requirements-docs.txt"},
		{Exact: "requirements-docs.lock"},
		{Exact: ".github/workflows/website.yml"},
	}
}

// DocsPaths are the paths a documentation-only change consists of: everything that
// builds the site, the `docs/` tree, and the Markdown files anywhere in the
// repository.
//
// This list is the repository's answer to "what counts as documentation", and it has
// exactly one owner: the gate's scope decision and CI's scope job both classify
// through ScopeRules below, so the two cannot drift apart on a pattern list kept in
// sync only by a comment.
func DocsPaths() []changes.Pattern {
	paths := WebsitePaths()
	return append(paths, changes.Pattern{Suffix: ".md"}, changes.Pattern{Prefix: "docs/"})
}

// ScopeRules are the change classifications the gate and CI ask for, in the order
// they are reported. Every name is consumed as a CI job output or as the gate's
// scope decision, so a rule nothing reads is dead weight and a consumer naming a
// rule that is not here is drift — the policy tests check both directions.
//
//   - docs_only decides that a branch can be verified by the documentation rules
//     alone. It is an All rule: one Go file anywhere makes the change a code change.
//   - go_changed decides that the Go jobs run, and it implies security_changed: a
//     change that can alter the module is a change the vulnerability scan must see.
//   - security_changed additionally covers the CI configuration and the gate itself,
//     because a weakened scan is a security change even when no Go file moved.
//   - website_changed decides that the site is built with MkDocs.
func ScopeRules() []changes.Rule {
	return []changes.Rule{
		{
			Name:  "docs_only",
			Mode:  changes.All,
			Paths: DocsPaths(),
		},
		{
			Name: "go_changed",
			Mode: changes.Any,
			Paths: []changes.Pattern{
				{Suffix: ".go"},
				{Exact: "go.mod"},
				{Exact: "go.sum"},
			},
		},
		{
			Name: "security_changed",
			Mode: changes.Any,
			Paths: []changes.Pattern{
				{Exact: ".github/workflows/ci.yml"},
				// The gate is the thing the security scan protects: a change to it
				// must be scanned even when it touches no Go file.
				{Exact: "scripts/verify.sh"},
			},
			Also: []string{"go_changed"},
		},
		{
			Name:  "website_changed",
			Mode:  changes.Any,
			Paths: WebsitePaths(),
		},
	}
}

// ScopeRuleNames returns the rule names in reporting order, so a consumer can check
// that it names each one and no other.
func ScopeRuleNames() []string {
	rules := ScopeRules()
	names := make([]string, 0, len(rules))
	for _, rule := range rules {
		names = append(names, rule.Name)
	}
	return names
}
