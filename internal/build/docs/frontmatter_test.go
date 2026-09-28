package docs

import (
	"strings"
	"testing"
)

// TestFieldsTable pins the frontmatter reader the version rule and the README check share.
func TestFieldsTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    map[string]string
		found   bool
	}{
		{
			name:    "a well-formed block",
			content: "---\ntype: Guide\ntitle: x\nversion: v3\n---\n\n# body\n",
			want:    map[string]string{"type": "Guide", "title": "x", "version": "v3"},
			found:   true,
		},
		{
			name:    "no block at all",
			content: "# body\n\nversion: v9\n",
			found:   false,
		},
		{
			// A document that opens a block and never closes it reads to the end: the
			// version rule has always read it that way, and a forgotten fence must not
			// exempt a document from versioning.
			name:    "an unterminated block reads to the end",
			content: "---\ntitle: x\nversion: v4\n\n# body\n",
			want:    map[string]string{"title": "x", "version": "v4"},
			found:   true,
		},
		{
			// A closed block ends the fields: a `version:` line in the body is prose,
			// not metadata.
			name:    "a closed block does not read the body",
			content: "---\ntitle: x\n---\n\nversion: v9\n",
			want:    map[string]string{"title": "x"},
			found:   true,
		},
		{
			name:    "the first value a key is given wins",
			content: "---\nversion: v1\nversion: v2\n---\n",
			want:    map[string]string{"version": "v1"},
			found:   true,
		},
		{
			name:    "a value keeps its trailing spaces",
			content: "---\nversion: v1 \n---\n",
			want:    map[string]string{"version": "v1 "},
			found:   true,
		},
		{
			name:    "an empty value is read as empty",
			content: "---\nversion:\n---\n",
			want:    map[string]string{"version": ""},
			found:   true,
		},
		{
			name:    "a line without a key is not a field",
			content: "---\n- a list item\nversion: v2\n---\n",
			want:    map[string]string{"version": "v2"},
			found:   true,
		},
		{
			name:    "a CRLF block reads the same",
			content: "---\r\nversion: v5\r\n---\r\n",
			want:    map[string]string{"version": "v5"},
			found:   true,
		},
		{
			// A carriage return that no LF follows is not a line ending in this format,
			// so it is part of the value. Nothing compares such a value as equal to a
			// clean one, so the effect is a stricter comparison rather than a lenient one.
			name:    "a stray carriage return is part of the value",
			content: "--- \nA:\r0",
			want:    map[string]string{"A": "\r0"},
			found:   true,
		},
		{
			name:    "an empty document",
			content: "",
			found:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields, found := Fields(tc.content)
			if found != tc.found {
				t.Fatalf("Fields(%q) found = %v, want %v", tc.content, found, tc.found)
			}
			if len(fields) != len(tc.want) {
				t.Fatalf("Fields(%q) = %q, want %q", tc.content, fields, tc.want)
			}
			for key, value := range tc.want {
				if fields[key] != value {
					t.Errorf("Fields(%q)[%q] = %q, want %q", tc.content, key, fields[key], value)
				}
			}
		})
	}
}

// TestFieldReadsTheVersion pins the accessor the version rule uses, including the cases a
// document can get wrong.
func TestFieldReadsTheVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "a version", content: "---\nversion: v7\n---\n", want: "v7"},
		{name: "no version key", content: "---\ntitle: x\n---\n", want: ""},
		{name: "no frontmatter", content: "version: v7\n", want: ""},
		{name: "an empty document", content: "", want: ""},
		{name: "a value with trailing spaces is kept verbatim", content: "---\nversion: v7 \n---\n", want: "v7 "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Field(tc.content); got != tc.want {
				t.Errorf("Field(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// TestMissingFields pins the check a package README is held to: a key the document does
// not carry, and a key it carries with nothing after the colon, are both missing — a
// reader cannot tell how current an empty version is.
func TestMissingFields(t *testing.T) {
	t.Parallel()

	required := []string{"type", "title", "description", "version"}

	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "a complete block",
			content: "---\ntype: Guide\ntitle: x\ndescription: y\nversion: v1\n---\n",
		},
		{
			name:    "an empty version",
			content: "---\ntype: Guide\ntitle: x\ndescription: y\nversion:\n---\n",
			want:    []string{"version"},
		},
		{
			name:    "a whitespace-only title",
			content: "---\ntype: Guide\ntitle:   \ndescription: y\nversion: v1\n---\n",
			want:    []string{"title"},
		},
		{
			name:    "no frontmatter at all",
			content: "# body\n",
			want:    []string{"type", "title", "description", "version"},
		},
		{
			name:    "the keys in the order asked for",
			content: "---\nversion: v1\n---\n",
			want:    []string{"type", "title", "description"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MissingFields(tc.content, required)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("MissingFields(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// TestFrontmatterRoundTrip pins that the renderer produces the shape the reader reads, so
// a caller that writes a document can read its own fields back.
func TestFrontmatterRoundTrip(t *testing.T) {
	t.Parallel()

	content := Frontmatter(
		[2]string{"type", "Guide"},
		[2]string{"title", "x — go-cask"},
		[2]string{"version", "v1"},
	) + "\n# body\n"

	fields, found := Fields(content)
	if !found {
		t.Fatalf("Fields did not read the rendered block:\n%s", content)
	}
	if fields["type"] != "Guide" || fields["title"] != "x — go-cask" || fields["version"] != "v1" {
		t.Errorf("rendered block read back as %q", fields)
	}
	if missing := MissingFields(content, []string{"type", "title", "version"}); len(missing) != 0 {
		t.Errorf("the rendered block is missing %q", missing)
	}
}
