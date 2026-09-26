package docs

import (
	"strings"
	"testing"
)

// FuzzFields checks the frontmatter reader the version rule and the README check share: it
// never panics on arbitrary bytes, it is deterministic, and a field it does not find is
// reported as missing — the two must not disagree, or a document could be judged on a
// version the reader would not report.
func FuzzFields(f *testing.F) {
	f.Add("---\ntype: Guide\ntitle: x\ndescription: y\nversion: v1\n---\n")
	f.Add("")
	f.Add("---\n")
	f.Add("---\n---\n")
	f.Add("---\nversion:\n---\n")
	f.Add("# body\nversion: v9\n")
	f.Add("---\r\nversion: v1\r\n---\r\n")

	required := []string{"type", "title", "description", "version"}

	f.Fuzz(func(t *testing.T, content string) {
		fields, found := Fields(content)
		if again, foundAgain := Fields(content); foundAgain != found || len(again) != len(fields) {
			t.Fatalf("Fields(%q) is not deterministic", content)
		}
		for key, value := range fields {
			if strings.ContainsAny(key, ":\r\n") {
				t.Fatalf("Fields(%q) read %q as a key", content, key)
			}
			// A value is the text of one line. The reader normalises a CRLF ending, so a
			// value never carries a line break of its own; a stray carriage return that
			// no LF follows is not a line ending in this format and is read verbatim,
			// which can only ever make a comparison stricter.
			if strings.Contains(value, "\n") {
				t.Fatalf("Fields(%q) read %q as a value, which carries a line break", content, value)
			}
		}

		missing := MissingFields(content, required)
		for _, key := range required {
			carried := strings.TrimSpace(fields[key]) != ""
			if !found {
				carried = false
			}
			isMissing := false
			for _, name := range missing {
				if name == key {
					isMissing = true
				}
			}
			if carried == isMissing {
				t.Fatalf("MissingFields(%q) reports %q as missing=%v while the reader carries it=%v",
					content, key, isMissing, carried)
			}
		}
	})
}

// FuzzFrontmatterRoundTrip checks that what the renderer writes, the reader reads: the
// fields are the document's own words, and a value the reader could not carry back is one
// the renderer was never asked to write — a line ending inside a value, or one that starts
// or ends with the whitespace the writer separates a field from its value with.
func FuzzFrontmatterRoundTrip(f *testing.F) {
	f.Add("Guide", "x — go-cask", "v1")
	f.Add("", "", "")
	f.Add("Guide", "a: b", "v2")
	f.Add("Guide", "line\nbreak", "v3")

	representable := func(value string) bool {
		return !strings.ContainsAny(value, "\r\n") && strings.TrimSpace(value) == value
	}

	f.Fuzz(func(t *testing.T, kind, title, version string) {
		if !representable(kind) || !representable(title) || !representable(version) {
			return
		}
		content := Frontmatter([2]string{"type", kind}, [2]string{"title", title}, [2]string{"version", version})
		fields, found := Fields(content)
		if !found {
			t.Fatalf("Fields did not read the rendered block:\n%s", content)
		}
		if fields["type"] != kind || fields["title"] != title || fields["version"] != version {
			t.Fatalf("rendered %q, %q, %q and read back %q", kind, title, version, fields)
		}
	})
}
