// Package website pins the contracts of the published documentation site that
// the Go gate can check without running MkDocs.
//
// The site is rendered by MkDocs with Jinja2 templates, and no gate step runs
// MkDocs, so before issue #220 the footer's build-provenance line had no test
// at all: dropping or changing it could not fail anything. This package checks
// the two artifacts in Go, deterministically and without a Python interpreter:
//
//   - website/overrides/partials/copyright.html — the guarded
//     build-provenance block is extracted and its Jinja2 constructs are
//     translated into their html/template equivalents, so the exact rendered
//     text is pinned for a CI build, for a date with no revision, for a
//     revision with no date, and for neither; and
//   - website/macros.py — the hook must return the date an ISO 8601 timestamp
//     names instead of converting it between zones.
//
// The translation is fail-closed: a Jinja statement outside the small set the
// footer uses fails the test instead of being rendered as literal text, and a
// translation that does not parse fails it too. A zone label coming back to
// the footer fails the pinned text, which is the regression #220 exists to
// prevent.
package website

import (
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const (
	partialPath = "website/overrides/partials/copyright.html"
	macrosPath  = "website/macros.py"

	// siteBuildMarker and copyrightMarker are the class names the footer
	// partial gives the two blocks this package renders.
	siteBuildMarker = "md-copyright__site-build"
	copyrightMarker = "md-copyright__highlight"
)

var (
	// jinjaStatement matches one `{% ... %}` statement, hyphens included.
	jinjaStatement = regexp.MustCompile(`\{%-?.*?-?%\}`)
	// statementKind reports whether a statement opens or closes a block.
	statementKind = regexp.MustCompile(`\{%-?\s*(if|endif)\b`)
	// yearLiteral matches a four-digit year, which the footer must derive from
	// the deployed revision's date instead of carrying as a literal.
	yearLiteral = regexp.MustCompile(`\b(?:19|20)\d\d\b`)
)

// statementTranslation maps every Jinja statement the footer's two blocks are
// allowed to use to its html/template equivalent. Jinja writes boolean
// conditions infix (`a and b`) and closes blocks with `endif`, while Go
// template functions are prefix (`and a b`) and close with `end`, so the
// mapping has to spell the conditions out. A statement outside this set fails
// the render rather than being translated by guesswork.
var statementTranslation = map[string]string{
	"{%- if site_build_date or site_revision %}":  "{{- if or .Date .Revision }}",
	"{%- if site_build_date and site_revision %}": "{{- if and .Date .Revision }}",
	"{% if site_build_date %}":                    "{{ if .Date }}",
	"{%- if site_build_date %}":                   "{{- if .Date }}",
	"{%- if site_revision %}":                     "{{- if .Revision }}",
	"{% endif %}":                                 "{{ end }}",
	"{% endif -%}":                                "{{ end -}}",
	"{%- endif %}":                                "{{- end }}",
}

// provenance is the pair of values website/macros.py publishes for the footer.
type provenance struct {
	Date     string
	Revision string
}

// repoFile returns the contents of a repository-relative file. The root is
// resolved from this source file, not from the working directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test source to resolve the repository root")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", partialPath, err)
	}
	return string(data)
}

// footerBlock returns the source lines of the element whose opening tag is on
// the first line containing marker. With guarded set, the range is widened to
// the `{% if %}`/`{% endif %}` pair enclosing the element, because that pair is
// what omits the element when both values are empty.
func footerBlock(t *testing.T, source, marker string, guarded bool) string {
	t.Helper()
	lines := strings.Split(source, "\n")

	anchor := -1
	for i, line := range lines {
		if strings.Contains(line, marker) {
			anchor = i
			break
		}
	}
	if anchor < 0 {
		t.Fatalf("%s: no element containing %q", partialPath, marker)
	}

	depth := 0
	end := -1
	for i := anchor; i < len(lines); i++ {
		depth += strings.Count(lines[i], "<div")
		depth -= strings.Count(lines[i], "</div>")
		if depth == 0 {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatalf("%s: the element containing %q is never closed", partialPath, marker)
	}
	if !guarded {
		return strings.Join(lines[anchor:end+1], "\n")
	}

	// Walk the statements above the element to find the block it sits in.
	open := 0
	start := -1
	for i := 0; i < anchor; i++ {
		for _, m := range statementKind.FindAllStringSubmatch(lines[i], -1) {
			if m[1] == "if" {
				open++
				if open == 1 {
					start = i
				}
				continue
			}
			open--
			if open == 0 {
				start = -1
			}
		}
	}
	if open != 1 || start < 0 {
		t.Fatalf("%s: the element containing %q is not enclosed by exactly one if block", partialPath, marker)
	}
	for i := end + 1; i < len(lines); i++ {
		for _, m := range statementKind.FindAllStringSubmatch(lines[i], -1) {
			if m[1] == "if" {
				open++
			} else {
				open--
			}
			if open == 0 {
				return strings.Join(lines[start:i+1], "\n")
			}
		}
	}
	t.Fatalf("%s: the if block around %q is not closed", partialPath, marker)
	return ""
}

// renderFooter evaluates a footer block the way MkDocs would, by translating
// the Jinja2 the block uses into html/template syntax and executing it with
// html/template. Whitespace is collapsed so the assertions pin the visible
// text, not the template's indentation.
func renderFooter(t *testing.T, block string, values provenance) string {
	t.Helper()
	translated := block
	for _, statement := range jinjaStatement.FindAllString(block, -1) {
		goStatement, ok := statementTranslation[statement]
		if !ok {
			t.Fatalf("%s: unsupported Jinja statement %s; add its html/template translation to statementTranslation", partialPath, statement)
		}
		translated = strings.ReplaceAll(translated, statement, goStatement)
	}
	translated = strings.NewReplacer(
		"site_build_date[:4]", "slice .Date 0 4",
		"site_build_date", ".Date",
		"site_revision", ".Revision",
	).Replace(translated)
	if strings.Contains(translated, "{%") || strings.Contains(translated, "%}") {
		t.Fatalf("%s: a Jinja statement survived the translation:\n%s", partialPath, translated)
	}

	tmpl, err := template.New("footer-block").Parse(translated)
	if err != nil {
		t.Fatalf("parse the footer block translated from %s: %v\ntranslated template:\n%s", partialPath, err, translated)
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, values); err != nil {
		t.Fatalf("render the footer block translated from %s: %v", partialPath, err)
	}
	rendered := strings.Join(strings.Fields(out.String()), " ")
	// The extracted block is a single element; compare what it renders inside
	// its wrapper div, so the pinned text is the visible footer line.
	if open := strings.Index(rendered, ">"); strings.HasPrefix(rendered, "<div") && open >= 0 {
		rendered = strings.TrimSuffix(rendered[open+1:], "</div>")
	}
	return strings.TrimSpace(rendered)
}

// TestFooterBuildLinePinsRenderedText pins the exact footer line for every
// combination of the two published values, so a zone label, a lost revision,
// or a guessed date fails here rather than on the published site.
func TestFooterBuildLinePinsRenderedText(t *testing.T) {
	source := repoFile(t, partialPath)
	if strings.Contains(source, "UTC") {
		t.Errorf("%s: the footer must not label its date with a zone; at day granularity no zone is correct", partialPath)
	}
	block := footerBlock(t, source, siteBuildMarker, true)

	cases := []struct {
		name   string
		values provenance
		want   string
	}{
		{
			name:   "ci variables",
			values: provenance{Date: "2026-09-23", Revision: "ab7deab"},
			want:   "Site built 2026-09-23 from revision <code>ab7deab</code>",
		},
		{
			name:   "date without revision",
			values: provenance{Date: "2026-09-23"},
			want:   "Site built 2026-09-23",
		},
		{
			name:   "revision without date",
			values: provenance{Revision: "ab7deab"},
			want:   "Site built revision <code>ab7deab</code>",
		},
		{
			name: "neither",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderFooter(t, block, tc.values)
			if got != tc.want {
				t.Errorf("footer build line = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "UTC") {
				t.Errorf("footer build line %q carries a zone label", got)
			}
		})
	}
}

// TestFooterCopyrightYearFollowsBuildDate checks that the imprint's year comes
// from the same date as the build line rather than from a literal, and that an
// unknown date drops the year instead of showing a stale one.
func TestFooterCopyrightYearFollowsBuildDate(t *testing.T) {
	source := repoFile(t, partialPath)
	if literal := yearLiteral.FindString(source); literal != "" {
		t.Errorf("%s: the imprint year %s is a literal; it must derive from the deployed revision's date", partialPath, literal)
	}
	block := footerBlock(t, source, copyrightMarker, false)

	cases := []struct {
		date string
		want string
	}{
		{date: "2026-09-23", want: "&copy; 2026 Daniel Mundt"},
		{date: "1999-01-02", want: "&copy; 1999 Daniel Mundt"},
		{date: "", want: "&copy; Daniel Mundt"},
	}
	for _, tc := range cases {
		got := renderFooter(t, block, provenance{Date: tc.date})
		if !strings.Contains(got, tc.want) {
			t.Errorf("footer imprint line with date %q = %q, want it to contain %q", tc.date, got, tc.want)
		}
	}
}

// TestMacrosDateIsNotZoneConverted pins the hook side of issue #220: the date
// is taken as the deployed revision records it, so a commit made at
// `00:30 +02:00` on the 24th reports the 24th like every other surface does.
// These are source assertions, not Python semantics: the gate has no Python
// runner, and the behaviour itself is exercised by `mkdocs build --strict`.
func TestMacrosDateIsNotZoneConverted(t *testing.T) {
	source := repoFile(t, macrosPath)

	if strings.Contains(source, "astimezone") {
		t.Errorf("%s: the build date must be read from the deployed revision, not converted between zones", macrosPath)
	}
	if !strings.Contains(source, "return parsed.date()") {
		t.Errorf("%s: _parse_date must return the date the timestamp names", macrosPath)
	}
	docstring := strings.SplitN(source, `"""`, 3)
	if len(docstring) < 3 || !strings.Contains(docstring[1], "deployed revision") {
		t.Errorf("%s: the module docstring must state that the date is sourced in the deployed revision", macrosPath)
	}
}
