package website

import (
	"strings"
	"testing"
)

// testFooter is a footer contract shaped like a real site's, so the rules below are
// exercised on values they do not own.
func testFooter() FooterSpec {
	return FooterSpec{
		Key:       "copyright",
		YearToken: "&copy;",
		Separator: "&middot; ",
		Label:     "Updated",
		CommitURL: "https://example.test/commit/",
		Forbidden: []string{"UTC", "Site built"},
	}
}

func TestFoldedScalar(t *testing.T) {
	t.Parallel()

	const document = "site_name: Example\n" +
		"copyright: >-\n" +
		"  &copy; Example &middot; <a href=\"/privacy/\">Privacy</a>\n" +
		"  &middot; <a href=\"/impressum/\">Impressum</a>\n" +
		"theme:\n" +
		"  name: material\n"

	cases := []struct {
		name     string
		document string
		key      string
		want     string
		found    bool
	}{
		{
			name:     "folded lines join with single spaces",
			document: document,
			key:      "copyright",
			want:     `&copy; Example &middot; <a href="/privacy/">Privacy</a> &middot; <a href="/impressum/">Impressum</a>`,
			found:    true,
		},
		{
			name:     "an unindented key ends the scalar",
			document: document,
			key:      "theme",
			found:    false,
		},
		{
			name:     "a nested block is not a folded scalar",
			document: "copyright:\n  text: literal\n",
			key:      "copyright",
			found:    false,
		},
		{
			name:     "an indented comment ends the value",
			document: "copyright: >-\n  one\n  # a note\n  two\nnext: 1\n",
			key:      "copyright",
			want:     "one",
			found:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := FoldedScalar(tc.document, tc.key)
			if found != tc.found {
				t.Fatalf("FoldedScalar(%q) found = %v, want %v", tc.key, found, tc.found)
			}
			if got != tc.want {
				t.Errorf("FoldedScalar(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestFooterLine(t *testing.T) {
	t.Parallel()

	const base = `&copy; Example &middot; <a href="/privacy/">Privacy</a>`
	spec := testFooter()

	cases := []struct {
		name     string
		date     string
		revision string
		want     string
	}{
		{
			name:     "a revision contributes its year and its linked date",
			date:     "2026-09-23",
			revision: "ab7deab",
			want: base[:len("&copy;")] + " 2026" + base[len("&copy;"):] +
				` &middot; Updated <a href="https://example.test/commit/ab7deab" title="commit ab7deab">2026-09-23</a>`,
		},
		{
			name: "a date without a revision still carries the year",
			date: "2026-09-23",
			want: base[:len("&copy;")] + " 2026" + base[len("&copy;"):],
		},
		{
			name: "no date leaves the base line alone",
			want: base,
		},
		{
			name: "a date that names no year adds nothing",
			date: "yesterday",
			want: base,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FooterLine(base, tc.date, tc.revision, spec)
			if got != tc.want {
				t.Errorf("FooterLine(%q, %q) = %q, want %q", tc.date, tc.revision, got, tc.want)
			}
		})
	}
}

func TestFooterFindings(t *testing.T) {
	t.Parallel()

	const base = `&copy; Example &middot; <a href="/privacy/">Privacy</a>`
	spec := testFooter()

	cases := []struct {
		name     string
		base     string
		date     string
		revision string
		want     string
	}{
		{
			name:     "a pinned line with a linked revision holds",
			base:     base,
			date:     "2026-09-23",
			revision: "ab7deab",
		},
		{
			name: "a build without a revision renders the base line",
			base: base,
		},
		{
			name: "a literal year in the base line is a finding",
			base: `&copy; 2026 Example`,
			want: "names the year 2026",
		},
		{
			name:     "a date that names no year is a finding",
			base:     base,
			date:     "yesterday",
			revision: "ab7deab",
			want:     "names no year",
		},
		{
			name:     "a zone label in the line is a finding",
			base:     `&copy; Example UTC`,
			date:     "2026-09-23",
			revision: "ab7deab",
			want:     `carries "UTC"`,
		},
		{
			// A build that cannot read a revision renders the base line and is
			// not a finding: the footer degrades rather than guessing a value.
			name: "no readable revision is not a finding",
			base: base,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := FooterFindings(tc.base, tc.date, tc.revision, spec)
			if tc.want == "" {
				if len(findings) != 0 {
					t.Fatalf("FooterFindings reported %q, want none", findings)
				}
				return
			}
			if len(findings) == 0 {
				t.Fatalf("FooterFindings reported nothing, want a finding about %q", tc.want)
			}
			if !containsFindings(findings, tc.want) {
				t.Errorf("FooterFindings reported %q, want a finding containing %q", findings, tc.want)
			}
		})
	}
}

// TestFooterRendersTheRevisionAsATarget pins the shape of the rendered line, which
// is what a reader sees: the revision lives in the link target, the date is the
// link text, the label sits outside the anchor, and the line is one line.
func TestFooterRendersTheRevisionAsATarget(t *testing.T) {
	t.Parallel()

	spec := testFooter()
	const revision = "ab7deab"
	const date = "2026-09-23"
	const base = `&copy; Example &middot; <a href="/privacy/">Privacy</a>`
	line := FooterLine(base, date, revision, spec)

	if strings.Contains(VisibleText(line), revision) {
		t.Errorf("the revision %s is visible text in %q; it belongs in the link target", revision, line)
	}
	if !strings.Contains(line, spec.Label+" <a href=") {
		t.Errorf("the label %q does not precede the anchor in %q", spec.Label, line)
	}
	if !strings.Contains(line, ">"+date+"</a>") {
		t.Errorf("the date %q is not the link text in %q", date, line)
	}
	if strings.ContainsAny(line, "\r\n") {
		t.Errorf("the footer is more than one line: %q", line)
	}
	if findings := FooterFindings(base, date, revision, spec); len(findings) != 0 {
		t.Errorf("the line the rule composes fails the rule: %q", findings)
	}
}

// containsFindings reports whether any finding carries the wanted fragment.
func containsFindings(findings []string, want string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, want) {
			return true
		}
	}
	return false
}

func TestCheckSourceGuards(t *testing.T) {
	t.Parallel()

	guards := []SourceGuard{
		{Path: "a.py", Must: []string{"--selftest"}, MustNot: []string{"SITE_REVISION"}},
		{Path: "b.yml", MustNot: []string{"custom_dir"}},
		{Path: "missing.sh", Must: []string{"--selftest"}},
	}
	contents := map[string]string{
		"a.py":  "def main():\n    return '--selftest'\n",
		"b.yml": "theme:\n  name: material\n",
	}

	findings := CheckSourceGuards(contents, guards)
	if len(findings) != 1 {
		t.Fatalf("CheckSourceGuards reported %q, want only the unread file", findings)
	}
	if !strings.Contains(findings[0], "missing.sh") {
		t.Errorf("finding %q does not name the file the caller never read", findings[0])
	}

	returned := CheckSourceGuards(map[string]string{"a.py": "SITE_REVISION = 1"}, guards[:1])
	if len(returned) != 2 {
		t.Fatalf("CheckSourceGuards reported %q, want the missing and the returned text", returned)
	}
}

func TestCheckAbsentPaths(t *testing.T) {
	t.Parallel()

	existing := map[string]bool{"website/overrides": true}
	findings := CheckAbsentPaths([]string{"website/overrides", "website/partials"}, func(path string) bool {
		return existing[path]
	})
	if len(findings) != 1 || !strings.Contains(findings[0], "website/overrides") {
		t.Fatalf("CheckAbsentPaths reported %q, want only the path that still exists", findings)
	}
}
