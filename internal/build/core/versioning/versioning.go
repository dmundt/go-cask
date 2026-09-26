// Package versioning holds one rule: a file that carries a version moved it when it
// changed.
//
// The package is named for the rule rather than the field it reads, because reading
// the field is not what it contributes — the frontmatter reader lives in the docs
// package, so a rule and a document renderer cannot disagree about what a version is.
// What this package owns is the decision: which paths the rule applies to, and which
// of them failed it.
//
// It is deliberately not tied to one field name or one document format. The caller
// supplies the version a path carried and the version it carries now, which keeps the
// decision testable without a repository and applicable to any file whose version
// lives in its frontmatter.
//
// The command that reads Git and the working tree to produce those two values is
// cmd/buildtool's `version-fields` subcommand.
package versioning

import (
	"sort"

	"github.com/dmundt/go-cask/internal/build/core/docs"
)

// Field returns the version a document carries in its frontmatter, or "" when it has
// no frontmatter or no version field. It is the docs package's reader, re-exported so
// a caller of this rule needs one import.
func Field(content string) string { return docs.Field(content) }

// Result is one path the rule judged: the version before and after the change.
type Result struct {
	// Path is the repository-relative path.
	Path string
	// Before is the version the base revision carried.
	Before string
	// After is the version the working file carries.
	After string
	// Judged reports whether the rule applied at all. A file with no version in the
	// base — a new file, or one that was never versioned — has nothing to compare,
	// so the rule does not apply to it.
	Judged bool
}

// Bumped reports whether a judged path moved its version.
func (r Result) Bumped() bool { return r.Judged && r.After != r.Before }

// Unbumped returns the judged paths whose version did not move, sorted.
//
// A path that is not judged is skipped rather than reported: the rule is "if the base
// carried a version, the change must move it", so a file the base revision does not
// contain cannot violate it.
func Unbumped(results []Result) []string {
	paths := []string{}
	for _, result := range results {
		if !result.Judged {
			continue
		}
		if !result.Bumped() {
			paths = append(paths, result.Path)
		}
	}
	sort.Strings(paths)
	return paths
}
