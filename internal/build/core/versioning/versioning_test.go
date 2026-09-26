package versioning

import "testing"

// TestField pins the extractor against the shapes a document can have. The cases
// mirror what the original `awk` decided, including the two boundaries that are
// easy to get wrong: a file whose first line is not the fence has no frontmatter
// at all, and the version is compared verbatim, so trailing space is significant.
func TestField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "a versioned file",
			content: "---\ntype: Spec\ntitle: T\nversion: v12\n---\n\n# T\n",
			want:    "v12",
		},
		{
			name:    "an unversioned file with frontmatter",
			content: "---\ntype: Spec\ntitle: T\n---\n\n# T\n",
			want:    "",
		},
		{
			name:    "no frontmatter at all",
			content: "# T\n\nversion: v3\n",
			want:    "",
		},
		{
			name:    "an empty file",
			content: "",
			want:    "",
		},
		{
			name:    "a version on the first field line",
			content: "---\nversion: v1\n---\n",
			want:    "v1",
		},
		{
			name:    "an unclosed frontmatter block still yields its version",
			content: "---\nversion: v7\n\ntext\n",
			want:    "v7",
		},
		{
			name:    "a version after the closing fence is not frontmatter",
			content: "---\ntype: Spec\n---\nversion: v9\n",
			want:    "",
		},
		{
			// The original compared the field's text as written, so a trailing
			// space is part of the value and a "bump" that only removes it still
			// reads as a change.
			name:    "trailing space is preserved",
			content: "---\nversion: v2 \n---\n",
			want:    "v2 ",
		},
		{
			name:    "a fence with trailing spaces still opens the block",
			content: "--- \nversion: v4\n---\n",
			want:    "v4",
		},
		{
			name:    "a quoted version keeps its quotes",
			content: "---\nversion: \"v5\"\n---\n",
			want:    "\"v5\"",
		},
		{
			name:    "CRLF line endings are handled",
			content: "---\r\nversion: v6\r\n---\r\n",
			want:    "v6",
		},
		{
			name:    "the first version field wins",
			content: "---\nversion: v1\nversion: v2\n---\n",
			want:    "v1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Field(test.content); got != test.want {
				t.Errorf("Field() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestUnbumped pins the decision: a judged path that did not move is reported, a
// bumped one is not, and an unjudged one — no version in the base, so the rule
// cannot apply — is skipped rather than reported or silently passing.
func TestUnbumped(t *testing.T) {
	t.Parallel()

	results := []Result{
		{Path: "docs/a.md", Before: "v1", After: "v2", Judged: true},  // bumped
		{Path: "docs/b.md", Before: "v3", After: "v3", Judged: true},  // not bumped
		{Path: "docs/c.md", Before: "", After: "v1", Judged: false},   // new file
		{Path: "docs/d.md", Before: "v1", After: "v1", Judged: true},  // not bumped
		{Path: "docs/e.md", Before: "v9", After: "v10", Judged: true}, // bumped
		{Path: "docs/f.md", Before: "v1", After: "", Judged: false},   // version removed in base? not judged
	}

	got := Unbumped(results)
	want := []string{"docs/b.md", "docs/d.md"}
	if len(got) != len(want) {
		t.Fatalf("Unbumped = %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Unbumped = %q, want %q", got, want)
		}
	}
}

// TestUnbumpedIsSorted pins deterministic output: the gate prints these paths, so
// two runs over the same change must name them in the same order.
func TestUnbumpedIsSorted(t *testing.T) {
	t.Parallel()

	got := Unbumped([]Result{
		{Path: "z.md", Before: "v1", After: "v1", Judged: true},
		{Path: "a.md", Before: "v1", After: "v1", Judged: true},
		{Path: "m.md", Before: "v1", After: "v1", Judged: true},
	})
	want := []string{"a.md", "m.md", "z.md"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Unbumped = %q, want %q", got, want)
		}
	}
}

// TestJudgedIsTheRule pins that the rule does not fire on a file the base does not
// contain: the original skipped those, and reporting them would fail every new
// document on the branch that adds it.
func TestJudgedIsTheRule(t *testing.T) {
	t.Parallel()

	newFile := Result{Path: "docs/new.md", Before: "", After: "v1"}
	if newFile.Judged {
		t.Error("a file with no version in the base is judged; the rule cannot apply to it")
	}
	if unbumped := Unbumped([]Result{newFile}); len(unbumped) != 0 {
		t.Errorf("Unbumped reported a new file: %q", unbumped)
	}
}
