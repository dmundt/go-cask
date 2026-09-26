package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a document under a temporary repository root and returns the
// root and the File to check, so link resolution is exercised against a real
// filesystem rather than a mock.
func fixture(t *testing.T, name, content string, extra map[string]string) (string, File) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, name, content)
	for path, body := range extra {
		writeFile(t, root, path, body)
	}
	return root, File{Path: name, Content: content}
}

// writeFile creates a repository-relative file, including its directories.
func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// messages flattens findings to their messages, which is what most cases assert.
func messages(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, finding.Message)
	}
	return out
}

// TestCheckMarkdownTable pins each rule and — more importantly — the cases that
// must NOT be reported. A documentation check that cries wolf on a Go generic
// call or an inline code span is one somebody disables.
func TestCheckMarkdownTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		extra   map[string]string
		want    []string
	}{
		{
			name:    "clean prose",
			content: "# Title\n\nA paragraph with a [link](./other.md).\n",
			extra:   map[string]string{"other.md": "# Other\n"},
		},
		{
			name:    "raw HTML in prose is reported",
			content: "# Title\n\n<div>block</div>\n",
			want:    []string{"raw HTML is not allowed: <div>", "raw HTML is not allowed: </div>"},
		},
		{
			name:    "an HTML comment is reported",
			content: "# Title\n\n<!-- hidden -->\n",
			want:    []string{"raw HTML is not allowed: <!--"},
		},
		{
			name:    "a self-closing tag is reported",
			content: "# Title\n\n<hr/>\n",
			want:    []string{"raw HTML is not allowed: <hr/>"},
		},
		{
			// Pinned because it is surprising: `br` is genuinely NOT in the HTML
			// vocabulary the rule matches (the list runs `blockquote` -> `canvas`),
			// so `<br>` has never failed the gate. This reproduces the rule the
			// gate actually enforced; widening the list is a deliberate change, not
			// a tidy-up.
			name:    "a tag outside the vocabulary is not reported",
			content: "# Title\n\nline one<br>line two\n",
		},
		{
			name:    "a tag inside an inline code span is not raw HTML",
			content: "# Title\n\nWrite `<div>` to make a block.\n",
		},
		{
			name:    "a tag inside a fenced block is not raw HTML",
			content: "# Title\n\n```text\n<div>sample</div>\n```\n",
		},
		{
			name:    "a Go generic call is not a link",
			content: "# Title\n\nUse `New[T](encode, decode)` for this.\n",
		},
		{
			name:    "a Go generic call in a fenced block is not a link",
			content: "# Title\n\n```go\nstore := cas.New[T](a, b)\n```\n",
		},
		{
			name:    "a forbidden fence is reported",
			content: "# Title\n\n```html\n<p>x</p>\n```\n",
			want:    []string{"HTML/XML/SVG code fences are not allowed"},
		},
		{
			name:    "a forbidden fence is reported for svg",
			content: "# Title\n\n```svg\n<svg/>\n```\n",
			want:    []string{"HTML/XML/SVG code fences are not allowed"},
		},
		{
			name:    "a language that merely starts with html is fine",
			content: "# Title\n\n```html5\nx\n```\n",
		},
		{
			name:    "a broken relative link is reported",
			content: "# Title\n\nSee [gone](./missing.md).\n",
			want:    []string{"./missing.md"},
		},
		{
			name:    "a link that exists is not reported",
			content: "# Title\n\nSee [there](../other.md).\n",
			extra:   map[string]string{"../other.md": "# Other\n"},
		},
		{
			name:    "a fragment-only link is not a file reference",
			content: "# Title\n\nSee [section](#title).\n",
		},
		{
			name:    "an absolute URL is not a file reference",
			content: "# Title\n\nSee [site](https://example.com/x.md).\n",
		},
		{
			name:    "a mailto link is not a file reference",
			content: "# Title\n\nMail [us](mailto:x@example.com).\n",
		},
		{
			name:    "an image is not checked as a link",
			content: "# Title\n\n![alt](./missing.png)\n",
		},
		{
			name:    "an image that exists is not reported",
			content: "# Title\n\n![alt](./there.png)\n",
			extra:   map[string]string{"there.png": "x"},
		},
		{
			name:    "an angle-bracketed link with spaces resolves",
			content: "# Title\n\nSee [there](<./with space.md>).\n",
			extra:   map[string]string{"with space.md": "# Spaced\n"},
		},
		{
			name:    "a percent-escaped target resolves to the decoded path",
			content: "# Title\n\nSee [there](./with%20space.md).\n",
			extra:   map[string]string{"with space.md": "# Spaced\n"},
		},
		{
			name:    "a root-absolute link resolves from the repository root",
			content: "# Title\n\nSee [root](/cas/README.md).\n",
			extra:   map[string]string{"cas/README.md": "# Cas\n"},
		},
		{
			name:    "a root-absolute link to nothing is reported",
			content: "# Title\n\nSee [root](/nope/README.md).\n",
			want:    []string{"/nope/README.md"},
		},
		{
			name:    "a link definition target is checked",
			content: "# Title\n\nSee [there][ref].\n\n[ref]: ./missing.md\n",
			want:    []string{"./missing.md"},
		},
		{
			name:    "a link definition that exists is not reported",
			content: "# Title\n\nSee [there][ref].\n\n[ref]: ./other.md\n",
			extra:   map[string]string{"other.md": "# Other\n"},
		},
		{
			name:    "a directory target exists",
			content: "# Title\n\nSee [dir](./sub/).\n",
			extra:   map[string]string{"sub/x.md": "x"},
		},
		{
			name:    "a link with a fragment resolves the path part",
			content: "# Title\n\nSee [there](./other.md#section).\n",
			extra:   map[string]string{"other.md": "# Other\n"},
		},
		{
			name:    "a link with a broken path and a fragment is reported",
			content: "# Title\n\nSee [there](./missing.md#section).\n",
			want:    []string{"./missing.md#section"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, file := fixture(t, "doc.md", test.content, test.extra)
			got := messages(CheckMarkdown(root, file))
			if len(got) != len(test.want) {
				t.Fatalf("CheckMarkdown found %d findings %q, want %d %q", len(got), got, len(test.want), test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("finding %d = %q, want %q (all: %q)", i, got[i], test.want[i], got)
				}
			}
		})
	}
}

// TestFindingsCarryLineNumbers pins the position a reader needs: a finding that
// names no line is only marginally better than no finding at all.
func TestFindingsCarryLineNumbers(t *testing.T) {
	t.Parallel()

	content := "# Title\n\nfine\n\n<div>x</div>\n"
	root, file := fixture(t, "doc.md", content, nil)
	findings := CheckMarkdown(root, file)
	if len(findings) == 0 {
		t.Fatal("no findings for a document with raw HTML")
	}
	for _, finding := range findings {
		if finding.Line != 5 {
			t.Errorf("finding %q reports line %d, want 5", finding.Message, finding.Line)
		}
	}
}

// TestCRLFAndLFAgree pins that a Windows checkout reports the same findings as a
// Linux one, which matters because this repository is developed on Windows and
// gated on Linux.
func TestCRLFAndLFAgree(t *testing.T) {
	t.Parallel()

	const lf = "# Title\n\nfine\n\n<div>x</div>\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")

	rootLF, fileLF := fixture(t, "lf.md", lf, nil)
	rootCRLF, fileCRLF := fixture(t, "crlf.md", crlf, nil)
	gotLF := CheckMarkdown(rootLF, fileLF)
	gotCRLF := CheckMarkdown(rootCRLF, fileCRLF)

	if len(gotLF) != len(gotCRLF) {
		t.Fatalf("LF found %d findings, CRLF found %d", len(gotLF), len(gotCRLF))
	}
	for i := range gotLF {
		if gotLF[i].Line != gotCRLF[i].Line || gotLF[i].Message != gotCRLF[i].Message {
			t.Errorf("finding %d differs: LF %+v, CRLF %+v", i, gotLF[i], gotCRLF[i])
		}
	}
}

// TestChangelogRules pins the three structure rules and the clean case.
func TestChangelogRules(t *testing.T) {
	t.Parallel()

	valid := `# Changelog

## [Unreleased]

### Added

- something

## [v1.2.0] - 2026-01-01

### Fixed

- a fix

[Unreleased]: https://github.com/o/r/compare/v1.2.0...HEAD
[v1.2.0]: https://github.com/o/r/compare/v1.1.0...v1.2.0
`

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "a well-formed changelog has no findings",
			content: valid,
		},
		{
			name: "a release heading without a link definition is reported",
			content: `# Changelog

## [v1.2.0] - 2026-01-01

### Fixed

- a fix
`,
			want: []string{"release heading [v1.2.0] has no [v1.2.0]: link definition"},
		},
		{
			name: "a repeated section in one release is reported",
			content: `# Changelog

## [Unreleased]

### Security

- one

### Security

- two

[Unreleased]: https://github.com/o/r/compare/v1.2.0...HEAD
`,
			want: []string{"### Security repeats in the Unreleased section"},
		},
		{
			name: "the same section in two different releases is fine",
			content: `# Changelog

## [Unreleased]

### Fixed

- one

## [v1.2.0] - 2026-01-01

### Fixed

- two

[Unreleased]: https://github.com/o/r/compare/v1.2.0...HEAD
[v1.2.0]: https://github.com/o/r/compare/v1.1.0...v1.2.0
`,
		},
		{
			name: "an unreleased range from an older tag is reported",
			content: `# Changelog

## [Unreleased]

### Added

- x

## [v1.2.0] - 2026-01-01

### Fixed

- y

[Unreleased]: https://github.com/o/r/compare/v1.1.0...HEAD
[v1.2.0]: https://github.com/o/r/compare/v1.1.0...v1.2.0
`,
			want: []string{"[Unreleased] compares from v1.1.0, but the newest released section is v1.2.0"},
		},
		{
			name: "an unreleased range from the newest release is fine",
			content: `# Changelog

## [Unreleased]

### Added

- x

## [v1.2.0] - 2026-01-01

### Fixed

- y

[Unreleased]: https://github.com/o/r/compare/v1.2.0...HEAD
[v1.2.0]: https://github.com/o/r/compare/v1.1.0...v1.2.0
`,
		},
		{
			name: "a section heading before any release is not a duplicate",
			content: `# Changelog

### Added

- stray

## [Unreleased]

### Added

- real

[Unreleased]: https://github.com/o/r/compare/v1.2.0...HEAD
`,
		},
		{
			name:    "an empty changelog has no findings",
			content: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := messages(CheckChangelog("CHANGELOG.md", test.content))
			if len(got) != len(test.want) {
				t.Fatalf("CheckChangelog found %q, want %q", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("finding %d = %q, want %q", i, got[i], test.want[i])
				}
			}
		})
	}
}

// TestCheckMarkdownDispatchesChangelog pins that the changelog rules are applied
// to CHANGELOG.md through the general entry point, so a caller cannot check the
// prose and skip the structure by accident.
func TestCheckMarkdownDispatchesChangelog(t *testing.T) {
	t.Parallel()

	content := "# Changelog\n\n## [v9.9.9] - 2026-01-01\n\n### Fixed\n\n- x\n"
	root, file := fixture(t, "CHANGELOG.md", content, nil)
	got := messages(CheckMarkdown(root, file))
	if len(got) != 1 || !strings.Contains(got[0], "has no [v9.9.9]: link definition") {
		t.Fatalf("CheckMarkdown on CHANGELOG.md found %q, want the missing link definition", got)
	}

	// The same content under another name is prose only.
	root2, file2 := fixture(t, "notes.md", content, nil)
	if got := messages(CheckMarkdown(root2, file2)); len(got) != 0 {
		t.Errorf("CheckMarkdown on notes.md found %q, want nothing", got)
	}
}

// TestReportIsSortedAndDeduplicated pins the shape of the gate's output: stable
// order so two runs diff cleanly, and no repeated line so a broken link reported
// by two patterns does not look like two problems.
func TestReportIsSortedAndDeduplicated(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Path: "b.md", Line: 2, Message: "second"},
		{Path: "a.md", Line: 1, Message: "first"},
		{Path: "b.md", Line: 2, Message: "second"},
	}
	got := Report(findings)
	want := []string{"a.md:1: first", "b.md:2: second"}
	if len(got) != len(want) {
		t.Fatalf("Report returned %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Report returned %q, want %q", got, want)
		}
	}
}

// TestCheckMermaid pins the balance rule, including the cases that must NOT be
// reported: a fenced diagram is ordinary documentation, and an indented fence is
// counted by neither side, so it can neither satisfy nor break the rule.
func TestCheckMermaid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "a balanced mermaid block",
			content: "# T\n\n```mermaid\nflowchart TD\n  a --> b\n```\n",
			want:    0,
		},
		{
			name:    "an unclosed mermaid block",
			content: "# T\n\n```mermaid\nflowchart TD\n  a --> b\n",
			want:    1,
		},
		{
			name:    "two balanced blocks",
			content: "# T\n\n```mermaid\na\n```\n\ntext\n\n```mermaid\nb\n```\n",
			want:    0,
		},
		{
			name:    "one of two unclosed",
			content: "# T\n\n```mermaid\na\n```\n\n```mermaid\nb\n",
			want:    1,
		},
		{
			name:    "no mermaid at all",
			content: "# T\n\n```go\nx := 1\n```\n",
			want:    0,
		},
		{
			name:    "an indented fence counts for neither side",
			content: "# T\n\n  ```mermaid\n  a --> b\n  ```\n",
			want:    0,
		},
		{
			name:    "a mermaid opener closed by an indented fence is unbalanced",
			content: "# T\n\n```mermaid\na\n  ```\n",
			want:    1,
		},
		{
			name:    "a fence with a trailing space is not an opener",
			content: "# T\n\n```mermaid \na\n```\n",
			want:    0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, file := fixture(t, "doc.md", test.content, nil)
			var findings []Finding
			for _, finding := range CheckMarkdown(t.TempDir(), file) {
				if strings.Contains(finding.Message, "unbalanced mermaid") {
					findings = append(findings, finding)
				}
			}
			if len(findings) != test.want {
				t.Fatalf("found %d mermaid findings %q, want %d", len(findings), findings, test.want)
			}
		})
	}
}

// TestFindingStringOmitsALineWhenThereIsNone pins the rendering of a finding
// that is not tied to a line, so it does not print a misleading `:0`.
func TestFindingStringOmitsALineWhenThereIsNone(t *testing.T) {
	t.Parallel()

	if got := (Finding{Path: "a.md", Message: "x"}).String(); got != "a.md: x" {
		t.Errorf("Finding.String() = %q, want %q", got, "a.md: x")
	}
	if got := (Finding{Path: "a.md", Line: 3, Message: "x"}).String(); got != "a.md:3: x" {
		t.Errorf("Finding.String() = %q, want %q", got, "a.md:3: x")
	}
}
