package docs

import (
	"regexp"
	"strings"
)

// frontmatterFence is the `---` line that opens or closes a frontmatter block.
var frontmatterFence = regexp.MustCompile(`^---[ \t]*$`)

// frontmatterField matches one `key: value` line of a frontmatter block and captures the
// key and the value. The value is read verbatim, including trailing spaces: a rule that
// compares a field compares its text, so trimming here would make `v1` and `v1 ` equal.
var frontmatterField = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):[ \t]*(.*)$`)

// Fields returns the fields of a document's frontmatter block, keyed by field name, and
// whether the document carries such a block at all. A key that appears twice keeps the
// value it was first given, which is the value Field has always read.
//
// The block is a leading `---` line followed by fields until the next `---` line. A file
// whose first line is not the fence has no frontmatter at all. A block that is never
// closed reads to the end of the document, which is the reading the version rule has
// always used: a forgotten closing fence must not silently exempt a document from
// versioning.
//
// This is the one frontmatter reader in the toolkit: the version-field rule, the document
// renderer and the check that a package README carries its keys all use it, so they
// cannot disagree about what a frontmatter field is.
func Fields(content string) (map[string]string, bool) {
	lines := splitLines(content)
	if len(lines) == 0 || !frontmatterFence.MatchString(lines[0]) {
		return nil, false
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		if frontmatterFence.MatchString(line) {
			break
		}
		if match := frontmatterField.FindStringSubmatch(line); match != nil {
			if _, seen := fields[match[1]]; !seen {
				fields[match[1]] = match[2]
			}
		}
	}
	return fields, true
}

// Field returns the `version:` value of a document's frontmatter block, or "" when the
// file has no frontmatter or no version field.
func Field(content string) string {
	fields, ok := Fields(content)
	if !ok {
		return ""
	}
	return fields["version"]
}

// MissingFields returns the frontmatter keys a document does not carry, in the order the
// caller listed them. A document with no frontmatter block is missing all of them, and a
// key present with an empty value does not count as carried: a reader cannot tell how
// current `version:` is from an empty one.
func MissingFields(content string, required []string) []string {
	fields, _ := Fields(content)
	var missing []string
	for _, key := range required {
		if strings.TrimSpace(fields[key]) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

// Frontmatter renders a frontmatter block from ordered fields, closing it with the fence.
// It produces the shape Fields reads, so a caller that writes a document can read its own
// fields back.
func Frontmatter(fields ...[2]string) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, field := range fields {
		b.WriteString(field[0])
		b.WriteString(": ")
		b.WriteString(field[1])
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	return b.String()
}
