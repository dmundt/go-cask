// Package docs holds the repository's Markdown integrity rules: the checks that
// keep the specification set, the changelog and the published pages honest.
//
// The gate enforced these with an embedded Python program that lived in
// scripts/verify.sh: raw HTML,
// HTML/XML/SVG code fences, links and file references that point at nothing, and
// the CHANGELOG structure the release notes depend on. A rule written as an
// embedded script is executed by every developer and by CI but covered by no
// test of its own, which is the failure AGENTS.md, "`verify.sh` stays forever",
// rules out for the gate.
//
// The checks live here as ordinary functions over (path, content) pairs, so they
// are covered by table tests and can be exercised on a fixture without a
// repository. Reading the file list and reading each file stay with the caller,
// which is what keeps these functions pure and testable.
//
// The rules themselves are stated in docs/specs/AGENT.md §9 (the Markdown and
// documentation conventions) and AGENTS.md, "Changelog and release-note policy".
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// File is one Markdown file to check: its repository-relative path, as Git
// reports it, and its contents.
type File struct {
	// Path is the repository-relative path, with forward slashes.
	Path string
	// Content is the file's text. Line endings may be either form; both are
	// handled, so a Windows checkout reports the same findings as a Linux one.
	Content string
}

// Finding is one broken rule, with the position a reader needs to fix it.
type Finding struct {
	// Path is the repository-relative path of the offending file.
	Path string
	// Line is the 1-based line, or 0 when the finding is not tied to a line.
	Line int
	// Message is what is wrong.
	Message string
}

// String renders the finding the way the gate reports it.
func (f Finding) String() string {
	if f.Line == 0 {
		return fmt.Sprintf("%s: %s", f.Path, f.Message)
	}
	return fmt.Sprintf("%s:%d: %s", f.Path, f.Line, f.Message)
}

// CheckMarkdown returns every finding in one Markdown file: raw HTML, a
// forbidden code fence, a link or file reference that resolves to nothing, an
// unbalanced mermaid block, and — for CHANGELOG.md — the structure rules the
// release notes depend on.
//
// root is the repository root: a link target that starts with "/" is resolved
// against it, exactly as the published site resolves it.
func CheckMarkdown(root string, file File) []Finding {
	var findings []Finding
	raw := splitLines(file.Content)
	stripped := stripCode(raw)

	findings = append(findings, checkHTML(file.Path, stripped)...)
	findings = append(findings, checkFences(file.Path, stripped)...)
	findings = append(findings, checkMermaid(file.Path, raw)...)
	findings = append(findings, checkLinks(root, file.Path, stripped)...)
	if filepath.Base(file.Path) == "CHANGELOG.md" {
		// The changelog is read raw: its headings are structural, so one written
		// inside a code fence is a mistake the rule should still see.
		findings = append(findings, CheckChangelog(file.Path, strings.Join(raw, "\n"))...)
	}
	return findings
}

// mermaidFence matches a mermaid block's opening fence and the closing fence of
// any block. Both are counted raw, whole lines: the rule is that a mermaid block
// is closed before the file ends, because an unbalanced one swallows the rest of
// the page when it renders (docs/specs/AGENT.md §9).
var (
	mermaidFence = regexp.MustCompile("^```mermaid$")
	anyFence     = regexp.MustCompile("^```$")
)

// checkMermaid reports a file whose mermaid blocks are not balanced.
//
// It counts fences rather than parsing them: a file with more mermaid openers
// than closers has a block that never ends. The count is of whole lines, so a
// fence indented inside a list or a blockquote is not counted — which is the
// behaviour the gate had before this check moved here, and is deliberate: such a
// fence does not render as a mermaid block either.
func checkMermaid(path string, lines []string) []Finding {
	openers, closers := 0, 0
	for _, line := range lines {
		if mermaidFence.MatchString(line) {
			openers++
		}
		if anyFence.MatchString(line) {
			closers++
		}
	}
	if closers >= openers {
		return nil
	}
	return []Finding{{
		Path: path,
		Message: fmt.Sprintf("unbalanced mermaid: %d opening fence(s), %d closing fence(s)",
			openers, closers),
	}}
}

// htmlToken matches raw HTML: a comment, or a tag whose name is in the HTML
// vocabulary. The vocabulary is explicit rather than "any tag-like token"
// because a document legitimately writes things that look like a tag — a Go
// generic call in prose, an angle-bracketed placeholder — and reporting those
// would make the rule unusable.
var htmlToken = regexp.MustCompile(
	`<!--|</?(?:a|abbr|address|article|aside|audio|blockquote|body|button|` +
		`canvas|caption|cite|code|col|data|dd|del|details|dfn|dialog|div|dl|dt|` +
		`em|embed|fieldset|figcaption|figure|footer|form|h[1-6]|head|header|` +
		`hgroup|hr|html|iframe|img|input|ins|kbd|label|legend|li|link|main|map|` +
		`mark|menu|meta|meter|nav|noscript|object|ol|optgroup|option|output|p|` +
		`picture|pre|progress|q|s|samp|script|section|select|small|source|span|` +
		`style|sub|summary|sup|table|tbody|td|template|textarea|tfoot|th|thead|` +
		`time|title|tr|track|u|ul|var|video|wbr)(?:\s[^<>]*)?/?>`)

// forbiddenFence matches a fenced block whose language is HTML, XML or SVG,
// which the Markdown policy bans (docs/specs/AGENT.md §9).
var forbiddenFence = regexp.MustCompile(`(?i)^\s*(?:` + "```" + `|~~~)\s*(?:html|xml|svg)\b`)

// checkHTML reports raw HTML in prose. Code is stripped first: a fenced block or
// an inline span of a tag is documentation *about* markup, not markup.
func checkHTML(path string, lines []string) []Finding {
	var findings []Finding
	for i, line := range lines {
		for _, match := range htmlToken.FindAllString(line, -1) {
			findings = append(findings, Finding{
				Path:    path,
				Line:    i + 1,
				Message: "raw HTML is not allowed: " + match,
			})
		}
	}
	return findings
}

// checkFences reports a fenced block tagged html, xml or svg.
func checkFences(path string, lines []string) []Finding {
	var findings []Finding
	for i, line := range lines {
		if forbiddenFence.MatchString(line) {
			findings = append(findings, Finding{
				Path:    path,
				Line:    i + 1,
				Message: "HTML/XML/SVG code fences are not allowed",
			})
		}
	}
	return findings
}

// inlineLink matches a Markdown inline link, capturing the target inside angle
// brackets or bare. It deliberately does not try to match a link whose target
// contains spaces unless it is angle-bracketed, which is what CommonMark says.
var (
	inlineLink = regexp.MustCompile(`\[[^\]]+\]\(\s*(?:<([^>]+)>|([^\s)]+))`)
	// imageLink matches the start of an image, so an inline link that is really
	// an image's can be skipped: `![alt](x)` also matches inlineLink, at the
	// bracket one byte later.
	imageLink = regexp.MustCompile(`!\[[^\]]*\]\(`)
	// referenceLink matches a link definition line, `[label]: target`.
	referenceLink = regexp.MustCompile(`^\s*\[[^\]]+\]:\s*(\S+)`)
)

// checkLinks reports a link or file reference whose local target does not exist.
//
// A link is skipped when it is not a local file reference: an absolute URL (it
// has a scheme or a host), a pure fragment (`#anchor`), or a target with no path
// at all (a query-only or empty target). An image is skipped too — `![alt](x)` is
// an inline link by shape, and the original check excluded it explicitly.
func checkLinks(root, path string, lines []string) []Finding {
	var findings []Finding
	for i, line := range lines {
		for _, target := range linkTargets(line) {
			if target == "" {
				continue
			}
			if !referencesLocalFile(target) {
				continue
			}
			if !localTargetExists(root, path, target) {
				findings = append(findings, Finding{
					Path:    path,
					Line:    i + 1,
					Message: target,
				})
			}
		}
	}
	return findings
}

// linkTargets returns the targets of the inline links and link definitions on
// one line, with images excluded.
func linkTargets(line string) []string {
	// An inline link's match starts at its "[", which for an image begins one
	// byte after the "!" — so images are excluded by RANGE containment, not by
	// looking up the match's start. Go's regexp has no negative lookbehind to
	// express the original `(?<!!)`.
	var images [][2]int
	for _, loc := range imageLink.FindAllStringIndex(line, -1) {
		images = append(images, [2]int{loc[0], loc[1]})
	}
	isImage := func(start int) bool {
		for _, span := range images {
			if start >= span[0] && start < span[1] {
				return true
			}
		}
		return false
	}

	var targets []string
	for _, loc := range inlineLink.FindAllStringSubmatchIndex(line, -1) {
		if isImage(loc[0]) {
			continue
		}
		if loc[2] >= 0 {
			targets = append(targets, strings.TrimSpace(line[loc[2]:loc[3]]))
			continue
		}
		targets = append(targets, strings.TrimSpace(line[loc[4]:loc[5]]))
	}
	for _, match := range referenceLink.FindAllStringSubmatch(line, -1) {
		targets = append(targets, strings.TrimSpace(match[1]))
	}
	return targets
}

// referencesLocalFile reports whether a link target points at a file in this
// repository, which is the only kind this check can resolve.
func referencesLocalFile(target string) bool {
	if strings.HasPrefix(target, "#") {
		return false
	}
	// Strip the query and fragment, then decide from the remaining path. A
	// scheme or a host means the target is not a local file.
	path := target
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	if path == "" {
		return false
	}
	if strings.Contains(path, "://") {
		return false
	}
	if strings.HasPrefix(path, "//") {
		// Protocol-relative, so it names a host rather than a file.
		return false
	}
	if strings.HasPrefix(path, "mailto:") || strings.Contains(path, ":") && !strings.HasPrefix(path, "/") {
		// A scheme such as `mailto:`; a Windows-style drive letter does not
		// occur in a repository-relative link.
		return false
	}
	return true
}

// localTargetExists reports whether a local link resolves. An absolute path is
// resolved from the repository root — the way the published site serves it — and
// a relative path from the linking file's directory. The target is unescaped, so
// `%20` names a file with a space.
func localTargetExists(root, path, target string) bool {
	targetPath := target
	if index := strings.IndexAny(targetPath, "?#"); index >= 0 {
		targetPath = targetPath[:index]
	}
	targetPath = unescapePath(targetPath)
	if targetPath == "" {
		return true
	}

	var resolved string
	if strings.HasPrefix(targetPath, "/") {
		resolved = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(targetPath, "/")))
	} else {
		resolved = filepath.Join(root, filepath.FromSlash(filepath.Dir(path)), filepath.FromSlash(targetPath))
	}
	_, err := os.Stat(resolved)
	return err == nil
}

// unescapePath decodes the percent escapes a link uses for a path. A malformed
// escape is left as written, which is what the original did: the check reports
// the missing file rather than failing on the escape.
func unescapePath(path string) string {
	if !strings.Contains(path, "%") {
		return path
	}
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] == '%' && i+2 < len(path) {
			high, okHigh := hexValue(path[i+1])
			low, okLow := hexValue(path[i+2])
			if okHigh && okLow {
				b.WriteByte(high<<4 | low)
				i += 2
				continue
			}
		}
		b.WriteByte(path[i])
	}
	return b.String()
}

// hexValue returns the value of one hexadecimal digit.
func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// stripCode blanks out fenced blocks and inline code spans.
//
// Prose rules do not apply to code: a Go generic call such as
// `New[T](encode, decode)` is shaped exactly like a Markdown link, and a sample
// of a tag is not raw HTML in the document. Lines are blanked rather than
// dropped so the reported line numbers stay honest.
func stripCode(lines []string) []string {
	out := make([]string, 0, len(lines))
	var fence string
	for _, line := range lines {
		marker := fenceMarker.FindStringSubmatch(line)
		if fence == "" && marker != nil {
			fence = strings.Repeat(string(marker[1][0]), 3)
			out = append(out, line)
			continue
		}
		if fence != "" {
			if marker != nil && strings.HasPrefix(marker[1], fence) {
				fence = ""
			} else {
				out = append(out, "")
				continue
			}
			out = append(out, line)
			continue
		}
		out = append(out, inlineCode.ReplaceAllString(line, ""))
	}
	return out
}

var (
	// fenceMarker matches a fenced-block delimiter.
	fenceMarker = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
	// inlineCode matches an inline code span.
	inlineCode = regexp.MustCompile("`+[^`]*`+")
)

// splitLines splits text into lines the way the check needs them: on LF and CRLF,
// with no trailing empty line. Only these two endings occur in this repository,
// and handling them explicitly keeps the line numbers of a Windows checkout
// identical to a Linux one.
func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// Report prints the findings the way the gate reports them: one line each, in a
// stable order, deduplicated so the same broken link is not reported twice.
func Report(findings []Finding) []string {
	seen := map[string]bool{}
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		line := finding.String()
		if seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}
