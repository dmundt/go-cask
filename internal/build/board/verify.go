package board

import (
	"fmt"
	"slices"
	"strings"
)

// Check is one of the six lines a landing is verified by, each read from its own authority
// rather than from the lane's report (docs/specs/coordination.md §6).
type Check struct {
	// Name is the rule, as the spec states it.
	Name string `json:"name"`
	// Evidence is what the authority said, in the authority's own terms.
	Evidence string `json:"evidence"`
	// OK is false when the line does not hold.
	OK bool `json:"ok"`
}

// Verification is a landing's six lines.
type Verification struct {
	// Subject names what was verified: `PR #N` or `issue #N`.
	Subject string `json:"subject"`
	// Verified is whether every line holds, reported alongside the lines so a reader does
	// not have to fold them itself.
	Verified bool `json:"verified"`
	// Checks are the six lines, in the spec's order.
	Checks []Check `json:"checks"`
	// Notes are readings that could not be made. A line with no evidence is never reported
	// as holding: an unknown is a failure to verify, not a pass.
	Notes []string `json:"notes,omitempty"`
}

// AllHold reports whether every line holds.
func (v Verification) AllHold() bool {
	for _, check := range v.Checks {
		if !check.OK {
			return false
		}
	}
	return true
}

// Verdict is the six readers a landing is verified from. Every field is a reading the
// caller made from the authority named beside it; nothing here is inferred, and a field the
// caller could not read is false with a note, never true by default.
type Verdict struct {
	// Subject is what the caller verified, e.g. `PR #469`.
	Subject string

	// Merged is `gh pr view --json state` == MERGED.
	Merged bool
	// MergeState is the forge's `mergeStateStatus`.
	MergeState string
	// Squash is true when the merge was a squash, read from the merge commit's parents:
	// a squash has one, a merge commit has two.
	Squash bool
	// Body is the merged pull request's body, which must cite the issue.
	Body string
	// Issue is the issue number the pull request must cite.
	Issue string

	// IssueState is `gh issue view --json state` of the issue the pull request cites.
	IssueState string

	// Files are the paths the landing changed, from the merge commit's diff.
	Files []string
	// Scope is the issue's declared scope, as the issue's own body names it. An empty
	// scope is reported as unproven rather than as satisfied.
	Scope []string

	// UserVisible is true when the change is one a user sees — anything but documentation.
	UserVisible bool
	// Changelog is true when the landing carried a CHANGELOG.md change.
	Changelog bool
	// VersionBumped is true when the landing moved a frontmatter `version:`.
	VersionBumped bool

	// Signed is `git verify-commit <head>` succeeding.
	Signed bool
	// ReceiptPublished is true when `refs/gate/<head>` exists on origin.
	ReceiptPublished bool
	// Head is the full commit that was signed and published.
	Head string

	// LaneReleased is true when the claim ref `refs/lane/<issue>` no longer exists.
	LaneReleased bool
	// WorktreeGone is true when `git worktree list` no longer names the lane's worktree.
	WorktreeGone bool

	// Notes are what the caller could not read.
	Notes []string
}

// Checks judges a landing against the six rules, each reported with the evidence it was read
// from. The order is the spec's, so a report read beside §6 lines up line for line.
func Checks(v Verdict) Verification {
	verification := Verification{Subject: v.Subject, Notes: append([]string(nil), v.Notes...)}
	verification.Checks = []Check{
		mergedCheck(v),
		issueCheck(v),
		scopeCheck(v),
		{
			Name:     "the change carried the docs it needed",
			Evidence: docsEvidence(v),
			OK:       docsOK(v),
		},
		{
			Name:     "the head was signed and its gate receipt was published",
			Evidence: evidenceEvidence(v),
			OK:       v.Signed && v.ReceiptPublished,
		},
		{
			Name:     "the lane was released and its worktree is gone",
			Evidence: laneEvidence(v),
			OK:       v.LaneReleased && v.WorktreeGone,
		},
	}
	verification.Verified = verification.AllHold()
	return verification
}

// mergedCheck is §6's first line: the pull request merged as a squash and its body cites the
// issue. The body citation is what ties the landing to the issue, so a merge without it is
// not a landing this check accepts however green the pull request was.
func mergedCheck(v Verdict) Check {
	check := Check{Name: "the pull request merged as a squash and cites the issue"}
	switch {
	case !v.Merged:
		check.Evidence = "the forge does not report the pull request as merged"
	case !v.Squash:
		check.Evidence = "the merge carries more than one parent: not a squash"
	case !Mentioned(v.Body, v.Issue):
		check.Evidence = fmt.Sprintf("the merged body does not cite Closes #%s", v.Issue)
	default:
		check.Evidence = fmt.Sprintf("merged squash, body cites Closes #%s", v.Issue)
		check.OK = true
	}
	return check
}

// issueCheck is §6's second line: the issue is CLOSED, read from the issue rather than from
// the pull request that claims to close it.
func issueCheck(v Verdict) Check {
	check := Check{Name: "the issue is CLOSED"}
	if strings.EqualFold(strings.TrimSpace(v.IssueState), "CLOSED") {
		check.Evidence = fmt.Sprintf("issue #%s is CLOSED", v.Issue)
		check.OK = true
		return check
	}
	state := strings.TrimSpace(v.IssueState)
	if state == "" {
		state = "unknown"
	}
	check.Evidence = fmt.Sprintf("issue #%s is %s", v.Issue, state)
	return check
}

// scopeCheck is §6's third line: the merged file list stays within the issue's scope. The
// scope is read from the issue's own body; when the body names none the line is reported as
// unproven rather than satisfied, because a check that passes when it could not read its
// input is the one that lets a drive-by file through.
func scopeCheck(v Verdict) Check {
	check := Check{Name: "the merged file list stays within the issue's scope"}
	if len(v.Scope) == 0 {
		check.Evidence = "the issue names no scope: compare the file list with the issue by hand"
		return check
	}
	outside := Outside(v.Files, v.Scope)
	if len(outside) > 0 {
		check.Evidence = fmt.Sprintf("%d file(s) outside %s: %s",
			len(outside), strings.Join(v.Scope, ", "), strings.Join(outside, ", "))
		return check
	}
	check.Evidence = fmt.Sprintf("every one of %d changed file(s) is under %s",
		len(v.Files), strings.Join(v.Scope, ", "))
	check.OK = true
	return check
}

// docsOK is §6's fourth line: a user-visible change carried a CHANGELOG bullet and the
// frontmatter bump it needed, and a change that is not user-visible carried neither. Both
// directions matter — a documentation-only change that appends a changelog bullet is as
// much drift as a user-visible one that does not.
func docsOK(v Verdict) bool {
	if v.UserVisible {
		return v.Changelog && v.VersionBumped
	}
	return !v.Changelog && !v.VersionBumped
}

// docsEvidence states which direction §6's fourth line was read in and what it found.
func docsEvidence(v Verdict) string {
	if !v.UserVisible {
		switch {
		case !v.Changelog && !v.VersionBumped:
			return "not user-visible, and it carried neither a CHANGELOG bullet nor a frontmatter bump"
		default:
			return fmt.Sprintf("not user-visible, but it carried a CHANGELOG bullet (%t) or a frontmatter bump (%t)",
				v.Changelog, v.VersionBumped)
		}
	}
	switch {
	case v.Changelog && v.VersionBumped:
		return "user-visible, with a CHANGELOG bullet and a frontmatter bump"
	case v.Changelog:
		return "user-visible and carried a CHANGELOG bullet, but no frontmatter bump"
	case v.VersionBumped:
		return "user-visible and bumped a frontmatter version, but no CHANGELOG bullet"
	default:
		return "user-visible, with no CHANGELOG bullet and no frontmatter bump"
	}
}

// evidenceEvidence is §6's fifth line: the branch head was signed and its gate receipt was
// published. The two are separate readings, so the evidence says which one is missing.
func evidenceEvidence(v Verdict) string {
	head := v.Head
	if len(head) > 12 {
		head = head[:12]
	}
	switch {
	case v.Signed && v.ReceiptPublished:
		return fmt.Sprintf("%s verifies as a signed commit and refs/gate/%s exists on origin", head, head)
	case !v.Signed && !v.ReceiptPublished:
		return fmt.Sprintf("%s is not a verified signature and refs/gate/%s is not on origin", head, head)
	case !v.Signed:
		return fmt.Sprintf("refs/gate/%s is on origin, but %s is not a verified signature", head, head)
	default:
		return fmt.Sprintf("%s verifies as a signed commit, but refs/gate/%s is not on origin", head, head)
	}
}

// laneEvidence is §6's sixth line: the lane released and the worktree gone. It is read from
// `git worktree list` — the authority for a removal — rather than from a removal's message
// (docs/specs/coordination.md §3).
func laneEvidence(v Verdict) string {
	switch {
	case v.LaneReleased && v.WorktreeGone:
		return "the claim ref is gone and no worktree holds the lane"
	case !v.LaneReleased && !v.WorktreeGone:
		return "the claim ref still exists and a worktree still holds the lane"
	case !v.LaneReleased:
		return "the worktree is gone, but the claim ref still exists"
	default:
		return "the claim ref is gone, but a worktree still holds the lane"
	}
}

// Outside lists the changed files that no declared scope covers, sorted. A scope entry is a
// path prefix: `internal/build/board` covers that directory and everything under it, and a
// trailing slash is irrelevant, which is what makes a scope read from an issue body usable
// as it was written.
func Outside(files, scope []string) []string {
	prefixes := make([]string, 0, len(scope))
	for _, entry := range scope {
		if trimmed := strings.TrimSuffix(strings.TrimSpace(entry), "/"); trimmed != "" {
			prefixes = append(prefixes, trimmed)
		}
	}
	var outside []string
	for _, file := range files {
		path := CanonicalPath(file)
		if path == "" {
			continue
		}
		covered := false
		for _, prefix := range prefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				covered = true
				break
			}
		}
		if !covered {
			outside = append(outside, path)
		}
	}
	slices.Sort(outside)
	return dedupe(outside)
}

// ScopeIn reads the paths an issue's body declares, in the sections a go-cask issue uses to
// say where its change belongs: `Home`, `Scope` or `Files`. A line that is prose rather than
// a path is skipped, so an issue that says "Home: internal/build/board" and an issue that
// says "Scope: `cmd/buildtool/`, `docs/index.md`" both yield their paths.
//
// Deliberately conservative: an issue whose scope cannot be read yields none, and the check
// says so rather than passing.
func ScopeIn(body string) []string {
	var declared []string
	inSection := false
	// In a list under a bare `Home:` header. It is separate from inSection because the
	// blank line between a heading and its list is not the end of the section — a blank
	// line that followed a heading has nothing before it to close.
	inList := false
	for _, line := range strings.Split(body, "\n") {
		raw := strings.TrimSpace(line)
		// A heading is the section boundary, and a leading `#` is the only marker that
		// is one; a bullet is a list item wherever it appears.
		if strings.HasPrefix(raw, "#") {
			trimmed := stripListMarker(raw)
			inSection = sectionHeading(trimmed)
			inList = inSection && !strings.Contains(trimmed, ":")
			if inSection && !inList {
				declared = append(declared, pathsIn(trimmed)...)
			}
			continue
		}
		if !inSection {
			continue
		}
		if raw == "" {
			// A blank line ends a list, and ends the section when nothing was open.
			inSection = inList
			inList = false
			continue
		}
		trimmed := stripListMarker(raw)
		if trimmed == "" {
			continue
		}
		declared = append(declared, pathsIn(trimmed)...)
	}
	return dedupe(declared)
}

// stripListMarker removes a leading list marker — a heading's hashes, a bullet, a checkbox,
// a numbered item — from a line before it is read as a path. Which marker it was does not
// matter; what does is that the marker is not left in the line, where a hashtag inside prose
// would read as a directory. The colon stays: `Home:` names a section by its colon, and a
// marker stripper that ate it would turn the section's own line into prose.
func stripListMarker(line string) string {
	trimmed := strings.TrimSpace(line)
	for trimmed != "" {
		switch {
		case strings.HasPrefix(trimmed, "#"):
			trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "), strings.HasPrefix(trimmed, "> "):
			trimmed = strings.TrimSpace(trimmed[2:])
		case strings.HasPrefix(trimmed, "[ ] "), strings.HasPrefix(trimmed, "[x] "), strings.HasPrefix(trimmed, "[X] "):
			trimmed = strings.TrimSpace(trimmed[4:])
		default:
			return trimmed
		}
	}
	return trimmed
}

// sectionHeading reports whether a line opens one of the sections an issue states its scope
// in — `Home`, `Scope`, `Files`, `Where` — with or without a value after the colon.
func sectionHeading(line string) bool {
	lower := strings.ToLower(line)
	for _, name := range []string{"home", "scope", "files", "where"} {
		if lower == name || strings.HasPrefix(lower, name+":") {
			return true
		}
	}
	return false
}

// pathsIn reads the backticked or bare path-looking tokens out of one line. A token is a
// path when it carries a slash or a dotted file name, which is what keeps an issue's prose
// out of its scope.
func pathsIn(line string) []string {
	var paths []string
	for _, field := range strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == ';' || r == '`' || r == '(' || r == ')' || r == '[' || r == ']'
	}) {
		field = strings.TrimSpace(strings.Trim(field, "`*\"'"))
		field = strings.TrimSuffix(field, ".")
		if field == "" || strings.ContainsAny(field, " \t") {
			continue
		}
		if !strings.Contains(field, "/") && !strings.Contains(field, ".") {
			continue
		}
		paths = append(paths, CanonicalPath(field))
	}
	return paths
}
