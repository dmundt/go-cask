package website

import (
	"fmt"
	"regexp"
	"strings"
)

// FooterSpec is one site's footer contract: the configuration key that declares the
// base line, the token the deployed revision's year follows, and how the provenance
// fragment reads. Every value is the caller's — a site states its own footer — and
// the rules that use them are not.
type FooterSpec struct {
	// Key is the configuration key whose folded block scalar declares the base
	// line, e.g. "copyright".
	Key string
	// YearToken is the token the revision's year is inserted after, e.g. "&copy;".
	YearToken string
	// Separator is the markup between two fragments of the line, e.g. "&middot; ".
	Separator string
	// Label is the visible text immediately before the commit link, e.g. "Updated".
	Label string
	// CommitURL is the prefix a revision is appended to, so the anchor points at
	// the commit the line was composed from.
	CommitURL string
	// Forbidden is text the rendered line must never carry: a build zone, a second
	// provenance phrase, a site's own copy of a value it is supposed to derive.
	Forbidden []string
}

// yearLiteral matches a four-digit year, which a footer must derive from the
// deployed revision's date instead of carrying as a literal.
var yearLiteral = regexp.MustCompile(`\b(?:19|20)\d\d\b`)

// tagPattern matches a tag with its attributes, so VisibleText can strip the markup
// that legitimately carries a revision.
var tagPattern = regexp.MustCompile(`<[^>]*>`)

// FoldedScalar returns the value of a `key: >-` folded block scalar, folded the way
// YAML folds it: the indented lines below the key joined by single spaces. It
// reports false when the document declares no such key, which a caller that pinned
// the key's value must treat as a failure rather than as an empty line.
func FoldedScalar(document, key string) (string, bool) {
	lines := splitLines(document)
	opener := key + ": >-"
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != opener {
			continue
		}
		var parts []string
		for _, next := range lines[i+1:] {
			// The scalar ends at the first line that is not indented under the
			// key, and a comment line belongs to the document, not to the value.
			if !strings.HasPrefix(next, "  ") || strings.HasPrefix(strings.TrimSpace(next), "#") {
				break
			}
			parts = append(parts, strings.TrimSpace(next))
		}
		return strings.Join(parts, " "), true
	}
	return "", false
}

// VisibleText drops tags and their attributes, so a caller can assert what a reader
// sees rather than what the markup carries.
func VisibleText(line string) string {
	return tagPattern.ReplaceAllString(line, "")
}

// FooterLine completes the base line for one deployed revision: the revision's year
// follows the year token, and the line closes with the label and that revision's
// date, linked to the commit. Either value is omitted — never guessed — when it is
// missing, so a build that cannot read a revision renders the base line unchanged.
func FooterLine(base, date, revision string, spec FooterSpec) string {
	line := strings.Join(strings.Fields(base), " ")
	year := leadingYear(date)
	if year == "" {
		return line
	}
	line = strings.Replace(line, spec.YearToken, spec.YearToken+" "+year, 1)
	if revision == "" {
		return line
	}
	return line + " " + spec.Separator + spec.Label + ` <a href="` + spec.CommitURL + revision +
		`" title="commit ` + revision + `">` + date + `</a>`
}

// leadingYear returns the four-digit year a date starts with, or "" when the value
// is not a date that names one.
func leadingYear(date string) string {
	if len(date) < 4 {
		return ""
	}
	year := date[:4]
	for _, digit := range year {
		if digit < '0' || digit > '9' {
			return ""
		}
	}
	return year
}

// FooterFindings reports what is wrong with a site's footer: the base line carries a
// literal year or cannot take the revision's, the composed line is not one line,
// carries text the contract forbids, or loses the link, the label or the fact that
// the revision belongs in the link target rather than in the text. It is empty when
// the footer holds.
func FooterFindings(base, date, revision string, spec FooterSpec) []string {
	var findings []string
	if literal := yearLiteral.FindString(base); literal != "" {
		findings = append(findings, fmt.Sprintf(
			"the base line names the year %s; it must derive from the deployed revision's date", literal))
	}
	if date != "" && leadingYear(date) == "" {
		findings = append(findings, fmt.Sprintf(
			"the date %q names no year, so the footer cannot show one", date))
	}

	line := FooterLine(base, date, revision, spec)
	if strings.ContainsAny(line, "\r\n") {
		findings = append(findings, fmt.Sprintf("the footer is %d lines; it must be one", strings.Count(line, "\n")+1))
	}
	for _, forbidden := range spec.Forbidden {
		if strings.Contains(line, forbidden) {
			findings = append(findings, fmt.Sprintf("the footer line carries %q; it names no zone and invents no second value", forbidden))
		}
	}
	if revision == "" {
		return findings
	}
	if !strings.Contains(line, spec.CommitURL+revision) {
		findings = append(findings, fmt.Sprintf("the footer line does not link to commit %s", revision))
	}
	if strings.Contains(VisibleText(line), revision) {
		findings = append(findings, fmt.Sprintf(
			"the footer line shows the revision %s as visible text; it belongs in the link target", revision))
	}
	if !strings.Contains(line, spec.Label+" <a href=") {
		findings = append(findings, fmt.Sprintf("the footer line does not label the date %q outside the link", spec.Label))
	}
	return findings
}

// SourceGuard is a file that must carry certain text and must not carry other text,
// which is the shape a "this machinery stays deleted" rule takes. Both lists are the
// caller's: the engine decides only whether the file honours them.
type SourceGuard struct {
	// Path is the file, relative to the repository root, the caller reads.
	Path string
	// Must is text the file has to contain.
	Must []string
	// MustNot is text the file must not contain.
	MustNot []string
}

// CheckSourceGuards reports, per guard, the required text a file no longer carries
// and the removed text that came back. contents maps a guard's path to its text, so
// the caller owns reading and the rule stays provable without a repository.
func CheckSourceGuards(contents map[string]string, guards []SourceGuard) []string {
	var findings []string
	for _, guard := range guards {
		source, ok := contents[guard.Path]
		if !ok {
			findings = append(findings, fmt.Sprintf("%s: not read, so the guard on it proves nothing", guard.Path))
			continue
		}
		for _, want := range guard.Must {
			if !strings.Contains(source, want) {
				findings = append(findings, fmt.Sprintf("%s: no longer contains %q", guard.Path, want))
			}
		}
		for _, gone := range guard.MustNot {
			if strings.Contains(source, gone) {
				findings = append(findings, fmt.Sprintf("%s: contains %q again, which the rule deleted", guard.Path, gone))
			}
		}
	}
	return findings
}

// CheckAbsentPaths reports the paths that exist although the rule deleted them.
// exists is the caller's filesystem question, so the rule is testable without one.
func CheckAbsentPaths(paths []string, exists func(string) bool) []string {
	var findings []string
	for _, path := range paths {
		if exists(path) {
			findings = append(findings, fmt.Sprintf("%s still exists; the rule deleted it", path))
		}
	}
	return findings
}
