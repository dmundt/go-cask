package docs

import (
	"regexp"
)

// CHANGELOG.md is published on the website and read by the release-notes command,
// so its structure is checked here rather than left to review (AGENTS.md,
// "Changelog and release-note policy"). Three rules, each for a failure that
// already happened:
//
//   - A release heading without a link definition renders as literal bracket text
//     instead of a link.
//   - A heading repeated inside one release section merges two sections into one,
//     which is how three `### Security` blocks accumulated in `Unreleased`.
//   - An `[Unreleased]` compare range that still starts at an older tag folds
//     already-released versions back into "unreleased" and reports the wrong
//     range for the next release.
//
// changelogHeading matches a release heading, `## [label]`.
var (
	changelogHeading    = regexp.MustCompile(`^## \[([^\]]+)\]`)
	changelogSection    = regexp.MustCompile(`^###\s+(Added|Changed|Deprecated|Removed|Fixed|Security)\s*$`)
	changelogLink       = regexp.MustCompile(`^\[([^\]]+)\]:\s*\S`)
	changelogUnreleased = regexp.MustCompile(`^\[Unreleased\]:\s*\S*?compare/([^\s]+?)\.\.\.HEAD\s*$`)
)

// CheckChangelog returns the structure findings in a CHANGELOG.md.
//
// path is the repository-relative path used in the findings, and content the
// file's text.
func CheckChangelog(path, content string) []Finding {
	lines := splitLines(content)

	defined := map[string]bool{}
	for _, line := range lines {
		if match := changelogLink.FindStringSubmatch(line); match != nil {
			defined[match[1]] = true
		}
	}

	var findings []Finding
	var (
		release        string
		newestRelease  string
		unreleasedAt   int
		unreleasedTag  string
		haveUnreleased bool
		seen           = map[string]bool{}
	)

	for index, line := range lines {
		if match := changelogUnreleased.FindStringSubmatch(line); match != nil && !haveUnreleased {
			unreleasedAt = index + 1
			unreleasedTag = match[1]
			haveUnreleased = true
		}

		if match := changelogHeading.FindStringSubmatch(line); match != nil {
			release = match[1]
			// A new section starts with no headings seen: the duplicate rule is
			// per release section, not per file.
			seen = map[string]bool{}
			if newestRelease == "" && release != "Unreleased" {
				newestRelease = release
			}
			if !defined[release] {
				findings = append(findings, Finding{
					Path:    path,
					Line:    index + 1,
					Message: "release heading [" + release + "] has no [" + release + "]: link definition",
				})
			}
			continue
		}

		if match := changelogSection.FindStringSubmatch(line); match != nil && release != "" {
			if seen[match[1]] {
				findings = append(findings, Finding{
					Path:    path,
					Line:    index + 1,
					Message: "### " + match[1] + " repeats in the " + release + " section",
				})
			}
			seen[match[1]] = true
		}
	}

	if haveUnreleased && newestRelease != "" && unreleasedTag != newestRelease {
		findings = append(findings, Finding{
			Path: path,
			Line: unreleasedAt,
			Message: "[Unreleased] compares from " + unreleasedTag +
				", but the newest released section is " + newestRelease,
		})
	}
	return findings
}
