// Package release turns a CHANGELOG section into GitHub release notes, and holds
// the guards that decide whether a tag may be published.
//
// It replaces scripts/release-notes.sh and scripts/release.sh, the repository's
// only scripts that ran exactly once per release — at the moment a mistake is
// most damaging (the notes are public and permanent) and least likely to be
// noticed, because nothing exercised them between releases. Both were untested.
// The work is text: find a version's section, reshape its headings, append the
// compare link. That is now a pure function over the changelog text, covered by
// tests including one that runs it against the real CHANGELOG.md.
//
// Publishing itself is not here: the caller runs `gh` and owns the argument
// parsing, and this package only decides.
package release

import (
	"fmt"
	"regexp"
	"strings"
)

// CompareBaseURL is the repository whose releases the notes link to. The compare
// link is what makes a release note traceable to the commits it describes, and it
// is the one line AGENTS.md's release policy requires in every note.
const CompareBaseURL = "https://github.com/dmundt/go-cask/compare"

// sectionHeading matches a release heading, `## [label]`, capturing the label.
var sectionHeading = regexp.MustCompile(`^## \[([^\]]*)\]`)

// Section returns the changelog section for a tag, as the lines between its
// `## [<tag>]` heading and the next `## [` heading.
//
// The match is a substring, not equality: `v1.2` selects `## [v1.2.0]`. That is
// the behaviour the shell helper had, and a release tag is normally exact, so the
// looseness only matters for a deliberate prefix query.
func Section(changelog, tag string) ([]string, error) {
	lines := splitLines(changelog)
	var section []string
	inSection := false
	for _, line := range lines {
		if match := sectionHeading.FindStringSubmatch(line); match != nil {
			if inSection {
				break
			}
			if strings.Contains(match[1], tag) {
				inSection = true
				// The heading itself is not part of the body: the notes' own
				// headings are the change groups.
				continue
			}
		}
		if inSection {
			section = append(section, line)
		}
	}
	if len(section) == 0 {
		return nil, fmt.Errorf("no changelog section found for %s", tag)
	}
	// The section ends where the next release begins, so the blank lines that
	// separate them are inside it. They are dropped because the shell helper read
	// its section through a command substitution, which strips every trailing
	// newline: preserving them here would put a triple newline before the compare
	// link. A blank line at the end of a section carries no information.
	for len(section) > 0 && strings.TrimSpace(section[len(section)-1]) == "" {
		section = section[:len(section)-1]
	}
	return section, nil
}

// groupHeadings are the keep-a-changelog change groups, which become the notes'
// top-level headings.
var groupHeadings = []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"}

// groupHeading matches one change-group heading at the start of a line, capturing
// the group name and anything that trails it.
var groupHeading = regexp.MustCompile(`^### (` + strings.Join(groupHeadings, "|") + `)(.*)$`)

// Reshape returns the note body: a section with its change-group headings promoted
// from `###` to `##`, so the groups read as sections of the release note.
//
// A group heading is only promoted when the line is exactly the heading: a
// trailing space or a suffix leaves it alone, which is the strictness the shell
// helper had (`/^### Fixed$/`). The promotion can only happen once per group
// because the changelog check rejects a repeated group within a release.
func Reshape(section []string) []string {
	notes := make([]string, 0, len(section))
	for index, line := range section {
		if index == 0 && sectionHeading.MatchString(line) {
			// The section extractor already drops the heading, so this cannot fire
			// for a section it produced. It is kept because the shell helper had it
			// and a caller may hand this function a section that still carries one.
			continue
		}
		if match := groupHeading.FindStringSubmatch(line); match != nil && match[2] == "" {
			notes = append(notes, "## "+match[1])
			continue
		}
		notes = append(notes, line)
	}
	return notes
}

// CompareURL returns the compare link between two tags.
func CompareURL(fromTag, newTag string) string {
	return fmt.Sprintf("%s/%s...%s", CompareBaseURL, fromTag, newTag)
}

// Notes returns the full release note: the reshaped section, a blank line, and the
// compare link.
//
// It takes an already-extracted section so the text shaping is testable without a
// changelog file; Build is the convenience that does both.
func Notes(section []string, fromTag, newTag string) string {
	body := strings.Join(Reshape(section), "\n")
	return fmt.Sprintf("%s\n\n**Full Changelog**: %s\n", body, CompareURL(fromTag, newTag))
}

// Build extracts the section for a tag and renders its notes in one step.
func Build(changelog, newTag, fromTag string) (string, error) {
	section, err := Section(changelog, newTag)
	if err != nil {
		return "", err
	}
	return Notes(section, fromTag, newTag), nil
}

// PreviousTag returns the highest tag below a given one, using the same ordering
// Git is asked for (`--sort=-version:refname`), or "" when there is none.
//
// The list is expected in that order, as `git tag --sort=-version:refname` prints
// it: this function picks the first entry that is not the tag itself rather than
// sorting, so the ordering rule lives in one place — the Git invocation — rather
// than being reimplemented here.
func PreviousTag(tags []string, tag string) string {
	for _, candidate := range tags {
		if candidate != tag && candidate != "" {
			return candidate
		}
	}
	return ""
}

// State is what the publish guards need to know about the repository. The caller
// gathers it with Git; this package decides.
type State struct {
	// Dirty reports whether the working tree has uncommitted changes.
	Dirty bool
	// TagExists reports whether the tag is present locally.
	TagExists bool
	// TagRevision is the commit the tag points at.
	TagRevision string
	// HeadRevision is the current HEAD commit.
	HeadRevision string
	// MainExists reports whether a local `main` branch exists.
	MainExists bool
	// TagOnMain reports whether the tag is an ancestor of `main`.
	TagOnMain bool
}

// ValidatePublish returns why a tag may not be published, or nil when it may.
//
// The guards exist so a release cannot describe a tree other than the tagged one:
// a dirty tree means the notes may not match the tag, a tag that is not HEAD means
// the notes describe something already superseded, and a tag off `main` is a
// release of a branch. Each is a separate sentinel-free error message, because the
// remedy differs.
func ValidatePublish(tag string, state State) error {
	switch {
	case state.Dirty:
		return fmt.Errorf("working tree must be clean before publishing a release")
	case !state.TagExists:
		return fmt.Errorf("release tag %s does not exist locally", tag)
	case state.TagRevision != state.HeadRevision:
		return fmt.Errorf("release tag %s must point at HEAD", tag)
	case !state.MainExists:
		return fmt.Errorf("main branch does not exist locally")
	case !state.TagOnMain:
		return fmt.Errorf("release tag %s must be reachable from main", tag)
	}
	return nil
}

// splitLines splits a changelog on LF, dropping a trailing newline's empty tail so
// a section never ends with a spurious blank line.
func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
