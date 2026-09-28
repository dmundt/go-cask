package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleChangelog is a changelog with the shapes the real one uses: an
// Unreleased section, released sections, and the trailing link definitions.
const sampleChangelog = `# Changelog

## [Unreleased]

### Added

- something unreleased

## [v1.2.0] - 2026-01-02

### Fixed

- a fix

- another fix

### Changed

- a change

## [v1.1.0] - 2026-01-01

### Added

- older

[Unreleased]: https://github.com/dmundt/go-cask/compare/v1.2.0...HEAD
[v1.2.0]: https://github.com/dmundt/go-cask/compare/v1.1.0...v1.2.0
[v1.1.0]: https://github.com/dmundt/go-cask/compare/v1.0.0...v1.1.0
`

// TestSection pins the extraction: the section runs from its heading to the next
// release heading, the heading itself is dropped, and the link definitions at the
// end of the file are not part of any section.
func TestSection(t *testing.T) {
	t.Parallel()

	section, err := Section(sampleChangelog, "v1.2.0")
	if err != nil {
		t.Fatalf("Section: %v", err)
	}
	want := []string{"", "### Fixed", "", "- a fix", "", "- another fix", "", "### Changed", "", "- a change"}
	if strings.Join(section, "\n") != strings.Join(want, "\n") {
		t.Errorf("Section = %q, want %q", section, want)
	}
}

// TestSectionStopsAtTheNextRelease pins the boundary: without it a release note
// would carry every older release with it.
func TestSectionStopsAtTheNextRelease(t *testing.T) {
	t.Parallel()

	section, err := Section(sampleChangelog, "v1.2.0")
	if err != nil {
		t.Fatalf("Section: %v", err)
	}
	joined := strings.Join(section, "\n")
	for _, forbidden := range []string{"older", "v1.1.0", "[Unreleased]"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("section for v1.2.0 contains %q, which belongs to another section", forbidden)
		}
	}
}

// TestSectionErrorsOnAMissingTag pins that an unknown tag is an error rather than
// empty notes: publishing an empty release note is worse than failing.
func TestSectionErrorsOnAMissingTag(t *testing.T) {
	t.Parallel()

	if _, err := Section(sampleChangelog, "v9.9.9"); err == nil {
		t.Error("Section accepted a tag the changelog does not have")
	}
}

// TestSectionMatchesAPrefix pins the deliberate looseness: `v1.2` finds
// `## [v1.2.0]`. The shell helper matched a substring, and that is preserved so a
// prefix query behaves as it did.
func TestSectionMatchesAPrefix(t *testing.T) {
	t.Parallel()

	section, err := Section(sampleChangelog, "v1.2")
	if err != nil {
		t.Fatalf("Section with a prefix: %v", err)
	}
	if len(section) == 0 {
		t.Fatal("Section returned nothing for a prefix that should match")
	}
}

// TestReshape pins the heading promotion and its strictness: only a line that is
// exactly a change group is promoted, so a trailing space leaves it alone — the
// same rule the shell helper applied with `/^### Fixed$/`.
func TestReshape(t *testing.T) {
	t.Parallel()

	section := []string{
		"### Added", "- a", "",
		"### Fixed", "- b", "",
		"### Security", "- c", "",
		"### Fixed ", "- not promoted",
		"### Something else", "- untouched",
		"### Fixed: a suffix", "- untouched",
	}
	got := strings.Join(Reshape(section), "\n")
	want := strings.Join([]string{
		"## Added", "- a", "",
		"## Fixed", "- b", "",
		"## Security", "- c", "",
		"### Fixed ", "- not promoted",
		"### Something else", "- untouched",
		"### Fixed: a suffix", "- untouched",
	}, "\n")
	if got != want {
		t.Errorf("Reshape =\n%q\nwant\n%q", got, want)
	}
}

// TestReshapeDropsALeadingHeading pins the guard the shell helper carried: a
// section that still has its release heading must not emit it as note text.
func TestReshapeDropsALeadingHeading(t *testing.T) {
	t.Parallel()

	got := Reshape([]string{"## [v1.2.0] - 2026-01-02", "", "### Fixed", "- a fix"})
	if strings.Contains(strings.Join(got, "\n"), "v1.2.0") {
		t.Errorf("Reshape kept the release heading: %q", got)
	}
}

// TestNotes pins the whole shape: the reshaped body, a blank line, and the
// compare link — the exact text a release note carries.
func TestNotes(t *testing.T) {
	t.Parallel()

	section, err := Section(sampleChangelog, "v1.2.0")
	if err != nil {
		t.Fatalf("Section: %v", err)
	}
	got := Notes(section, "v1.1.0", "v1.2.0")
	want := "\n## Fixed\n\n- a fix\n\n- another fix\n\n## Changed\n\n- a change\n\n" +
		"**Full Changelog**: https://github.com/dmundt/go-cask/compare/v1.1.0...v1.2.0\n"
	if got != want {
		t.Errorf("Notes =\n%q\nwant\n%q", got, want)
	}
}

// TestBuildIsSectionPlusNotes pins that the convenience does both steps, so a
// caller cannot extract without rendering or the other way round.
func TestBuildIsSectionPlusNotes(t *testing.T) {
	t.Parallel()

	built, err := Build(sampleChangelog, "v1.2.0", "v1.1.0")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	section, err := Section(sampleChangelog, "v1.2.0")
	if err != nil {
		t.Fatalf("Section: %v", err)
	}
	if built != Notes(section, "v1.1.0", "v1.2.0") {
		t.Error("Build disagrees with Section followed by Notes")
	}
}

// TestNotesLinkForm pins the exact spelling of the compare line, because the
// gate checks for it with a substring and the two drifted apart once already: the
// line is `**Full Changelog**: <url>`, so a check for the words immediately
// followed by a colon never matches.
func TestNotesLinkForm(t *testing.T) {
	t.Parallel()

	notes := Notes([]string{"- a fix"}, "v1.0.0", "v1.1.0")
	if !strings.Contains(notes, "**Full Changelog**: ") {
		t.Errorf("notes do not carry the bold compare label: %q", notes)
	}
	if strings.Contains(notes, "Full Changelog:") {
		t.Errorf("notes contain a bare `Full Changelog:`, so a substring check for it would pass on a form that is not the rendered one: %q", notes)
	}
	if !strings.Contains(notes, "Full Changelog") {
		t.Errorf("notes do not mention the changelog at all: %q", notes)
	}
}

// TestCompareURL pins the link format, which AGENTS.md's release policy requires
// in every note.
func TestCompareURL(t *testing.T) {
	t.Parallel()

	got := CompareURL("v1.1.0", "v1.2.0")
	want := "https://github.com/dmundt/go-cask/compare/v1.1.0...v1.2.0"
	if got != want {
		t.Errorf("CompareURL = %q, want %q", got, want)
	}
}

// TestPreviousTag pins the selection: the first tag that is not the tag itself,
// from a list already in Git's version-descending order.
func TestPreviousTag(t *testing.T) {
	t.Parallel()

	tags := []string{"v1.3.0", "v1.2.0", "v1.1.0"}
	if got := PreviousTag(tags, "v1.3.0"); got != "v1.2.0" {
		t.Errorf("PreviousTag = %q, want v1.2.0", got)
	}
	if got := PreviousTag(tags, "v1.1.0"); got != "v1.3.0" {
		t.Errorf("PreviousTag for the oldest tag = %q, want the newest, which is what the shell helper picked", got)
	}
	if got := PreviousTag(nil, "v1.0.0"); got != "" {
		t.Errorf("PreviousTag with no tags = %q, want \"\"", got)
	}
	if got := PreviousTag([]string{"", "v1.0.0"}, "v1.0.0"); got != "" {
		t.Errorf("PreviousTag skipped past an empty entry to %q, want \"\"", got)
	}
}

// TestValidatePublish pins every guard and the order they are reported in, so a
// release cannot describe a tree other than the tagged one.
func TestValidatePublish(t *testing.T) {
	t.Parallel()

	sound := State{
		TagExists:    true,
		TagRevision:  "abc",
		HeadRevision: "abc",
		MainExists:   true,
		TagOnMain:    true,
	}
	if err := ValidatePublish("v1.0.0", sound); err != nil {
		t.Fatalf("ValidatePublish rejected a sound state: %v", err)
	}

	broken := []struct {
		name    string
		state   State
		wantSub string
	}{
		{name: "a dirty tree", state: State{Dirty: true, TagExists: true}, wantSub: "clean"},
		{name: "a missing tag", state: State{}, wantSub: "does not exist locally"},
		{
			name:    "a tag that is not HEAD",
			state:   State{TagExists: true, TagRevision: "abc", HeadRevision: "def", MainExists: true, TagOnMain: true},
			wantSub: "must point at HEAD",
		},
		{
			name:    "no main branch",
			state:   State{TagExists: true, TagRevision: "abc", HeadRevision: "abc"},
			wantSub: "main branch does not exist",
		},
		{
			name:    "a tag off main",
			state:   State{TagExists: true, TagRevision: "abc", HeadRevision: "abc", MainExists: true},
			wantSub: "reachable from main",
		},
	}
	for _, test := range broken {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePublish("v1.0.0", test.state)
			if err == nil {
				t.Fatalf("ValidatePublish accepted %s", test.name)
			}
			if !strings.Contains(err.Error(), test.wantSub) {
				t.Errorf("ValidatePublish(%s) = %q, want it to mention %q", test.name, err, test.wantSub)
			}
		})
	}
}

// TestAgainstTheRealChangelog is the check that matters for the release: the real
// CHANGELOG.md must yield notes for a real released tag, in the shape the notes
// have always had. It reads the committed file rather than a fixture, so a
// changelog convention change fails here.
func TestAgainstTheRealChangelog(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..", "..")
	changelog, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	if err != nil {
		t.Skipf("cannot read the committed changelog: %v", err)
	}

	// A tag the changelog certainly has; if it is ever removed, the test should be
	// updated deliberately rather than silently pass.
	const tag = "v1.6.5"
	section, err := Section(string(changelog), tag)
	if err != nil {
		t.Fatalf("Section(%s): %v", tag, err)
	}
	notes, err := Build(string(changelog), tag, "v1.6.4")
	if err != nil {
		t.Fatalf("Build(%s): %v", tag, err)
	}

	if !strings.Contains(notes, "**Full Changelog**: https://github.com/dmundt/go-cask/compare/v1.6.4...v1.6.5") {
		t.Errorf("notes for %s lack the compare link:\n%s", tag, notes)
	}
	if !strings.Contains(notes, "## ") {
		t.Errorf("notes for %s have no promoted group heading:\n%s", tag, notes)
	}
	if strings.Contains(notes, "### ") {
		t.Errorf("notes for %s still carry a `###` group heading:\n%s", tag, notes)
	}
	if strings.Contains(notes, "["+tag+"]") {
		t.Errorf("notes for %s still carry the release heading:\n%s", tag, notes)
	}
	if !strings.HasSuffix(notes, "\n") {
		t.Errorf("notes for %s do not end with a newline", tag)
	}
	if strings.Contains(notes, "\n\n\n") {
		t.Errorf("notes for %s carry a triple newline:\n%s", tag, notes)
	}
	_ = section
}
